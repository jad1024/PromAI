package main

// 外部告警接入层：
//   - POST /api/promai/webhook/alerts[/:id]  接收 n9e / 华为云 SMN / 通用 webhook 告警事件
//   - GET|POST /api/promai/alert-sources     告警源 CRUD
//   - GET|PUT|DELETE /api/promai/alert-sources/:id
//   - POST /api/promai/alert-sources/:id/sync  手动同步平台规则
//   - GET /api/promai/alert-sources/:id/rules  查询同步的规则
//   - POST /api/promai/alert/instances/:fp/resolve  手动结束告警
//
// 外部告警事件统一转换为 database.AlertInstance + database.AlertHistory 记录，
// 与本地告警共用告警历史/明细视图；可选按全局通知渠道转发 + AI 根因分析。

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/alerting/sync"
	"PromAI/pkg/alerting/webhook"
	"PromAI/pkg/database"
	"PromAI/pkg/notify"
	piagent "PromAI/pkg/pi-agent"

	"gorm.io/gorm"
)

// secureTokenEqual 常量时间比较两个 token，避免时序侧信道攻击。
func secureTokenEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// ---------- Webhook 接收 ----------

func (a *AdminAPI) handleExternalWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "不支持的请求方法")
		return
	}
	id, _ := getLastPathID(r.URL.Path)
	var source database.ExternalAlertSource
	if id > 0 {
		if err := database.DB.First(&source, id).Error; err != nil {
			writeError(w, 404, "告警源不存在")
			return
		}
		if !source.Enabled {
			writeError(w, 403, "告警源已禁用")
			return
		}
	}

	// token 校验：Authorization: Bearer <token> 或 ?token=xxx
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "" {
		if len(authz) > 7 && strings.EqualFold(authz[:7], "Bearer ") {
			token = strings.TrimSpace(authz[7:])
		}
	}
	if source.ID == 0 {
		// 无 id：从启用的告警源中按 token 常量时间匹配（Token 加密存储，
		// 无法用 SQL 明文匹配，需逐条解密后比较；AfterFind 已自动解密）
		if token != "" {
			var candidates []database.ExternalAlertSource
			database.DB.Where("enabled = ?", true).Find(&candidates)
			for i := range candidates {
				if secureTokenEqual(token, candidates[i].Token) {
					source = candidates[i]
					break
				}
			}
		}
	}

	// 安全加固：必须匹配到"已启用且已配置 token"的告警源才放行。
	// 未配置 token 的源不允许接收推送，防止伪造告警注入 / 通知轰炸。
	if source.ID == 0 || source.Token == "" {
		writeError(w, 401, "webhook token 校验失败")
		return
	}
	if !secureTokenEqual(token, source.Token) {
		writeError(w, 401, "webhook token 校验失败")
		return
	}

	body, err := readBody(r, 4<<20) // 4MB 上限
	if err != nil {
		writeError(w, 400, "读取请求体失败: "+err.Error())
		return
	}
	sourceType := source.Type
	if sourceType == "" {
		sourceType = "generic"
	}
	events, err := webhook.Parse(body, sourceType)
	if err != nil {
		log.Printf("[ExternalWebhook] 解析失败: %v", err)
		writeError(w, 400, "告警事件解析失败: "+err.Error())
		return
	}

	accepted := 0
	for i := range events {
		if err := processExternalEvent(r.Context(), &events[i], &source); err != nil {
			log.Printf("[ExternalWebhook] 处理事件失败: %v", err)
			continue
		}
		accepted++
	}
	log.Printf("[ExternalWebhook] 源[%s] 收到 %d 个事件，处理 %d 个", source.Name, len(events), accepted)
	writeJSON(w, map[string]interface{}{
		"received": len(events), "accepted": accepted, "source": source.Name,
	})
}

// processExternalEvent 处理单个外部告警事件
func processExternalEvent(ctx context.Context, ev *webhook.AlertEvent, source *database.ExternalAlertSource) error {
	ev.Source = source.Name
	ev.SourceID = source.ID

	// 在计算 fingerprint 前，从 annotations.summary 等字段补齐 instance/device/mountpoint 等维度，
	// 避免同一 alertname 下不同实例被错误合并到同一个 fingerprint。
	webhook.EnrichInstanceLabels(ev)

	// 华为云 SMN 订阅确认：回访 subscribe_url 完成订阅
	if ev.State == "confirm" {
		if u := ev.Annotations[webhook.SubscribeURLKey]; u != "" {
			go func(url string) {
				resp, err := http.Get(url)
				if err != nil {
					log.Printf("[ExternalWebhook] SMN 订阅确认失败: %v", err)
					return
				}
				resp.Body.Close()
				log.Printf("[ExternalWebhook] SMN 订阅确认成功 (HTTP %d)", resp.StatusCode)
			}(u)
		}
		return nil
	}

	if ev.ExternalID == "" {
		ev.ExternalID = fmt.Sprintf("%s|%s", ev.RuleName, hashLabelsForFP(ev.Labels))
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now()
	}
	fp := externalFingerprint(ev)
	now := time.Now()

	switch ev.State {
	case "firing", "pending":
		return upsertExternalAlert(ctx, ev, source, fp, now)
	case "resolved":
		return resolveExternalAlert(ctx, ev, source, fp, now)
	default:
		return nil
	}
}

// upsertExternalAlert 写入/更新活跃告警实例 + 追加历史事件 + 通知 + AI 分析
func upsertExternalAlert(ctx context.Context, ev *webhook.AlertEvent, source *database.ExternalAlertSource, fp string, now time.Time) error {
	// 关联镜像规则（origin=sync 的外部规则），让告警详情能展示真实规则名而非"规则 #0"
	var linkedRuleID uint
	var linkedRule database.AlertRule
	if ev.ExternalID != "" {
		if err := database.DB.Where("origin = ? AND origin_source_id = ? AND origin_external_id = ?",
			"sync", source.ID, ev.ExternalID).First(&linkedRule).Error; err == nil {
			linkedRuleID = linkedRule.ID
		}
	}
	// 规则名兜底写入 labels.alertname，保证任何视图都能展示规则名
	if ev.RuleName != "" && ev.Labels["alertname"] == "" {
		ev.Labels["alertname"] = ev.RuleName
	}
	if ev.Labels["datasource_name"] == "" {
		ev.Labels["datasource_name"] = source.Name
	}
	labelsJSON, _ := json.Marshal(ev.Labels)
	annotationsJSON, _ := json.Marshal(ev.Annotations)

	var inst database.AlertInstance
	err := database.DB.Where("fingerprint = ?", fp).First(&inst).Error
	if err == gorm.ErrRecordNotFound {
		inst = database.AlertInstance{
			Fingerprint:      fp,
			RuleID:           linkedRuleID,
			DatasourceID:     0,
			ExternalSourceID: source.ID,
			LabelsJSON:       string(labelsJSON),
			AnnotationsJSON:  string(annotationsJSON),
			State:            ev.State,
			Severity:         ev.Severity,
			Value:            ev.Value,
			Threshold:        ev.Threshold,
			ActiveAt:         ev.OccurredAt,
			FiredAt:          &ev.OccurredAt,
			LastEvalAt:       now,
			GroupKey:         "ext_" + fp[:12],
			UnreadCount:      1, // 新告警：未读 +1
			FiringCount:      1, // 首次触发
		}
		if ev.State == "firing" {
			inst.FiredAt = &ev.OccurredAt
		}
		if err := database.DB.Create(&inst).Error; err != nil {
			return fmt.Errorf("写入外部告警实例失败: %w", err)
		}
		log.Printf("[ExternalWebhook] 新增外部告警实例 fp=%s rule=%s state=%s", fp[:12], ev.RuleName, ev.State)
	} else {
		updates := map[string]interface{}{
			"labels_json": string(labelsJSON), "annotations_json": string(annotationsJSON),
			"state": ev.State, "severity": ev.Severity, "value": ev.Value,
			"threshold": ev.Threshold,
			"active_at": ev.OccurredAt, "last_eval_at": now,
		}
		if linkedRuleID != 0 && inst.RuleID == 0 {
			updates["rule_id"] = linkedRuleID
		}
		if inst.ExternalSourceID == 0 && source.ID != 0 {
			updates["external_source_id"] = source.ID
		}
		// 未读/触发计数：仅当再次从恢复状态转为告警（新告警）时累加，重发不累加
		if inst.State == "resolved" && (ev.State == "firing" || ev.State == "pending") {
			updates["unread_count"] = inst.UnreadCount + 1
			updates["firing_count"] = inst.FiringCount + 1
			updates["fired_at"] = ev.OccurredAt
		} else if ev.State == "firing" {
			// 每次收到 firing 事件都累加触发次数（含重复推送）
			updates["firing_count"] = inst.FiringCount + 1
			if inst.FiredAt == nil {
				updates["fired_at"] = ev.OccurredAt
			}
		} else if ev.State == "pending" && inst.FiredAt == nil {
			updates["fired_at"] = ev.OccurredAt
		}
		if err := database.DB.Model(&database.AlertInstance{}).Where("fingerprint = ?", fp).Updates(updates).Error; err != nil {
			return fmt.Errorf("更新外部告警实例失败: %w", err)
		}
		log.Printf("[ExternalWebhook] 更新外部告警实例 fp=%s rule=%s state=%s", fp[:12], ev.RuleName, ev.State)
	}

	// 追加历史事件（pending 不落历史，避免噪音）
	if ev.State == "firing" {
		history := database.AlertHistory{
			Fingerprint:     fp,
			RuleID:          linkedRuleID,
			RuleName:        fmt.Sprintf("[%s] %s", source.Name, ev.RuleName),
			DatasourceID:    0,
			DatasourceName:  source.Name,
			State:           "firing",
			Severity:        ev.Severity,
			Value:           ev.Value,
			Threshold:       ev.Threshold,
			LabelsJSON:      string(labelsJSON),
			AnnotationsJSON: string(annotationsJSON),
			EventType:       "firing",
			OccurredAt:      ev.OccurredAt,
			CreatedAt:       now,
		}
		if err := database.DB.Create(&history).Error; err != nil {
			log.Printf("[ExternalWebhook] 写入外部告警历史失败: %v", err)
		}

		if source.NotifyEnabled {
			go safeNotifyExternal(ev, source, "告警触发")
		}
		if source.AIAnalysisEnabled && piagent.DefaultAgentHandler != nil && piagent.DefaultAgentHandler.AIEnabled() {
			go safeAnalyzeExternal(ev, source, fp)
		}
	}
	return nil
}

// resolveExternalAlert 结束外部告警：更新实例为 resolved + 追加恢复历史
func resolveExternalAlert(ctx context.Context, ev *webhook.AlertEvent, source *database.ExternalAlertSource, fp string, now time.Time) error {
	var inst database.AlertInstance
	err := database.DB.Where("fingerprint = ?", fp).First(&inst).Error
	labelsJSON, _ := json.Marshal(ev.Labels)
	annotationsJSON, _ := json.Marshal(ev.Annotations)

	if err == nil && (inst.State == "firing" || inst.State == "pending") {
		if e := database.DB.Model(&database.AlertInstance{}).Where("fingerprint = ?", fp).
			Updates(map[string]interface{}{
				"state":        "resolved",
				"resolved_at":  now,
				"last_eval_at": now,
				"unread_count": 0, // 恢复后清零未读
			}).Error; e != nil {
			return fmt.Errorf("结束外部告警失败: %w", e)
		}
		log.Printf("[ExternalWebhook] 外部告警恢复 fp=%s rule=%s", fp[:12], ev.RuleName)
	} else if err == gorm.ErrRecordNotFound {
		log.Printf("[ExternalWebhook] 收到恢复事件但无活跃实例 fp=%s（忽略）", fp[:12])
		return nil
	}

	history := database.AlertHistory{
		Fingerprint:     fp,
		RuleID:          inst.RuleID,
		RuleName:        fmt.Sprintf("[%s] %s", source.Name, ev.RuleName),
		DatasourceID:    0,
		DatasourceName:  source.Name,
		State:           "resolved",
		Severity:        ev.Severity,
		Value:           ev.Value,
		Threshold:       inst.Threshold,
		LabelsJSON:      string(labelsJSON),
		AnnotationsJSON: string(annotationsJSON),
		EventType:       "resolved",
		OccurredAt:      now,
		CreatedAt:       now,
	}
	if e := database.DB.Create(&history).Error; e != nil {
		log.Printf("[ExternalWebhook] 写入外部告警恢复历史失败: %v", e)
	}
	if source.NotifyEnabled {
		go safeNotifyExternal(ev, source, "告警恢复")
	}
	return nil
}

// externalFingerprint 外部告警指纹：源ID + 平台规则ID + 标签哈希
func externalFingerprint(ev *webhook.AlertEvent) string {
	h := sha256.New()
	fmt.Fprintf(h, "src=%d|rid=%s|", ev.SourceID, ev.ExternalID)
	keys := make([]string, 0, len(ev.Labels))
	for k := range ev.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(ev.Labels[k]))
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func hashLabelsForFP(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(labels[k]))
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// ---------- 通知转发与 AI 分析 ----------

// safeNotifyExternal 将外部告警按所有启用的通知渠道转发（文本形式）
func safeNotifyExternal(ev *webhook.AlertEvent, source *database.ExternalAlertSource, action string) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[ExternalWebhook] 通知转发 panic: %v", p)
		}
	}()
	var channels []database.NotificationChannel
	database.DB.Where("enabled = ?", true).Find(&channels)
	if len(channels) == 0 {
		return
	}
	title := fmt.Sprintf("🔔 外部告警 · %s · %s", source.Name, action)
	severityIcon := map[string]string{"critical": "🔴", "warning": "🟠", "info": "🔵", "normal": "⚪"}[ev.Severity]
	var b strings.Builder
	fmt.Fprintf(&b, "%s 级别: %s\n", severityIcon, ev.Severity)
	fmt.Fprintf(&b, "来源: %s\n", source.Name)
	fmt.Fprintf(&b, "规则: %s\n", ev.RuleName)
	if ev.Value != 0 {
		fmt.Fprintf(&b, "触发值: %v\n", ev.Value)
	}
	fmt.Fprintf(&b, "时间: %s\n", ev.OccurredAt.Format("2006-01-02 15:04:05"))
	if len(ev.Labels) > 0 {
		keys := make([]string, 0, len(ev.Labels))
		for k := range ev.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("标签:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s=%s\n", k, ev.Labels[k])
		}
	}
	if s := ev.Annotations["summary"]; s != "" {
		fmt.Fprintf(&b, "摘要: %s\n", s)
	}

	ctx := context.Background()
	for _, ch := range channels {
		if err := sendExternalText(ctx, &ch, title, b.String()); err != nil {
			log.Printf("[ExternalWebhook] 渠道[%s]发送失败: %v", ch.Name, err)
		}
	}
}

func sendExternalText(ctx context.Context, ch *database.NotificationChannel, title, text string) error {
	switch ch.ChannelType {
	case "feishu":
		var cfg notify.FeishuConfig
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &cfg); err != nil {
			return err
		}
		return notify.SendFeishuText(ctx, cfg, title, text)
	case "dingtalk":
		var cfg notify.DingtalkConfig
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &cfg); err != nil {
			return err
		}
		return notify.SendDingtalkText(ctx, cfg, title, text)
	case "wechat_work":
		var cfg notify.WeChatWorkConfig
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &cfg); err != nil {
			return err
		}
		return notify.SendWeChatWorkText(ctx, cfg, title, text)
	case "webhook":
		var cfg notify.WebhookConfig
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &cfg); err != nil {
			return err
		}
		return notify.SendWebhookText(ctx, cfg, title, text)
	default:
		return fmt.Errorf("渠道类型 %s 暂不支持外部告警文本推送", ch.ChannelType)
	}
}

// safeAnalyzeExternal 外部告警 AI 根因分析（异步）
func safeAnalyzeExternal(ev *webhook.AlertEvent, source *database.ExternalAlertSource, fp string) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[ExternalWebhook] AI 分析 panic: %v", p)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := piagent.DefaultAgentHandler.AnalyzeExternalAlertAndRecord(ctx, source.Name, ev, fp)
	if err != nil {
		log.Printf("[ExternalWebhook] 外部告警 AI 分析失败: %v", err)
		return
	}
	log.Printf("[ExternalWebhook] 外部告警 AI 分析完成 fp=%s len=%d", fp[:12], len(res.Text))
}

// ---------- 告警源 CRUD ----------

func (a *AdminAPI) handleAlertSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		var list []database.ExternalAlertSource
		database.DB.Order("created_at desc").Find(&list)
		for i := range list {
			sanitizeExternalSource(&list[i])
		}
		writeJSON(w, list)
	case "POST":
		var s database.ExternalAlertSource
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			writeError(w, 400, "请求体格式错误")
			return
		}
		if s.Name == "" || s.Type == "" {
			writeError(w, 400, "名称和类型不能为空")
			return
		}
		switch s.Type {
		case "n9e", "huaweicloud", "aliyun", "generic":
		default:
			writeError(w, 400, "类型仅支持 n9e / huaweicloud / aliyun / generic")
			return
		}
		if s.SyncInterval == "" {
			s.SyncInterval = "1h"
		}
		if err := database.DB.Create(&s).Error; err != nil {
			writeError(w, 500, "创建告警源失败: "+err.Error())
			return
		}
		sanitizeExternalSource(&s)
		writeJSON(w, s)
	default:
		writeError(w, 405, "不支持的请求方法")
	}
}

func (a *AdminAPI) handleAlertSourceByID(w http.ResponseWriter, r *http.Request) {
	var id uint
	var idErr error
	// 子路径 /:id/sync 或 /:id/rules 的 ID 在倒数第二位
	if strings.HasSuffix(r.URL.Path, "/sync") || strings.HasSuffix(r.URL.Path, "/rules") {
		id, idErr = parseParentID(r.URL.Path)
	} else {
		id, idErr = getLastPathID(r.URL.Path)
	}
	if id == 0 || idErr != nil {
		writeError(w, 400, "无效的告警源 ID")
		return
	}

	// 子路径分派：/sync 手动同步
	if strings.HasSuffix(r.URL.Path, "/sync") && r.Method == "POST" {
		a.handleAlertSourceSync(w, r, id)
		return
	}
	// /rules 规则列表
	if strings.HasSuffix(r.URL.Path, "/rules") && r.Method == "GET" {
		a.handleAlertSourceRules(w, r, id)
		return
	}

	var s database.ExternalAlertSource
	if err := database.DB.First(&s, id).Error; err != nil {
		writeError(w, 404, "告警源不存在")
		return
	}

	switch r.Method {
	case "GET":
		sanitizeExternalSource(&s)
		writeJSON(w, s)
	case "PUT":
		var req database.ExternalAlertSource
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "请求体格式错误")
			return
		}
		updates := map[string]interface{}{
			"name": req.Name, "type": req.Type, "enabled": req.Enabled, "url": req.URL,
			"region": req.Region, "project_id": req.ProjectID, "username": req.Username,
			"sync_interval": req.SyncInterval, "notify_enabled": req.NotifyEnabled,
			"ai_analysis_enabled": req.AIAnalysisEnabled,
		}
		// 凭据字段：空值表示不修改（Updates(map) 不触发 hook，需手动加密）
		if req.AccessKey != "" {
			updates["access_key"] = req.AccessKey
		}
		if req.SecretKey != "" {
			updates["secret_key"] = encryptSecret(req.SecretKey)
		}
		if req.Password != "" {
			updates["password"] = encryptSecret(req.Password)
		}
		// n9e_token / token：直接覆盖（允许传空串清空，从而切回账号密码登录）。
		// 前端编辑时回填真实 token，不再脱敏，避免错误 token 无法更新。
		updates["n9e_token"] = encryptSecret(req.N9eToken)
		updates["token"] = encryptSecret(req.Token)
		if err := database.DB.Model(&database.ExternalAlertSource{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			writeError(w, 500, "更新告警源失败: "+err.Error())
			return
		}
		database.DB.First(&s, id)
		sanitizeExternalSource(&s)
		writeJSON(w, s)
	case "DELETE":
		database.DB.Where("source_id = ?", id).Delete(&database.ExternalRule{})
		if err := database.DB.Delete(&database.ExternalAlertSource{}, id).Error; err != nil {
			writeError(w, 500, "删除告警源失败: "+err.Error())
			return
		}
		w.WriteHeader(204)
	default:
		writeError(w, 405, "不支持的请求方法")
	}
}

// handleAlertSourceSync 手动触发规则同步
func (a *AdminAPI) handleAlertSourceSync(w http.ResponseWriter, r *http.Request, id uint) {
	var s database.ExternalAlertSource
	if err := database.DB.First(&s, id).Error; err != nil {
		writeError(w, 404, "告警源不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	created, updated, total, err := sync.SyncRules(ctx, &s)
	if err != nil {
		writeError(w, 500, "同步失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{
		"source": s.Name, "created": created, "updated": updated, "total": total, "status": "success",
	})
}

// handleAlertSourceRules 查询已同步的外部规则（分页）
func (a *AdminAPI) handleAlertSourceRules(w http.ResponseWriter, r *http.Request, id uint) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	var total int64
	database.DB.Model(&database.ExternalRule{}).Where("source_id = ?", id).Count(&total)
	var rules []database.ExternalRule
	database.DB.Where("source_id = ?", id).Order("status desc, updated_at desc").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&rules)
	writeJSON(w, map[string]interface{}{
		"total": total, "page": page, "page_size": pageSize, "rules": rules,
	})
}

// handleAlertResolve 手动结束一条活跃告警
// resolveHistoryIdentity 手动结束时推导历史记录的规则名/数据源名：
// 外部告警 → "[源名] 规则名" / 源名；本地告警 → 规则名 / 数据源名
func resolveHistoryIdentity(inst *database.AlertInstance) (ruleName, dsName string) {
	ruleName = "[手动结束]"
	var labels map[string]string
	if err := json.Unmarshal([]byte(inst.LabelsJSON), &labels); err == nil {
		if n := labels["alertname"]; n != "" {
			ruleName = n
		}
	}
	if inst.RuleID != 0 {
		var r database.AlertRule
		if err := database.DB.First(&r, inst.RuleID).Error; err == nil && r.Name != "" {
			ruleName = r.Name
		}
	}
	if inst.ExternalSourceID != 0 {
		var s database.ExternalAlertSource
		if err := database.DB.First(&s, inst.ExternalSourceID).Error; err == nil {
			dsName = s.Name
			if !strings.HasPrefix(ruleName, "[") {
				ruleName = fmt.Sprintf("[%s] %s", s.Name, ruleName)
			}
			return ruleName, dsName
		}
	}
	if inst.DatasourceID != 0 {
		var ds database.DataSource
		if err := database.DB.First(&ds, inst.DatasourceID).Error; err == nil {
			dsName = ds.Name
		}
	}
	if dsName == "" && labels["datasource_name"] != "" {
		dsName = labels["datasource_name"]
	}
	return ruleName, dsName
}

// handleAlertResolve 手动结束单条活跃告警（含外部告警），追加恢复历史
func (a *AdminAPI) handleAlertResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "不支持的请求方法")
		return
	}
	fp := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/promai/alert/instances/"), "/resolve")
	if fp == "" || len(fp) != 32 {
		writeError(w, 400, "无效的告警指纹")
		return
	}
	now := time.Now()
	var inst database.AlertInstance
	if err := database.DB.Where("fingerprint = ?", fp).First(&inst).Error; err != nil {
		writeError(w, 404, "告警实例不存在")
		return
	}
	if inst.State != "firing" && inst.State != "pending" {
		writeError(w, 400, "告警当前状态不是活跃状态")
		return
	}
	if err := database.DB.Model(&database.AlertInstance{}).Where("fingerprint = ?", fp).
		Updates(map[string]interface{}{"state": "resolved", "resolved_at": now}).Error; err != nil {
		writeError(w, 500, "结束告警失败: "+err.Error())
		return
	}
	// 追加恢复历史
	var labelsJSON, annotationsJSON []byte
	if inst.LabelsJSON != "" {
		labelsJSON = []byte(inst.LabelsJSON)
	}
	if inst.AnnotationsJSON != "" {
		annotationsJSON = []byte(inst.AnnotationsJSON)
	}
	ruleName, dsName := resolveHistoryIdentity(&inst)
	history := database.AlertHistory{
		Fingerprint:     fp,
		RuleID:          inst.RuleID,
		RuleName:        ruleName,
		DatasourceID:    inst.DatasourceID,
		DatasourceName:  dsName,
		State:           "resolved",
		Severity:        inst.Severity,
		Value:           inst.Value,
		LabelsJSON:      string(labelsJSON),
		AnnotationsJSON: string(annotationsJSON),
		EventType:       "resolved",
		OccurredAt:      now,
		CreatedAt:       now,
	}
	database.DB.Create(&history)
	writeJSON(w, map[string]interface{}{"message": "告警已结束", "fingerprint": fp})
}

// handleAlertInstanceBatch 批量操作：delete=删除 / resolve=结束 / silence=静默
// POST /api/promai/alert/instances/batch  {"action":"resolve","fingerprints":[...],"silence_minutes":60,"comment":"..."}
func (a *AdminAPI) handleAlertInstanceBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "不支持的请求方法")
		return
	}
	var req struct {
		Action         string   `json:"action"` // delete / resolve / silence / read
		Fingerprints   []string `json:"fingerprints"`
		SilenceMinutes int      `json:"silence_minutes"`
		Comment        string   `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "请求体格式错误: "+err.Error())
		return
	}
	if len(req.Fingerprints) == 0 {
		writeError(w, 400, "fingerprints 不能为空")
		return
	}
	if req.Action == "" {
		req.Action = "resolve"
	}
	switch req.Action {
	case "delete", "resolve", "silence", "read":
	default:
		writeError(w, 400, "action 仅支持 delete / resolve / silence / read")
		return
	}
	now := time.Now()
	done := 0
	var errs []string

	for _, fp := range req.Fingerprints {
		var inst database.AlertInstance
		if err := database.DB.Where("fingerprint = ?", fp).First(&inst).Error; err != nil {
			errs = append(errs, fp[:12]+": 不存在")
			continue
		}
		switch req.Action {
		case "delete":
			if err := database.DB.Delete(&database.AlertInstance{}, inst.ID).Error; err != nil {
				errs = append(errs, fp[:12]+": "+err.Error())
				continue
			}
			// 手动删除实时告警：没有恢复记录，同步软删该 fingerprint 的历史事件，
			// 聚合故障随之消失（removed_at IS NULL 过滤）。
			if err := database.DB.Model(&database.AlertHistory{}).
				Where("fingerprint = ? AND removed_at IS NULL", fp).
				Update("removed_at", now).Error; err != nil {
				errs = append(errs, fp[:12]+": "+err.Error())
				continue
			}
		case "resolve":
			if inst.State == "firing" || inst.State == "pending" {
				if err := database.DB.Model(&database.AlertInstance{}).Where("id = ?", inst.ID).
					Updates(map[string]interface{}{"state": "resolved", "resolved_at": now}).Error; err != nil {
					errs = append(errs, fp[:12]+": "+err.Error())
					continue
				}
				appendResolveHistory(&inst, now)
			}
		case "read":
			if err := database.DB.Model(&database.AlertInstance{}).Where("id = ?", inst.ID).
				Update("unread_count", 0).Error; err != nil {
				errs = append(errs, fp[:12]+": "+err.Error())
				continue
			}
		case "silence":
			minutes := req.SilenceMinutes
			if minutes <= 0 {
				minutes = 60
			}
			comment := strings.TrimSpace(req.Comment)
			if comment == "" {
				comment = "批量静默: " + inst.Fingerprint[:12]
			}
			if err := createSilenceForInstance(&inst, comment, minutes); err != nil {
				errs = append(errs, fp[:12]+": "+err.Error())
				continue
			}
		}
		done++
	}
	writeJSON(w, map[string]interface{}{"action": req.Action, "done": done, "failed": len(errs), "errors": errs})
}

// appendResolveHistory 手动结束时追加恢复历史（复用 handleAlertResolve 逻辑）
func appendResolveHistory(inst *database.AlertInstance, now time.Time) {
	ruleName, dsName := resolveHistoryIdentity(inst)
	history := database.AlertHistory{
		Fingerprint:     inst.Fingerprint,
		RuleID:          inst.RuleID,
		RuleName:        ruleName,
		DatasourceID:    inst.DatasourceID,
		DatasourceName:  dsName,
		State:           "resolved",
		Severity:        inst.Severity,
		Value:           inst.Value,
		LabelsJSON:      inst.LabelsJSON,
		AnnotationsJSON: inst.AnnotationsJSON,
		EventType:       "resolved",
		OccurredAt:      now,
		CreatedAt:       now,
	}
	database.DB.Create(&history)
}

// createSilenceForInstance 为单个告警实例创建静默规则（基于 labels 生成 matcher）
func createSilenceForInstance(inst *database.AlertInstance, comment string, minutes int) error {
	var labels map[string]string
	if err := json.Unmarshal([]byte(inst.LabelsJSON), &labels); err != nil || len(labels) == 0 {
		return fmt.Errorf("实例标签为空，无法生成静默匹配条件")
	}
	type matcher struct {
		Name  string `json:"name"`
		Op    string `json:"op"`
		Value string `json:"value"`
	}
	ms := make([]matcher, 0, 3)
	if v := labels["alertname"]; v != "" {
		ms = append(ms, matcher{Name: "alertname", Op: "=", Value: v})
	}
	for _, k := range []string{"instance", "job", "datasource_name"} {
		if v := labels[k]; v != "" {
			ms = append(ms, matcher{Name: k, Op: "=", Value: v})
		}
	}
	if len(ms) == 0 {
		for k, v := range labels {
			ms = append(ms, matcher{Name: k, Op: "=", Value: v})
			break
		}
	}
	mj, _ := json.Marshal(ms)
	now := time.Now()
	return database.DB.Create(&database.AlertSilence{
		Comment:      comment,
		CreatedBy:    "system",
		MatchersJSON: string(mj),
		StartsAt:     now,
		EndsAt:       now.Add(time.Duration(minutes) * time.Minute),
		Enabled:      true,
	}).Error
}

// ---------- 工具 ----------

func sanitizeExternalSource(s *database.ExternalAlertSource) {
	s.SecretKey = ""
	s.Password = ""
	// n9e_token / token 不再脱敏：用户需要能在编辑时看到/修改/清空 token（否则填错的 token 永远无法更新）
}

func readBody(r *http.Request, max int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, max))
}

// ---------- 周期同步调度 ----------

// startExternalSyncScheduler 启动外部告警源规则周期同步（每 60s 检查一次到期任务）
func startExternalSyncScheduler() {
	go func() {
		time.Sleep(10 * time.Second) // 等系统启动完成
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for range t.C {
			runDueExternalSyncs()
		}
	}()
	log.Printf("[ExternalSync] 外部告警源周期同步调度已启动（每 60s 检查）")
}

func runDueExternalSyncs() {
	var sources []database.ExternalAlertSource
	database.DB.Where("enabled = ?", true).Find(&sources)
	now := time.Now()
	for _, src := range sources {
		if src.Type == "generic" {
			continue // 通用 webhook 源无规则可同步
		}
		if src.URL == "" {
			continue
		}
		interval := parseSyncInterval(src.SyncInterval)
		if interval <= 0 {
			continue
		}
		if src.LastSyncAt != nil && now.Sub(*src.LastSyncAt) < interval {
			continue
		}
		s := src
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		created, updated, total, err := sync.SyncRules(ctx, &s)
		cancel()
		if err != nil {
			log.Printf("[ExternalSync] 周期同步失败 [%s]: %v", src.Name, err)
		} else {
			log.Printf("[ExternalSync] 周期同步完成 [%s]: 新增%d 更新%d 共%d", src.Name, created, updated, total)
		}
	}
}

func parseSyncInterval(s string) time.Duration {
	if s == "" {
		return time.Hour
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "30m", "30min":
			return 30 * time.Minute
		case "1d", "daily":
			return 24 * time.Hour
		case "1h", "hourly", "hour":
			return time.Hour
		}
		return time.Hour
	}
	return d
}
