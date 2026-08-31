package piagent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"PromAI/pkg/alerting/lts"
	"PromAI/pkg/database"
	"github.com/jay-y/pi/pkg/ai"
	agent "github.com/jay-y/pi/pkg/ai-agent"
)

// QueryLTSTool 查询华为云 LTS 日志的工具（对话场景二次深挖）。
// 依赖告警源（ExternalAlertSource）里的 AK/SK/Region/ProjectID 凭据；
// 查询结果经 Java 日志降噪漏斗折叠后再返回给 AI，控制 token 消耗。
type QueryLTSTool struct {
	db DB
}

// query_lts 工具调用上限：限制 AI 深挖次数，防止绕过 token 护栏。
const ltsToolMaxCalls = 2

func (t *QueryLTSTool) GetName() string  { return "query_lts" }
func (t *QueryLTSTool) GetLabel() string { return "查询LTS日志" }
func (t *QueryLTSTool) GetDescription() string {
	return "查询华为云 LTS 日志并返回降噪折叠摘要，用于定位告警根因"
}

func (t *QueryLTSTool) GetParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"keywords": map[string]any{
				"type":        "string",
				"description": "检索关键字（分词级，空格分隔多个词），如服务名、IP、错误码",
			},
			"log_group_id": map[string]any{
				"type":        "string",
				"description": "LTS 日志组 ID（可选，留空则从启用的华为云告警源/触发规则自动推断）",
			},
			"log_stream_id": map[string]any{
				"type":        "string",
				"description": "LTS 日志流 ID（可选，留空则从启用的华为云告警源/触发规则自动推断）",
			},
			"time_range_minutes": map[string]any{
				"type":        "integer",
				"description": "回溯时间窗（分钟），默认 15，最大 60",
			},
			"source_id": map[string]any{
				"type":        "integer",
				"description": "告警源 ID（可选，指定用哪个华为云告警源的 AK/SK 凭据）",
			},
		},
		"required": []string{"keywords"},
	}
}

func (t *QueryLTSTool) Execute(ctx context.Context, params map[string]any, onUpdate func(*agent.AgentToolResult)) (*agent.AgentToolResult, error) {
	keywords, _ := params["keywords"].(string)
	logGroupID, _ := params["log_group_id"].(string)
	logStreamID, _ := params["log_stream_id"].(string)
	window := 15
	if v, ok := params["time_range_minutes"].(float64); ok && v > 0 {
		window = int(v)
		if window > 60 {
			window = 60
		}
	}
	var sourceID uint
	if v, ok := params["source_id"].(float64); ok && v > 0 {
		sourceID = uint(v)
	}
	log.Printf("[PiAgent] 工具调用: query_lts keywords=%q group=%s stream=%s window=%dm source=%d", keywords, logGroupID, logStreamID, window, sourceID)

	if strings.TrimSpace(keywords) == "" {
		return toolLTSResult("请提供检索关键字 keywords"), nil
	}

	// 解析华为云告警源凭据（AK/SK 已加密存库，AfterFind 自动解密）
	src, err := t.resolveLTSSource(sourceID)
	if err != nil {
		return toolLTSResult(err.Error()), nil
	}
	if src == nil {
		return toolLTSResult("未找到可用的华为云告警源（需配置 AK/SK/Region/ProjectID）"), nil
	}
	// 日志组/流未显式给出时，从匹配的触发规则推断
	if logGroupID == "" || logStreamID == "" {
		g, s, found := t.inferLogGroupStream(sourceID, logGroupID, logStreamID)
		if found {
			logGroupID, logStreamID = g, s
		}
	}
	if logGroupID == "" || logStreamID == "" {
		return toolLTSResult("缺少日志组/日志流 ID：请提供 log_group_id 与 log_stream_id，或先创建绑定该日志组/流的 LTS 触发规则"), nil
	}

	end := time.Now()
	start := end.Add(-time.Duration(window) * time.Minute)

	client := lts.NewClient(src.Region, src.ProjectID, src.AccessKey, src.SecretKey)
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lines, err := client.Query(queryCtx, lts.QueryParams{
		LogGroupID:  logGroupID,
		LogStreamID: logStreamID,
		StartTime:   start,
		EndTime:     end,
		Keywords:    keywords,
		Limit:       50, // 工具场景限 50 行，控制 token
		IsDesc:      true,
	})
	if err != nil {
		return toolLTSResult(fmt.Sprintf("LTS 查询失败: %v", err)), nil
	}
	if len(lines) == 0 {
		return toolLTSResult(fmt.Sprintf("关键字 %q 在最近 %d 分钟内未检索到日志", keywords, window)), nil
	}

	folded := lts.FoldJavaLogs(lines, "ERROR,FATAL,WARN")
	summary := lts.RenderSummary(folded)
	return toolLTSResult(summary), nil
}

// resolveLTSSource 解析华为云告警源：优先按 sourceID；否则取第一个启用且有 AK/SK 的华为云源。
func (t *QueryLTSTool) resolveLTSSource(sourceID uint) (*ltsSourceCredential, error) {
	if sourceID > 0 {
		var s database.ExternalAlertSource
		if err := t.db.First(&s, sourceID).Error(); err != nil {
			return nil, fmt.Errorf("告警源 %d 不存在", sourceID)
		}
		return toLTSSourceCredential(&s), nil
	}
	// 兜底：第一个启用且有 AK/SK 的华为云告警源
	var sources []database.ExternalAlertSource
	t.db.Model(&database.ExternalAlertSource{}).Where("enabled = ? AND type = ?", true, "huaweicloud").Find(&sources)
	for i := range sources {
		if sources[i].AccessKey != "" && sources[i].SecretKey != "" {
			return toLTSSourceCredential(&sources[i]), nil
		}
	}
	return nil, nil
}

// inferLogGroupStream 从启用触发规则推断日志组/流。
// 优先匹配已给出的 group 或 stream；否则取该告警源（或任一源）绑定规则的第一组。
func (t *QueryLTSTool) inferLogGroupStream(sourceID uint, group, stream string) (string, string, bool) {
	var rules []database.AlertTriggerRule
	q := t.db.Model(&database.AlertTriggerRule{}).Where("enabled = ?", true)
	if group != "" {
		q = q.Where("log_group_id = ?", group)
	}
	if stream != "" {
		q = q.Where("log_stream_id = ?", stream)
	}
	if sourceID > 0 {
		q = q.Where("source_id = ?", sourceID)
	}
	q.Order("id desc").Limit(5).Find(&rules)
	if len(rules) == 0 {
		return "", "", false
	}
	return rules[0].LogGroupID, rules[0].LogStreamID, true
}

// ltsSourceCredential 工具内部使用的 LTS 凭据视图。
type ltsSourceCredential struct {
	Region    string
	ProjectID string
	AccessKey string
	SecretKey string
}

func toLTSSourceCredential(s *database.ExternalAlertSource) *ltsSourceCredential {
	return &ltsSourceCredential{
		Region:    s.Region,
		ProjectID: s.ProjectID,
		AccessKey: s.AccessKey,
		SecretKey: s.SecretKey,
	}
}

func toolLTSResult(text string) *agent.AgentToolResult {
	return &agent.AgentToolResult{
		Content: []ai.ContentBlock{ai.NewTextContentBlock(text)},
	}
}
