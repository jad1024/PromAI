package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/database"
)

// =============================================================================
// 日志分析 / 链路分析 · VictoriaLogs 实现
//
// 参考 ccfos/nightingale 的「数据查询 → 日志」交互：查询栏 + 直方图 + 日志表格
// + 字段侧栏。数据源用 VictoriaLogs LogsQL（/select/logsql/query 与 /hits）。
// 链路分析不依赖独立 APM：从日志里的 traceId 字段串出同一请求的调用链。
// =============================================================================

type LogQueryResult struct {
	Logs       []map[string]any `json:"logs"`
	Total      int              `json:"total"`
	ElapsedMs  int64            `json:"elapsed_ms"`
	Fields     []string         `json:"fields"`
	Query      string           `json:"query"`
	Error      string           `json:"error,omitempty"`
	Configured bool             `json:"configured"`
}

type LogHitsResult struct {
	Timestamps []string `json:"timestamps"`
	Values     []int64  `json:"values"`
	Total      int64    `json:"total"`
}

type LogFieldValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// victoriaLogsURL 返回配置了的环境变量优先的 VictoriaLogs 地址。
func (a *AdminAPI) victoriaLogsURL() string {
	if v := strings.TrimSpace(os.Getenv("PROMAI_VICTORIALOGS_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	if a.config == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(a.config.LogAnalysis.VictoriaLogs.URL), "/")
}

func (a *AdminAPI) logQueryTimeout() time.Duration {
	if a.config != nil && a.config.LogAnalysis.VictoriaLogs.Timeout != "" {
		if d, err := time.ParseDuration(a.config.LogAnalysis.VictoriaLogs.Timeout); err == nil {
			return d
		}
	}
	return 15 * time.Second
}

// vlGet 调用 VictoriaLogs 的查询端点，返回响应 body（调用方负责关闭）。
func (a *AdminAPI) vlGet(w http.ResponseWriter, endpoint string, params url.Values) ([]byte, bool) {
	base := a.victoriaLogsURL()
	if base == "" {
		writeError(w, 503, "未配置 VictoriaLogs 地址（设置环境变量 PROMAI_VICTORIALOGS_URL 或在 config.yaml 配置 log_analysis.victorialogs.url）")
		return nil, false
	}
	u := base + endpoint
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		writeError(w, 500, "构造查询失败: "+err.Error())
		return nil, false
	}
	client := &http.Client{Timeout: a.logQueryTimeout()}
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, 502, "VictoriaLogs 请求失败: "+err.Error())
		return nil, false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, 502, "读取 VictoriaLogs 响应失败: "+err.Error())
		return nil, false
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeError(w, 502, fmt.Sprintf("VictoriaLogs 返回 HTTP %d: %s", resp.StatusCode, truncate(string(body), 300)))
		return nil, false
	}
	return body, true
}

// handleLogQuery 日志检索：GET /api/promai/log/query
// 参数：q(LogsQL) / start / end / limit / offset / sort
func (a *AdminAPI) handleLogQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		q = "*"
	}
	params := url.Values{}
	params.Set("query", q)
	if s := r.URL.Query().Get("start"); s != "" {
		params.Set("start", s)
	} else {
		params.Set("start", "1h")
	}
	if e := r.URL.Query().Get("end"); e != "" {
		params.Set("end", e)
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	params.Set("limit", strconv.Itoa(limit))
	if off, _ := strconv.Atoi(r.URL.Query().Get("offset")); off > 0 {
		params.Set("offset", strconv.Itoa(off))
	}
	if r.URL.Query().Get("sort") == "asc" {
		params.Set("sort_direction", "asc")
	}
	begin := time.Now()
	body, ok := a.vlGet(w, "/select/logsql/query", params)
	if !ok {
		return
	}
	logs := []map[string]any{}
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	fieldSet := map[string]bool{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		logs = append(logs, item)
		for k := range item {
			fieldSet[k] = true
		}
	}
	fields := make([]string, 0, len(fieldSet))
	for k := range fieldSet {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	writeJSON(w, LogQueryResult{
		Logs: logs, Total: len(logs), ElapsedMs: time.Since(begin).Milliseconds(),
		Fields: fields, Query: q, Configured: true,
	})
}

// handleLogHits 日志直方图：GET /api/promai/log/hits
func (a *AdminAPI) handleLogHits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		q = "*"
	}
	params := url.Values{}
	params.Set("query", q)
	if s := r.URL.Query().Get("start"); s != "" {
		params.Set("start", s)
	} else {
		params.Set("start", "1h")
	}
	if e := r.URL.Query().Get("end"); e != "" {
		params.Set("end", e)
	}
	step := r.URL.Query().Get("step")
	if step == "" {
		step = "5m"
	}
	params.Set("step", step)
	body, ok := a.vlGet(w, "/select/logsql/hits", params)
	if !ok {
		return
	}
	var vl struct {
		Hits []struct {
			Fields     map[string]string `json:"fields"`
			Timestamps []string          `json:"timestamps"`
			Values     []int64           `json:"values"`
			Total      int64             `json:"total"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &vl); err != nil {
		writeError(w, 502, "解析直方图失败: "+err.Error())
		return
	}
	// 无分组时直接合并所有桶；有分组时按时间累加。
	agg := map[string]int64{}
	var order []string
	var total int64
	for _, h := range vl.Hits {
		for i, ts := range h.Timestamps {
			if i >= len(h.Values) {
				break
			}
			if _, ok := agg[ts]; !ok {
				order = append(order, ts)
			}
			agg[ts] += h.Values[i]
		}
		total += h.Total
	}
	sort.Strings(order)
	values := make([]int64, 0, len(order))
	for _, ts := range order {
		values = append(values, agg[ts])
	}
	writeJSON(w, LogHitsResult{Timestamps: order, Values: values, Total: total})
}

// handleLogFields 从查询结果里统计字段 Top N 值（参考 Nightingale 字段侧栏）。
// 参数：q / field / start / end / limit
func (a *AdminAPI) handleLogFields(w http.ResponseWriter, r *http.Request) {
	field := r.URL.Query().Get("field")
	if strings.TrimSpace(field) == "" {
		writeError(w, 400, "缺少 field")
		return
	}
	q := r.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		q = "*"
	}
	params := url.Values{}
	params.Set("query", q)
	if s := r.URL.Query().Get("start"); s != "" {
		params.Set("start", s)
	} else {
		params.Set("start", "1h")
	}
	if e := r.URL.Query().Get("end"); e != "" {
		params.Set("end", e)
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	params.Set("limit", "1000")
	body, ok := a.vlGet(w, "/select/logsql/query", params)
	if !ok {
		return
	}
	counts := map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var item map[string]any
		if err := json.Unmarshal(sc.Bytes(), &item); err != nil {
			continue
		}
		v := jsonFieldString(item, field)
		if v == "" {
			continue
		}
		counts[v]++
	}
	vals := make([]LogFieldValue, 0, len(counts))
	for v, c := range counts {
		vals = append(vals, LogFieldValue{Value: v, Count: c})
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i].Count > vals[j].Count })
	if len(vals) > limit {
		vals = vals[:limit]
	}
	writeJSON(w, map[string]any{"field": field, "values": vals})
}

func jsonFieldString(item map[string]any, field string) string {
	if v, ok := item[field]; ok {
		switch x := v.(type) {
		case string:
			return x
		default:
			b, _ := json.Marshal(x)
			return string(b)
		}
	}
	return ""
}

// handleLogStatus 仅返回是否配置，不暴露地址细节。
func (a *AdminAPI) handleLogStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"configured": a.victoriaLogsURL() != ""})
}

// =============================================================================
// 链路分析：从日志 traceId 串出调用链（不依赖独立 APM）
// =============================================================================

var traceIDKeys = []string{
	"traceId", "trace_id", "traceID", "traceid", "X-B3-TraceId", "traceparent",
	"requestId", "request_id", "requestID", "spanId", "span_id",
}

// 很多 Java 日志没有把 traceId 作为独立字段，而是直接打在消息里：
// traceId=xxx / trace_id:xxx / requestId=xxx。
// 注意：qid / 订单号这类业务 ID 不全局唯一，不能用它串调用链。
var msgTraceIDRe = regexp.MustCompile(`(?i)(?:trace[_-]?id|request[_-]?id|X-B3-TraceId)[=:]\s*([0-9a-zA-Z][0-9a-zA-Z._-]{3,63})`)

func detectTraceID(item map[string]any) string {
	for _, k := range traceIDKeys {
		if v := jsonFieldString(item, k); v != "" {
			// traceparent: 00-<traceid>-<spanid>-<flags>
			if k == "traceparent" {
				parts := strings.Split(v, "-")
				if len(parts) > 1 {
					return parts[1]
				}
			}
			return v
		}
	}
	if m := msgTraceIDRe.FindStringSubmatch(jsonFieldString(item, "_msg")); len(m) > 1 {
		return m[1]
	}
	return ""
}

// extractLinkID 按指定关联字段从日志取链路键。
// auto / trace_id: 标准 traceId；request_id: 请求标识。只认能全局定位一次请求的 ID。
func extractLinkID(item map[string]any, field string) string {
	switch field {
	case "request_id":
		if v := firstNonEmpty(jsonFieldString(item, "requestId"), jsonFieldString(item, "request_id")); v != "" {
			return v
		}
		if m := msgTraceIDRe.FindStringSubmatch(jsonFieldString(item, "_msg")); len(m) > 1 {
			return m[1]
		}
		return ""
	default:
		return detectTraceID(item)
	}
}

type TraceSpan struct {
	TraceID  string         `json:"trace_id"`
	SpanID   string         `json:"span_id,omitempty"`
	Service  string         `json:"service"`
	Method   string         `json:"method,omitempty"`
	Level    string         `json:"level,omitempty"`
	Message  string         `json:"message"`
	Time     string         `json:"time"`
	Raw      map[string]any `json:"raw"`
	Duration string         `json:"duration,omitempty"`
	SortKey  string         `json:"-"`
}

// handleTraceByID：按 traceId 查同一次请求的全部日志，按时间排成链路。
// 参数：id(traceId) / start / end
func (a *AdminAPI) handleTraceByID(w http.ResponseWriter, r *http.Request) {
	tid := strings.TrimSpace(r.URL.Query().Get("id"))
	if tid == "" {
		writeError(w, 400, "缺少 traceId")
		return
	}
	params := url.Values{}
	// 直接用 LogsQL 全文匹配 traceId；Java 日志通常把它打在消息或字段里。
	params.Set("query", strconv.Quote(tid))
	if s := r.URL.Query().Get("start"); s != "" {
		params.Set("start", s)
	} else {
		params.Set("start", "24h")
	}
	if e := r.URL.Query().Get("end"); e != "" {
		params.Set("end", e)
	}
	params.Set("limit", "500")
	params.Set("sort_direction", "asc")
	body, ok := a.vlGet(w, "/select/logsql/query", params)
	if !ok {
		return
	}
	spans := []TraceSpan{}
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var item map[string]any
		if err := json.Unmarshal(sc.Bytes(), &item); err != nil {
			continue
		}
		// 二次校验：全文匹配可能命中无关日志，要求确实用同一关联字段串起来。
		field := r.URL.Query().Get("field")
		valid := extractLinkID(item, field) == tid
		if !valid && field == "" && strings.Contains(jsonFieldString(item, "_msg"), tid) {
			valid = true // 兼容旧调用：按消息文本匹配
		}
		if !valid {
			continue
		}
		spans = append(spans, TraceSpan{
			TraceID: tid,
			SpanID:  firstNonEmpty(jsonFieldString(item, "span_id"), jsonFieldString(item, "spanId")),
			Service: firstNonEmpty(jsonFieldString(item, "service"), jsonFieldString(item, "app"), jsonFieldString(item, "application")),
			Method:  firstNonEmpty(jsonFieldString(item, "method"), jsonFieldString(item, "uri"), jsonFieldString(item, "path")),
			Level:   firstNonEmpty(jsonFieldString(item, "level"), jsonFieldString(item, "severity")),
			Message: jsonFieldString(item, "_msg"),
			Time:    jsonFieldString(item, "_time"),
			Raw:     item,
			SortKey: jsonFieldString(item, "_time"),
		})
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].SortKey < spans[j].SortKey })
	writeJSON(w, map[string]any{
		"trace_id": tid, "spans": spans, "count": len(spans),
		"configured": true,
	})
}

// handleTraceRecent 列出最近日志里出现的 traceId（链路分析入口页）。
// 参数：start / end / limit
func (a *AdminAPI) handleTraceRecent(w http.ResponseWriter, r *http.Request) {
	field := strings.TrimSpace(r.URL.Query().Get("field"))
	if field == "" {
		field = "auto"
	}
	params := url.Values{}
	params.Set("query", "*")
	if s := r.URL.Query().Get("start"); s != "" {
		params.Set("start", s)
	} else {
		params.Set("start", "1h")
	}
	if e := r.URL.Query().Get("end"); e != "" {
		params.Set("end", e)
	}
	params.Set("limit", "1000")
	body, ok := a.vlGet(w, "/select/logsql/query", params)
	if !ok {
		return
	}
	seen := map[string]map[string]any{}
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var item map[string]any
		if err := json.Unmarshal(sc.Bytes(), &item); err != nil {
			continue
		}
		// 只认能唯一标识一次请求的 traceId / requestId，不用 qid 这类业务号凑数。
		tid := extractLinkID(item, field)
		if tid == "" {
			continue
		}
		if prev, ok := seen[tid]; !ok || jsonFieldString(item, "_time") > jsonFieldString(prev, "_time") {
			seen[tid] = item
		}
	}
	type traceItem struct {
		TraceID string `json:"trace_id"`
		Service string `json:"service"`
		LastAt  string `json:"last_at"`
		Message string `json:"message"`
		Field   string `json:"field,omitempty"`
	}
	items := make([]traceItem, 0, len(seen))
	for tid, item := range seen {
		items = append(items, traceItem{
			TraceID: tid,
			Service: firstNonEmpty(jsonFieldString(item, "service"), jsonFieldString(item, "app")),
			LastAt:  jsonFieldString(item, "_time"),
			Message: truncate(jsonFieldString(item, "_msg"), 120),
			Field:   field,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].LastAt > items[j].LastAt })
	writeJSON(w, map[string]any{"items": items, "count": len(items), "field": field})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// registerLogAnalysisHandlers 注册日志分析与链路分析路由。
func (a *AdminAPI) registerLogAnalysisHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/promai/log/query", a.logRequest(a.authMiddleware(a.handleLogQuery)))
	mux.HandleFunc("/api/promai/log/hits", a.logRequest(a.authMiddleware(a.handleLogHits)))
	mux.HandleFunc("/api/promai/log/fields", a.logRequest(a.authMiddleware(a.handleLogFields)))
	mux.HandleFunc("/api/promai/log/status", a.logRequest(a.authMiddleware(a.handleLogStatus)))
	mux.HandleFunc("/api/promai/trace/recent", a.logRequest(a.authMiddleware(a.handleTraceRecent)))
	mux.HandleFunc("/api/promai/trace/by-id", a.logRequest(a.authMiddleware(a.handleTraceByID)))
}

// keep database import used (future: log analysis audit records)
var _ = database.DB
