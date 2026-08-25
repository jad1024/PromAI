package sync

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"PromAI/pkg/database"
)

// 华为云 CES（云监控）规则同步。
// 通过 AK/SK 签名调用 ListAlarmRules：
//   GET https://ces.{region}.myhuaweicloud.com/V1.0/{project_id}/alarms?limit=100&start={alarm_id}
//
// 签名算法：SDK-HMAC-SHA256（华为云 API 签名 v3）。

const (
	defaultCESEndpoint = "ces.myhuaweicloud.com"
	cesAPIVersion      = "V1.0"
)

type cesAlarm struct {
	AlarmID      string `json:"alarm_id"`
	AlarmName    string `json:"alarm_name"`
	AlarmEnabled bool   `json:"alarm_enabled"`
	AlarmLevel   int    `json:"alarm_level"`
	AlarmState   string `json:"alarm_state"`
	Metric       struct {
		MetricName string `json:"metric_name"`
		Namespace  string `json:"namespace"`
		Dimensions []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"dimensions"`
	} `json:"metric"`
	Condition struct {
		ComparisonOperator string `json:"comparison_operator"`
		Value              string `json:"value"`
		Count              int    `json:"count"`
		Period             int    `json:"period"`
		Filter             string `json:"filter"`
		Unit               string `json:"unit"`
	} `json:"condition"`
	AlarmDescription string `json:"alarm_description"`
}

type cesListResponse struct {
	Alarms       []cesAlarm `json:"alarms"`
	MetaData     struct {
		Count  int    `json:"count"`
		Marker string `json:"marker"`
		Total  int    `json:"total"`
	} `json:"meta_data"`
}

func syncHuaweiCloudRules(ctx context.Context, source *database.ExternalAlertSource) (created, updated, total int, err error) {
	if source.AccessKey == "" || source.SecretKey == "" {
		return 0, 0, 0, fmt.Errorf("华为云 AK/SK 未配置")
	}
	region := source.Region
	if region == "" {
		region = "cn-north-4"
	}
	projectID := source.ProjectID
	if projectID == "" {
		return 0, 0, 0, fmt.Errorf("华为云 project_id 未配置")
	}
	endpoint := source.URL
	if endpoint == "" {
		endpoint = "https://ces." + region + ".myhuaweicloud.com"
	}
	endpoint = strings.TrimRight(endpoint, "/")

	client := &http.Client{Timeout: 30 * time.Second}
	var all []cesAlarm
	start := ""
	for {
		q := "limit=100"
		if start != "" {
			q += "&start=" + start
		}
		reqURL := fmt.Sprintf("%s/%s/%s/alarms?%s", endpoint, cesAPIVersion, projectID, q)
		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return 0, 0, 0, err
		}
		if err := signHWSRequest(req, region, projectID, source.AccessKey, source.SecretKey); err != nil {
			return 0, 0, 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("调用华为云 CES ListAlarmRules 失败: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return 0, 0, 0, fmt.Errorf("华为云 CES 返回 %d: %s", resp.StatusCode, truncate(string(body), 300))
		}
		var list cesListResponse
		if err := json.Unmarshal(body, &list); err != nil {
			return 0, 0, 0, fmt.Errorf("华为云 CES 响应解析失败: %w", err)
		}
		all = append(all, list.Alarms...)
		if len(list.Alarms) == 0 || list.MetaData.Marker == "" || list.MetaData.Marker == start {
			break
		}
		start = list.MetaData.Marker
	}

	records := make([]database.ExternalRule, 0, len(all))
	for _, a := range all {
		cond := describeCESCondition(a)
		status := "disabled"
		if a.AlarmEnabled {
			status = "enabled"
		}
		raw, _ := json.Marshal(a)
		records = append(records, database.ExternalRule{
			ExternalID: a.AlarmID,
			RuleName:   a.AlarmName,
			Severity:   cesSeverity(a.AlarmLevel),
			Status:     status,
			Condition:  cond,
			RawJSON:    string(raw),
		})
	}

	created, updated = upsertExternalRules(source, records)
	log.Printf("[ExternalSync] 华为云[%s] 拉取告警规则 %d 条", source.Name, len(all))
	return created, updated, len(all), nil
}

func describeCESCondition(a cesAlarm) string {
	op := map[string]string{
		">": ">", ">=": ">=", "<": "<", "<=": "<=",
		"eq": "==", "ne": "!=",
	}[a.Condition.ComparisonOperator]
	if op == "" {
		op = a.Condition.ComparisonOperator
	}
	metric := a.Metric.MetricName
	if a.Metric.Namespace != "" {
		metric = a.Metric.Namespace + "." + metric
	}
	cond := fmt.Sprintf("%s %s %s", metric, op, a.Condition.Value)
	if a.Condition.Unit != "" {
		cond += " " + a.Condition.Unit
	}
	if a.Condition.Period > 0 || a.Condition.Count > 0 {
		cond += fmt.Sprintf(" (周期%ds 连续%d次)", a.Condition.Period, a.Condition.Count)
	}
	if a.AlarmDescription != "" {
		cond += " | " + a.AlarmDescription
	}
	return cond
}

func cesSeverity(level int) string {
	switch level {
	case 1:
		return "critical"
	case 2:
		return "warning"
	case 3, 4:
		return "info"
	default:
		return "warning"
	}
}

// ---------- 华为云 AK/SK 签名（SDK-HMAC-SHA256） ----------

func signHWSRequest(req *http.Request, region, projectID, ak, sk string) error {
	now := time.Now().UTC()
	xSdkDate := now.Format("20060102T150405Z")
	req.Header.Set("X-Sdk-Date", xSdkDate)

	// CanonicalRequest
	method := req.Method
	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQuery := canonicalQueryString(req.URL.RawQuery)

	// 参与签名的 headers：host + x-sdk-date
	host := req.URL.Host
	canonicalHeaders := "host:" + strings.TrimSpace(host) + "\n" + "x-sdk-date:" + xSdkDate + "\n"
	signedHeaders := "host;x-sdk-date"

	canonicalRequest := strings.Join([]string{
		method, canonicalURI, canonicalQuery, canonicalHeaders, signedHeaders,
	}, "\n")
	canonicalRequestHash := sha256Hex(canonicalRequest)

	// StringToSign
	stringToSign := "SDK-HMAC-SHA256\n" + xSdkDate + "\n" + canonicalRequestHash

	// Signature
	mac := hmac.New(sha256.New, []byte(sk))
	mac.Write([]byte(stringToSign))
	signature := hex.EncodeToString(mac.Sum(nil))

	auth := fmt.Sprintf("SDK-HMAC-SHA256 Access=%s, SignedHeaders=%s, Signature=%s", ak, signedHeaders, signature)
	req.Header.Set("Authorization", auth)
	return nil
}

func canonicalQueryString(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	params := strings.Split(rawQuery, "&")
	keys := make([]string, 0, len(params))
	pairs := make(map[string]string, len(params))
	for _, p := range params {
		if p == "" {
			continue
		}
		var k, v string
		if idx := strings.Index(p, "="); idx >= 0 {
			k, v = p[:idx], p[idx+1:]
		} else {
			k = p
		}
		keys = append(keys, k)
		pairs[k] = v
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+pairs[k])
	}
	return strings.Join(parts, "&")
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
