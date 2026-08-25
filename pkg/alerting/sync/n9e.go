package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/database"
)

// n9e openapi 规则同步。
//
// 认证方式（按优先级）：
//  1. X-User-Token（n9e v8.0.0-beta.5+ 官方推荐）：个人中心创建的 Token，
//     请求头带 X-User-Token，先用 /api/n9e/self/profile 验证；
//  2. 账号密码登录（兼容 v6/v7）：POST /api/n9e/auth/login → dat.access_token，
//     请求头带 Authorization: Bearer。
//
// 规则接口优先使用 v9 跨业务组路径 /api/n9e/busi-groups/alert-rules，
// 再回退到旧版 /api/n9e/alert-rules 等；遇到 HTML（如 302 到登录页、404 页面）
// 时给出可读的诊断信息。

type n9eRule struct {
	ID        int64               `json:"id"`
	Name      string              `json:"name"`
	Severity  int                 `json:"severity"`
	Status    int                 `json:"status"`
	Expr      string              `json:"expr"`
	GroupID   int64               `json:"group_id"`
	Tags      []map[string]string `json:"tags"`
	// 宽松兼容字段（不同版本命名差异）
	RuleName   string          `json:"rule_name"`
	Enable     bool            `json:"enable"`
	Enabled    bool            `json:"enabled"`
	Disabled   int             `json:"disabled"` // v9：1=禁用 0=启用（v9 无 status 字段）
	Query      string          `json:"query"`
	PromQL     string          `json:"promql"`
	AlarmExpr  string          `json:"alarm_expr"`
	Cate       string          `json:"cate"`        // v9 数据源类型（prometheus / elasticsearch ...）
	RuleConfig json.RawMessage `json:"rule_config"` // v9 规则配置 JSON（字符串或对象，内含 prom_ql）
}

var n9eLoginPaths = []string{
	"/api/n9e/auth/login",
	"/api/auth/login",
	"/auth/login",
}

// v9 跨业务组接口优先，旧版路径兜底
var n9eRulePaths = []string{
	"/api/n9e/busi-groups/alert-rules",
	"/api/n9e/alert-rules",
	"/api/alert-rules",
	"/alert-rules",
}

var n9eProfilePaths = []string{
	"/api/n9e/self/profile",
	"/api/self/profile",
}

func syncN9ERules(ctx context.Context, source *database.ExternalAlertSource) (created, updated, total int, err error) {
	base := strings.TrimRight(source.URL, "/")
	if base == "" {
		return 0, 0, 0, fmt.Errorf("n9e 地址未配置 (url)")
	}
	client := &http.Client{Timeout: 30 * time.Second}

	var token, authHeader string // authHeader: "X-User-Token" / "Bearer"
	if source.N9eToken != "" {
		// v8+ 官方认证：X-User-Token
		token = source.N9eToken
		authHeader = "X-User-Token"
		if err := n9eVerifyToken(ctx, client, base, token); err != nil {
			return 0, 0, 0, fmt.Errorf("X-User-Token 验证失败: %w（请确认在 n9e 个人中心创建了 Token，且 n9e 配置了 [HTTP.TokenAuth] Enable=true）", err)
		}
	} else {
		// 账号密码登录（v6/v7 兼容）
		var tried []string
		var loginErr error
		token, tried, loginErr = n9eTryLogin(ctx, client, base, source.Username, source.Password)
		if loginErr != nil {
			return 0, 0, 0, fmt.Errorf("n9e 账号密码登录失败（已尝试 %s）: %w", strings.Join(tried, ", "), loginErr)
		}
		authHeader = "Bearer"
	}

	var all []n9eRule
	page := 1
	const limit = 100
	for {
		params := fmt.Sprintf("p=%d&limit=%d", page, limit)
		rules, triedPaths, err := n9eTryFetchRules(ctx, client, base, authHeader, token, params)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("拉取 n9e 规则失败（已尝试 %s）: %w", strings.Join(triedPaths, ", "), err)
		}
		all = append(all, rules...)
		if len(rules) < limit {
			break
		}
		page++
	}

	// 映射到 ExternalRule
	records := make([]database.ExternalRule, 0, len(all))
	for _, r := range all {
		name := r.Name
		if name == "" {
			name = r.RuleName
		}
		expr := n9eRuleExpr(r)
		status := "enabled"
		switch {
		case r.Disabled == 1:
			status = "disabled"
		case r.Disabled == 0 && r.RuleConfig != nil:
			status = "enabled" // v9 以 disabled 字段为准
		case r.Status == 1 || r.Enable || r.Enabled:
			status = "enabled"
		default:
			status = "disabled"
		}
		raw, _ := json.Marshal(r)
		records = append(records, database.ExternalRule{
			ExternalID: strconv.FormatInt(r.ID, 10),
			RuleName:   name,
			Severity:   n9eSeverity(r.Severity),
			Status:     status,
			Condition:  expr,
			RawJSON:    string(raw),
		})
	}

	created, updated = upsertExternalRules(source, records)
	log.Printf("[ExternalSync] n9e[%s] 拉取规则 %d 条", source.Name, len(all))
	return created, updated, len(all), nil
}

// n9eRuleExpr 依次从多个候选字段提取规则表达式，v9 时解析 rule_config 里的 prom_ql。
func n9eRuleExpr(r n9eRule) string {
	for _, s := range []string{r.Expr, r.Query, r.PromQL, r.AlarmExpr} {
		if s != "" {
			return s
		}
	}
	return extractPromQLFromRuleConfig(r.RuleConfig)
}

// extractPromQLFromRuleConfig 从 v9 的 rule_config（JSON 对象或 JSON 字符串）提取 prom_ql。
func extractPromQLFromRuleConfig(rc json.RawMessage) string {
	if len(rc) == 0 || string(rc) == "null" {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(rc, &m); err != nil {
		// 可能包了一层 JSON 字符串
		var s string
		if err2 := json.Unmarshal(rc, &s); err2 != nil {
			return ""
		}
		if err3 := json.Unmarshal([]byte(s), &m); err3 != nil {
			return ""
		}
	}
	if q := firstStr(m, "prom_ql", "promql", "promQL"); q != "" {
		return q
	}
	if queries, ok := m["queries"].([]interface{}); ok {
		for _, qi := range queries {
			qm, ok := qi.(map[string]interface{})
			if !ok {
				continue
			}
			if q := firstStr(qm, "prom_ql", "promql"); q != "" {
				return q
			}
		}
	}
	return ""
}

// n9eVerifyToken 用 X-User-Token 调 profile 接口验证 token 有效性。
func n9eVerifyToken(ctx context.Context, client *http.Client, base, token string) error {
	var tried []string
	for _, path := range n9eProfilePaths {
		url := base + path
		tried = append(tried, path)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("X-User-Token", token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			if looksLikeHTML(raw) {
				return fmt.Errorf("%s 返回 HTML（状态 %d），token 可能无效或路径错误；响应片段: %s",
					path, resp.StatusCode, truncate(stripHTML(raw), 120))
			}
			continue
		}
		if looksLikeHTML(raw) {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if e, _ := m["err"].(string); e != "" {
			return fmt.Errorf("token 无效: %s", e)
		}
		log.Printf("[ExternalSync] n9e X-User-Token 验证成功: %s", path)
		return nil
	}
	return fmt.Errorf("profile 接口不可达（已尝试 %s），请检查 n9e 服务地址", strings.Join(tried, ", "))
}

// n9eTryLogin 尝试多组登录 endpoint，返回第一个成功响应中的 access_token。
// 失败时附带各端点的 HTTP 状态码，便于诊断版本/路径问题。
func n9eTryLogin(ctx context.Context, client *http.Client, base, username, password string) (token string, tried []string, err error) {
	if username == "" {
		return "", nil, fmt.Errorf("n9e 用户名未配置（n9e v8+ 也可改用 X-User-Token 认证，无需账号密码）")
	}
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	var statuses []string
	for _, path := range n9eLoginPaths {
		url := base + path
		tried = append(tried, path)
		req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			statuses = append(statuses, fmt.Sprintf("%s=ERR(%v)", path, err))
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			statuses = append(statuses, fmt.Sprintf("%s=%d", path, resp.StatusCode))
			continue
		}
		if looksLikeHTML(raw) {
			statuses = append(statuses, fmt.Sprintf("%s=HTML", path))
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			statuses = append(statuses, fmt.Sprintf("%s=非JSON", path))
			continue
		}
		t := n9eExtractToken(m)
		if t != "" {
			log.Printf("[ExternalSync] n9e 账号密码登录成功: %s", path)
			return t, tried, nil
		}
		statuses = append(statuses, fmt.Sprintf("%s=无token字段", path))
	}
	detail := ""
	if len(statuses) > 0 {
		detail = "（各端点状态: " + strings.Join(statuses, "; ") + "）"
	}
	return "", tried, fmt.Errorf("所有登录 endpoint 均失败%s；请检查 URL、账号密码，或改用 X-User-Token 认证（n9e v8+ 官方推荐）", detail)
}

func n9eExtractToken(m map[string]interface{}) string {
	for _, k := range []string{"token", "access_token", "jwt", "id_token"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	// dat/data 可能是对象或字符串 token
	for _, k := range []string{"dat", "data"} {
		switch v := m[k].(type) {
		case string:
			if v != "" && !strings.HasPrefix(v, "{") && !strings.HasPrefix(v, "[") {
				return v
			}
		case map[string]interface{}:
			for _, kk := range []string{"access_token", "token", "jwt", "id", "value"} {
				if s, ok := v[kk].(string); ok && s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// n9eTryFetchRules 尝试多组规则 endpoint，返回分页规则列表。
// authHeader 为 "X-User-Token" 或 "Bearer"，对应两种认证方式。
func n9eTryFetchRules(ctx context.Context, client *http.Client, base, authHeader, token, params string) (rules []n9eRule, tried []string, err error) {
	for _, path := range n9eRulePaths {
		url := fmt.Sprintf("%s%s?%s", base, path, params)
		tried = append(tried, path)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			continue
		}
		if authHeader == "X-User-Token" {
			req.Header.Set("X-User-Token", token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			// 遇到 HTML 时记录更友好的诊断
			if looksLikeHTML(body) {
				return nil, tried, fmt.Errorf("%s 返回 HTML（状态 %d），可能是 URL 路径错误或 token 失效被重定向到登录页；响应片段: %s",
					path, resp.StatusCode, truncate(stripHTML(body), 120))
			}
			continue
		}
		if looksLikeHTML(body) {
			return nil, tried, fmt.Errorf("%s 返回 HTML 页面而非 JSON，请检查 n9e API 路径", path)
		}
		var list []n9eRule
		if err := extractN9EList(body, &list); err != nil {
			continue
		}
		log.Printf("[ExternalSync] n9e 规则接口成功: %s", path)
		return list, tried, nil
	}
	return nil, tried, fmt.Errorf("所有规则 endpoint 均无法获取 JSON 规则列表")
}

func extractN9EList(body []byte, out *[]n9eRule) error {
	if looksLikeHTML(body) {
		return fmt.Errorf("响应为 HTML 页面，非 JSON: %s", truncate(stripHTML(body), 120))
	}
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		// 直接是数组也接受
		var direct []n9eRule
		if err2 := json.Unmarshal(body, &direct); err2 == nil {
			*out = direct
			return nil
		}
		return fmt.Errorf("n9e 规则响应解析失败: %w", err)
	}
	var list []interface{}
	for _, k := range []string{"dat", "data", "list", "rules"} {
		if v, ok := m[k].([]interface{}); ok {
			list = v
			break
		}
	}
	if list == nil {
		// 直接是数组
		var direct []n9eRule
		if err := json.Unmarshal(body, &direct); err == nil {
			*out = direct
			return nil
		}
		return fmt.Errorf("n9e 规则响应结构未知: %s", truncate(string(body), 200))
	}
	b, _ := json.Marshal(list)
	return json.Unmarshal(b, out)
}

func n9eSeverity(s int) string {
	switch s {
	case 1:
		return "critical"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4, 5:
		return "info"
	default:
		return "warning"
	}
}

func firstStr(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// looksLikeHTML 判断响应是否是 HTML 页面（简单启发式）。
func looksLikeHTML(b []byte) bool {
	s := strings.TrimSpace(string(b))
	if len(s) == 0 {
		return false
	}
	return strings.HasPrefix(s, "<") || strings.HasPrefix(s, "<!DOCTYPE") || strings.Contains(s, "<html")
}

// stripHTML 粗略去除 HTML 标签，用于错误信息展示。
func stripHTML(b []byte) string {
	s := string(b)
	for {
		i := strings.Index(s, "<")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], ">")
		if j < 0 {
			break
		}
		s = s[:i] + " " + s[i+j+1:]
	}
	s = strings.Join(strings.Fields(s), " ")
	return s
}
