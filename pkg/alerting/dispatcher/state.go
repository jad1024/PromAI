package dispatcher

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"PromAI/pkg/alerting"
	"PromAI/pkg/alerting/evaluator"
	"PromAI/pkg/alerting/store"
	"PromAI/pkg/database"

	"gorm.io/gorm"
)

// stateManager 把 Sample 流转换成 AlertInstance 的状态变迁。
//
// 内存中持有 fingerprint → *trackedAlert，flush 时批量写 DB。
// 为避免 evaluator 在每轮空 vector 时找不到旧实例，stateManager 同时维护
// (rule_id, ds_id) 维度的"上一轮命中过的 fingerprint 集合"，
// 当本轮收到 Active=false 的"全恢复样本"或扫描到 stale fingerprint 时驱动 resolved。
type stateManager struct {
	d *Dispatcher

	mu      sync.Mutex
	tracked map[string]*trackedAlert // key=fingerprint

	// (rule_id, ds_id) → 上一轮活跃 fingerprint 集合
	lastSeen map[ruleDsKey]map[string]struct{}
}

type ruleDsKey struct {
	rule uint
	ds   uint
}

type trackedAlert struct {
	fingerprint string
	rule        *database.AlertRule
	ds          *database.DataSource
	ruleID      uint
	dsID        uint
	labels      alerting.LabelSet
	value       float64
	threshold   float64
	severity    string
	state       string // pending / firing / resolved
	activeAt    time.Time
	firedAt     *time.Time
	resolvedAt  *time.Time
	lastEvalAt  time.Time

	// 本轮是否被采样命中（决定是否在 flush 时进入 resolved）
	seenThisRound bool
	// 本轮 evaluator 报告了 Active=true（仍然命中阈值）
	stillActive bool
	// 是否需要持久化（dirty）
	dirty bool

	annotations alerting.LabelSet
	groupKey    string
	silencedBy  []uint
	inhibitedBy []uint
}

func newStateManager(d *Dispatcher) *stateManager {
	sm := &stateManager{
		d:        d,
		tracked:  make(map[string]*trackedAlert, 4096),
		lastSeen: make(map[ruleDsKey]map[string]struct{}, 256),
	}
	// 启动时从 DB 恢复活跃实例（避免重启丢失 firing 状态）
	sm.recoverFromDB()
	return sm
}

func (sm *stateManager) recoverFromDB() {
	var rows []database.AlertInstance
	if err := database.DB.Where("state IN ?", []string{"pending", "firing"}).Find(&rows).Error; err != nil {
		return
	}
	for i := range rows {
		r := rows[i]
		labels := alerting.DecodeLabels(r.LabelsJSON)
		var ann alerting.LabelSet
		_ = json.Unmarshal([]byte(r.AnnotationsJSON), &ann)
		t := &trackedAlert{
			fingerprint: r.Fingerprint,
			ruleID:      r.RuleID,
			dsID:        r.DatasourceID,
			labels:      labels,
			annotations: ann,
			value:       r.Value,
			threshold:   r.Threshold,
			severity:    r.Severity,
			state:       r.State,
			activeAt:    r.ActiveAt,
			firedAt:     r.FiredAt,
			resolvedAt:  r.ResolvedAt,
			lastEvalAt:  r.LastEvalAt,
			groupKey:    r.GroupKey,
		}
		sm.tracked[r.Fingerprint] = t
		// 我们没有 rule / ds 指针；evaluator 下一轮会重新填充。
		// 若 (rule, ds) 在内存中被删除，下面 sweepStale 会驱动 resolved。
		k := ruleDsKey{rule: r.RuleID, ds: r.DatasourceID}
		set, ok := sm.lastSeen[k]
		if !ok {
			set = make(map[string]struct{}, 4)
			sm.lastSeen[k] = set
		}
		set[r.Fingerprint] = struct{}{}
	}
	if len(sm.tracked) > 0 {
		log.Printf("[Alerting] state: recovered %d active alert instances from DB", len(sm.tracked))
	}
}

// clear 清空所有内存中追踪的告警实例
func (sm *stateManager) clear() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.tracked = make(map[string]*trackedAlert, 64)
	sm.lastSeen = make(map[ruleDsKey]map[string]struct{}, 64)
}

// absorb 处理一个 Sample
func (sm *stateManager) absorb(s evaluator.Sample) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if s.Rule == nil || s.Datasource == nil {
		return
	}

	// Active=false + Labels 为空：表示 evaluator 本轮在该 (rule, ds) 上完全没命中
	// → 对该 (rule, ds) 内所有上一轮活跃 fingerprint 标记为"未再命中"
	if !s.Active && len(s.Labels) == 0 {
		k := ruleDsKey{rule: s.Rule.ID, ds: s.Datasource.ID}
		if set, ok := sm.lastSeen[k]; ok {
			for fp := range set {
				if t, ok := sm.tracked[fp]; ok {
					t.seenThisRound = true
					t.stillActive = false
					t.lastEvalAt = s.EvalAt
					t.dirty = true
				}
			}
		}
		return
	}

	fp := alerting.Fingerprint(s.Rule.ID, s.Datasource.ID, s.Labels)
	t, ok := sm.tracked[fp]
	if !ok {
		// 新告警初次出现：只有 Active 才入跟踪
		if !s.Active {
			return
		}
		t = &trackedAlert{
			fingerprint: fp,
			rule:        s.Rule,
			ds:          s.Datasource,
			ruleID:      s.Rule.ID,
			dsID:        s.Datasource.ID,
			labels:      s.Labels,
			value:       s.Value,
			threshold:   s.Threshold,
			severity:    s.Severity,
			state:       "pending",
			activeAt:    s.EvalAt,
			lastEvalAt:  s.EvalAt,
			seenThisRound: true,
			stillActive:   true,
			dirty:         true,
		}
		t.annotations = renderAnnotations(s.Rule, t.labels, t.value, t.threshold)
		sm.tracked[fp] = t
	} else {
		t.rule = s.Rule
		t.ds = s.Datasource
		t.ruleID = s.Rule.ID
		t.dsID = s.Datasource.ID
		t.value = s.Value
		t.threshold = s.Threshold
		t.severity = s.Severity
		t.lastEvalAt = s.EvalAt
		t.seenThisRound = true
		t.stillActive = s.Active
		t.dirty = true
		if t.labels == nil {
			t.labels = s.Labels
		}
		t.annotations = renderAnnotations(s.Rule, t.labels, t.value, t.threshold)
	}
	// 加入本轮 lastSeen
	k := ruleDsKey{rule: s.Rule.ID, ds: s.Datasource.ID}
	set, ok := sm.lastSeen[k]
	if !ok {
		set = make(map[string]struct{}, 4)
		sm.lastSeen[k] = set
	}
	set[fp] = struct{}{}
}

// flush 推进状态机 + 批量写 DB
func (sm *stateManager) flush() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(sm.tracked) == 0 {
		return
	}
	snap := store.MustSnapshot()
	if snap == nil {
		return
	}

	now := time.Now()
	toUpsert := make([]*trackedAlert, 0, 64)
	toResolveIDs := make([]uint, 0, 16)
	historyRows := make([]database.AlertHistory, 0, 64)

	for fp, t := range sm.tracked {
		// 检查规则是否在快照中且仍有数据源（无数据源的规则不会被 evaluator 调度）
		ruleDead := t.ruleID > 0 && !ruleHasDatasource(t.ruleID, snap)

		switch t.state {
		case "pending":
			if ruleDead {
				// 规则已无数据源 → 直接清理陈旧实例
				delete(sm.tracked, fp)
				if t.fingerprint != "" {
					_ = database.DB.Where("fingerprint = ?", t.fingerprint).Delete(&database.AlertInstance{}).Error
				}
				continue
			}
			if t.stillActive {
				forDur := parseFor(t.rule)
				if !t.activeAt.IsZero() && now.Sub(t.activeAt) >= forDur {
					t.state = "firing"
					ts := now
					t.firedAt = &ts
					sm.applyFilters(t, snap)
					sm.applyRouting(t, snap)
					historyRows = append(historyRows, sm.history(t, "firing", now))
					t.dirty = true
				}
			} else if t.seenThisRound {
				// pending 未达到 for 又消失 → 直接清除（不写 history，不算告警）
				delete(sm.tracked, fp)
				if t.fingerprint != "" {
					_ = database.DB.Where("fingerprint = ?", t.fingerprint).Delete(&database.AlertInstance{}).Error
				}
				continue
			}
		case "firing":
			if ruleDead {
				// 规则已无数据源 → 直接解决陈旧实例
				if t.rule == nil || t.ds == nil {
					// 无 rule/ds 指针（DB 恢复的实例），无法 upsert，直接删
					delete(sm.tracked, fp)
					if t.fingerprint != "" {
						_ = database.DB.Where("fingerprint = ?", t.fingerprint).Delete(&database.AlertInstance{}).Error
					}
					continue
				}
				t.state = "resolved"
				ts := now
				t.resolvedAt = &ts
				historyRows = append(historyRows, sm.history(t, "resolved", now))
				t.dirty = true
			} else if t.stillActive {
				// 维持，刷新 annotations
				t.dirty = true
			} else if t.seenThisRound {
				keep := parseKeepFiringFor(t.rule)
				if keep == 0 || now.Sub(t.lastEvalAt) >= keep {
					t.state = "resolved"
					ts := now
					t.resolvedAt = &ts
					historyRows = append(historyRows, sm.history(t, "resolved", now))
					t.dirty = true
				}
			}
		}
		if t.dirty {
			toUpsert = append(toUpsert, t)
		}
		// resolved 状态再保留一轮便于前端看到，下一轮删除
		if t.state == "resolved" && !t.dirty {
			toResolveIDs = append(toResolveIDs, 0) // 仅占位用，真实清理见下文
			delete(sm.tracked, fp)
		}
	}

	// 写 DB
	resolvedGroupKeys := make(map[string]struct{})
	if len(toUpsert) > 0 {
		_ = database.DB.Transaction(func(tx *gorm.DB) error {
			for _, t := range toUpsert {
				upsertInstance(tx, t)
				t.dirty = false
				if t.state == "resolved" && t.groupKey != "" {
					resolvedGroupKeys[t.groupKey] = struct{}{}
				}
			}
			return nil
		})
	}
	// 如果某组的所有实例均已恢复，立即拉近 next_notify_at，
	// 让 dispatchPending 在下一 tick 发送恢复通知，避免等待 repeat_interval。
	for gk := range resolvedGroupKeys {
		var activeCount int64
		database.DB.Model(&database.AlertInstance{}).
			Where("group_key = ? AND state IN ?", gk, []string{"pending", "firing"}).
			Count(&activeCount)
		if activeCount == 0 {
			database.DB.Model(&database.AlertGroup{}).
				Where("group_key = ?", gk).
				Update("next_notify_at", time.Now())
		}
	}
	if len(historyRows) > 0 {
		_ = database.DB.CreateInBatches(historyRows, 100).Error
	}

	// 清空 lastSeen 准备下一轮
	for k := range sm.lastSeen {
		delete(sm.lastSeen, k)
	}
	for fp, t := range sm.tracked {
		k := ruleDsKey{}
		if t.rule != nil {
			k.rule = t.rule.ID
		}
		if t.ds != nil {
			k.ds = t.ds.ID
		}
		if k.rule == 0 && k.ds == 0 {
			continue
		}
		set, ok := sm.lastSeen[k]
		if !ok {
			set = make(map[string]struct{}, 4)
			sm.lastSeen[k] = set
		}
		set[fp] = struct{}{}
		t.seenThisRound = false
	}
}

func upsertInstance(tx *gorm.DB, t *trackedAlert) {
	if t.rule == nil || t.ds == nil {
		return
	}
	row := database.AlertInstance{
		Fingerprint:     t.fingerprint,
		RuleID:          t.rule.ID,
		DatasourceID:    t.ds.ID,
		LabelsJSON:      alerting.EncodeJSON(t.labels),
		AnnotationsJSON: alerting.EncodeJSON(t.annotations),
		State:           t.state,
		Severity:        t.severity,
		Value:           t.value,
		Threshold:       t.threshold,
		ActiveAt:        t.activeAt,
		FiredAt:         t.firedAt,
		ResolvedAt:      t.resolvedAt,
		LastEvalAt:      t.lastEvalAt,
		GroupKey:        t.groupKey,
		SilencedByJSON:  alerting.EncodeUintSlice(t.silencedBy),
		InhibitedByJSON: alerting.EncodeUintSlice(t.inhibitedBy),
	}
	var existing database.AlertInstance
	err := tx.Where("fingerprint = ?", t.fingerprint).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		_ = tx.Create(&row).Error
		return
	}
	if err != nil {
		return
	}
	// 保留 ID/created_at/notified_count
	row.ID = existing.ID
	row.NotifiedCount = existing.NotifiedCount
	row.LastNotifiedAt = existing.LastNotifiedAt
	row.CreatedAt = existing.CreatedAt
	_ = tx.Save(&row).Error
}

// applyFilters 评估 silence/inhibit，写入 t.silencedBy / t.inhibitedBy
func (sm *stateManager) applyFilters(t *trackedAlert, snap *store.RuleSnapshot) {
	labels := mergeForFilter(t)
	silencedBy := []uint{}
	for i := range snap.Silences {
		s := &snap.Silences[i]
		matchers, err := alerting.DecodeMatchers(s.MatchersJSON)
		if err != nil || len(matchers) == 0 {
			continue
		}
		if alerting.MatchAll(matchers, labels) {
			silencedBy = append(silencedBy, s.ID)
		}
	}
	t.silencedBy = silencedBy

	// 抑制：检查是否存在 source 命中的活跃 firing 告警
	inhibitedBy := []uint{}
	for i := range snap.Inhibits {
		inh := &snap.Inhibits[i]
		targetM, _ := alerting.DecodeMatchers(inh.TargetMatchersJSON)
		if len(targetM) == 0 || !alerting.MatchAll(targetM, labels) {
			continue
		}
		sourceM, _ := alerting.DecodeMatchers(inh.SourceMatchersJSON)
		equalLabels := alerting.DecodeStringSlice(inh.EqualLabelsJSON)
		// 在 tracked 中找 firing 状态且 source 命中、且 equalLabels 全部相等的实例
		matched := false
		for _, other := range sm.tracked {
			if other.fingerprint == t.fingerprint || other.state != "firing" {
				continue
			}
			otherLabels := mergeForFilter(other)
			if !alerting.MatchAll(sourceM, otherLabels) {
				continue
			}
			eq := true
			for _, k := range equalLabels {
				if labels[k] != otherLabels[k] {
					eq = false
					break
				}
			}
			if eq {
				matched = true
				break
			}
		}
		if matched {
			inhibitedBy = append(inhibitedBy, inh.ID)
		}
	}
	t.inhibitedBy = inhibitedBy
}

// applyRouting 选路由 + 计算 group_key + 维护 AlertGroup
func (sm *stateManager) applyRouting(t *trackedAlert, snap *store.RuleSnapshot) {
	labels := mergeForFilter(t)
	route := matchRoute(snap.Routes, labels, t.rule.RouteID)
	if route == nil {
		// 无路由：使用根路由（若仍找不到，告警进入 untriaged 状态，但仍记录）
		root := findRoot(snap.Routes)
		if root == nil {
			return
		}
		route = root
	}
	var groupBy []string
	if route.GroupByJSON != "" {
		groupBy = alerting.DecodeStringSlice(route.GroupByJSON)
	}
	if len(groupBy) == 0 {
		groupBy = []string{"alertname"}
	}
	// 注入 alertname / datasource_id 等便利标签
	if labels["alertname"] == "" && t.rule != nil {
		labels["alertname"] = t.rule.Name
	}
	t.groupKey = alerting.GroupKey(route.ID, groupBy, labels)

	// 维护 AlertGroup 行
	groupLabels := alerting.LabelSet{}
	for _, k := range groupBy {
		groupLabels[k] = labels[k]
	}
	now := time.Now()
	var grp database.AlertGroup
	err := database.DB.Where("group_key = ?", t.groupKey).First(&grp).Error
	if err == gorm.ErrRecordNotFound {
		// 首次出现：group_wait 后触发首次通知
		next := now.Add(parseDurationOr(route.GroupWait, sm.d.cfg.DefaultGroupWait))
		grp = database.AlertGroup{
			GroupKey:     t.groupKey,
			RuleID:       t.ruleID,
			DatasourceID: t.dsID,
			RouteID:      route.ID,
			LabelsJSON:   alerting.EncodeJSON(groupLabels),
			AlertCount:   1,
			FirstSeenAt:  now,
			NextNotifyAt: &next,
			State:        "pending",
		}
		_ = database.DB.Create(&grp).Error
		return
	}
	if err != nil {
		return
	}
	// 已有分组：调整 next_notify_at（group_interval 适用于新增告警）
	if grp.State == "idle" {
		// 告警恢复后又重新触发：将 state 重置为 pending，
		// 否则 dispatchPending 的 WHERE state IN ('pending','notified') 会永久跳过该组。
		grp.State = "pending"
		grp.SendCount = 0
	}
	// 只有从未通知过的组才用 group_interval 拉近 next_notify_at；
	// 已通知过的组由 dispatchPending 按 repeat_interval 调度，不应被每次 eval 覆盖。
	if grp.SendCount == 0 {
		groupInterval := parseDurationOr(route.GroupInterval, sm.d.cfg.DefaultGroupInterval)
		if grp.NextNotifyAt == nil || grp.NextNotifyAt.After(now.Add(groupInterval)) {
			next := now.Add(groupInterval)
			grp.NextNotifyAt = &next
		}
	}
	grp.AlertCount++
	_ = database.DB.Save(&grp).Error
}

// history 构造一条历史记录
func (sm *stateManager) history(t *trackedAlert, event string, ts time.Time) database.AlertHistory {
	dsName := ""
	ruleName := ""
	ruleID := uint(0)
	dsID := uint(0)
	if t.rule != nil {
		ruleName = t.rule.Name
		ruleID = t.rule.ID
	}
	if t.ds != nil {
		dsName = t.ds.Name
		dsID = t.ds.ID
	}
	return database.AlertHistory{
		Fingerprint:     t.fingerprint,
		RuleID:          ruleID,
		RuleName:        ruleName,
		DatasourceID:    dsID,
		DatasourceName:  dsName,
		State:           t.state,
		Severity:        t.severity,
		Value:           t.value,
		Threshold:       t.threshold,
		LabelsJSON:      alerting.EncodeJSON(t.labels),
		AnnotationsJSON: alerting.EncodeJSON(t.annotations),
		EventType:       event,
		OccurredAt:      ts,
	}
}

func parseFor(rule *database.AlertRule) time.Duration {
	if rule == nil || rule.ForDuration == "" {
		return 0
	}
	d, err := time.ParseDuration(rule.ForDuration)
	if err != nil || d < 0 {
		return 0
	}
	return d
}

func parseKeepFiringFor(rule *database.AlertRule) time.Duration {
	if rule == nil || rule.KeepFiringFor == "" {
		return 0
	}
	d, err := time.ParseDuration(rule.KeepFiringFor)
	if err != nil || d < 0 {
		return 0
	}
	return d
}

// ruleHasDatasource 检查快照中指定 ID 的规则是否仍有可用的数据源
func ruleHasDatasource(ruleID uint, snap *store.RuleSnapshot) bool {
	var rule *database.AlertRule
	for i := range snap.Rules {
		if snap.Rules[i].ID == ruleID {
			rule = &snap.Rules[i]
			break
		}
	}
	if rule == nil {
		return false // 规则已被删除
	}
	if len(rule.DatasourceIDs) > 0 {
		return true
	}
	sel := alerting.DecodeDatasourceSelector(rule.DatasourceSelectorJSON)
	if sel != nil {
		return true
	}
	return false
}

// mergeForFilter 把规则标签、实例标签、内置标签合并为 matcher 评估的 label 集合
func mergeForFilter(t *trackedAlert) alerting.LabelSet {
	out := alerting.LabelSet{}
	if t.rule != nil {
		var ruleLabels alerting.LabelSet
		_ = json.Unmarshal([]byte(t.rule.LabelsJSON), &ruleLabels)
		for k, v := range ruleLabels {
			out[k] = v
		}
		out["alertname"] = t.rule.Name
		out["severity"] = t.severity
	}
	if t.ds != nil {
		out["datasource_id"] = uintToStr(t.ds.ID)
		out["datasource_name"] = t.ds.Name
		if t.ds.ProjectName != "" {
			out["project"] = t.ds.ProjectName
		}
	}
	for k, v := range t.labels {
		out[k] = v
	}
	return out
}

func uintToStr(v uint) string {
	// 替代 strconv，避免额外 import；几千数据源场景下 ID 都不大
	if v == 0 {
		return "0"
	}
	digits := make([]byte, 0, 10)
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// renderAnnotations 用规则 annotations 模板渲染（暂以原样输出 + 注入基础字段）
func renderAnnotations(rule *database.AlertRule, labels alerting.LabelSet, value, threshold float64) alerting.LabelSet {
	out := alerting.LabelSet{}
	if rule == nil {
		return out
	}
	raw := strings.TrimSpace(rule.AnnotationsJSON)
	// 模板替换辅助函数
	subst := func(v string) string {
		v = strings.ReplaceAll(v, "{{ $value }}", floatStr(value))
		v = strings.ReplaceAll(v, "{{ $threshold }}", floatStr(threshold))
		for lk, lv := range labels {
			v = strings.ReplaceAll(v, "{{ ."+lk+" }}", lv)
		}
		return v
	}
	// 优先按 JSON 解析；解析失败则把全部内容当作 description 纯文本
	if raw != "" {
		var ann alerting.LabelSet
		if err := json.Unmarshal([]byte(raw), &ann); err == nil && len(ann) > 0 {
			for k, v := range ann {
				out[k] = subst(v)
			}
		} else {
			out["description"] = subst(raw)
		}
	}
	if out["summary"] == "" {
		if out["description"] != "" {
			out["summary"] = out["description"]
		} else {
			out["summary"] = rule.Description
		}
	}
	return out
}

func floatStr(v float64) string {
	// 简易格式化：保留 2 位小数
	// 不引入 strconv 也行，但 strconv 更稳
	return strconv.FormatFloat(v, 'f', -1, 64)
}
