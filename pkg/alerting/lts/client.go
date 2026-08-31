package lts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Client 华为云 LTS 客户端（AK/SK 认证，只读检索日志）。
type Client struct {
	Region    string
	ProjectID string
	AccessKey string
	SecretKey string
	Endpoint  string // 可空，默认 lts.{region}.myhuaweicloud.com
	HTTP      *http.Client
}

// NewClient 构造 LTS 客户端。endpoint 为空时使用默认域名。
func NewClient(region, projectID, ak, sk string) *Client {
	if region == "" {
		region = "cn-north-4"
	}
	c := &Client{
		Region:    region,
		ProjectID: projectID,
		AccessKey: ak,
		SecretKey: sk,
		Endpoint:  "https://lts." + region + ".myhuaweicloud.com",
	}
	c.HTTP = &http.Client{Timeout: 30 * time.Second}
	return c
}

// QueryParams LTS 日志检索参数。
type QueryParams struct {
	LogGroupID  string            // 日志组 ID（必填）
	LogStreamID string            // 日志流 ID（必填）
	StartTime   time.Time         // 检索起始（UTC）
	EndTime     time.Time         // 检索结束（UTC）
	Keywords    string            // 关键词（分词级，空格分隔，可选）
	Labels      map[string]string // 日志过滤标签（可选）
	Limit       int               // 返回行数上限，1..5000，默认 200
	IsDesc      bool              // 是否按时间倒序；false 为按时间正序（旧→新）
}

// ltsQueryRequest LTS 查询日志请求体（start_time/end_time 为毫秒时间戳字符串）。
type ltsQueryRequest struct {
	StartTime string            `json:"start_time"`
	EndTime   string            `json:"end_time"`
	Labels    map[string]string `json:"labels,omitempty"`
	Keywords  string            `json:"keywords,omitempty"`
	IsDesc    bool              `json:"is_desc"`
	Limit     int               `json:"limit"`
	IsCount   bool              `json:"is_count"`
}

// ltsQueryResponse LTS 查询日志响应。
type ltsQueryResponse struct {
	Logs []struct {
		Content string            `json:"content"`
		Labels  map[string]string `json:"labels"`
		LineNum string            `json:"line_num"`
	} `json:"logs"`
	Count           int    `json:"count"`
	IsQueryComplete bool   `json:"isQueryComplete"`
	ErrorCode       string `json:"error_code"`
	ErrorMessage    string `json:"error_message"`
}

// Query 检索日志，返回日志内容行（按时间正序）。失败返回 error，调用方降级处理。
func (c *Client) Query(ctx context.Context, p QueryParams) ([]string, error) {
	if c.AccessKey == "" || c.SecretKey == "" {
		return nil, fmt.Errorf("华为云 AK/SK 未配置")
	}
	if c.ProjectID == "" {
		return nil, fmt.Errorf("华为云 project_id 未配置")
	}
	projectID := c.ProjectID
	if p.LogGroupID == "" || p.LogStreamID == "" {
		return nil, fmt.Errorf("log_group_id / log_stream_id 不能为空")
	}
	if p.Limit <= 0 {
		p.Limit = 200
	}
	if p.Limit > 5000 {
		p.Limit = 5000
	}
	if p.StartTime.IsZero() || p.EndTime.IsZero() {
		return nil, fmt.Errorf("start_time / end_time 不能为空")
	}

	reqBody := ltsQueryRequest{
		StartTime: fmt.Sprintf("%d", p.StartTime.UnixMilli()),
		EndTime:   fmt.Sprintf("%d", p.EndTime.UnixMilli()),
		Labels:    p.Labels,
		Keywords:  p.Keywords,
		IsDesc:    p.IsDesc,
		Limit:     p.Limit,
		IsCount:   true,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化 LTS 请求失败: %w", err)
	}

	url := fmt.Sprintf("%s/v2/%s/groups/%s/streams/%s/content/query",
		strings.TrimRight(c.Endpoint, "/"), projectID, p.LogGroupID, p.LogStreamID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	if err := signHWSRequest(req, c.AccessKey, c.SecretKey); err != nil {
		return nil, fmt.Errorf("LTS 请求签名失败: %w", err)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用华为云 LTS 查询失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("华为云 LTS 返回 %d: %s", resp.StatusCode, truncateString(string(raw), 300))
	}

	var parsed ltsQueryResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("华为云 LTS 响应解析失败: %w", err)
	}
	if parsed.ErrorCode != "" {
		return nil, fmt.Errorf("华为云 LTS 业务错误 %s: %s", parsed.ErrorCode, parsed.ErrorMessage)
	}

	lines := make([]string, 0, len(parsed.Logs))
	for _, lg := range parsed.Logs {
		if lg.Content == "" {
			continue
		}
		lines = append(lines, lg.Content)
	}
	log.Printf("[LTS] 查询完成: 组=%s 流=%s 返回=%d 行 (count=%d)", p.LogGroupID, p.LogStreamID, len(lines), parsed.Count)
	return lines, nil
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
