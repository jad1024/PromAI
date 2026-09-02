package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/database"
	"PromAI/pkg/portscan"
)

// ===== 敏感端口扫描（手动触发，不接通知链路，独立报告） =====

// handlePortScanCreate POST /api/promai/portscan
// 创建扫描任务并异步执行。body: {"targets":"1.2.3.4\n5.6.7.8", "ports":[22,3306]} 或 {"ports_text":"22,3306"}
func (a *AdminAPI) handlePortScanCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "不支持的请求方法")
		return
	}
	var req struct {
		Targets   string `json:"targets"`    // 批量目标，一行一个（支持逗号/空格分隔、CIDR）
		PortsText string `json:"ports_text"` // 端口文本（逗号分隔/范围），可选
		Ports     []int  `json:"ports"`      // 端口列表，可选
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "请求体格式错误")
		return
	}

	targets, err := portscan.ParseTargets(req.Targets, 4096)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	var ports []int
	switch {
	case len(req.Ports) > 0:
		ports = req.Ports
	case strings.TrimSpace(req.PortsText) != "":
		if ports, err = portscan.ParsePorts(req.PortsText); err != nil {
			writeError(w, 400, err.Error())
			return
		}
	default:
		for _, p := range portscan.DefaultSensitivePorts {
			ports = append(ports, p.Port)
		}
	}
	if len(ports) == 0 {
		writeError(w, 400, "端口列表为空")
		return
	}

	taskID := newTaskID()
	targetsJSON, _ := json.Marshal(targets)
	portsJSON, _ := json.Marshal(ports)
	task := database.PortScanTask{
		TaskID:       taskID,
		TargetsJSON:  string(targetsJSON),
		PortsJSON:    string(portsJSON),
		Status:       "running",
		TotalTargets: len(targets),
		TotalPorts:   len(ports),
		StartedAt:    time.Now(),
	}
	if err := database.DB.Create(&task).Error; err != nil {
		writeError(w, 500, "创建扫描任务失败: "+err.Error())
		return
	}

	log.Printf("[PortScan] 创建扫描任务 task_id=%s targets=%d ports=%d", taskID, len(targets), len(ports))
	writeJSON(w, map[string]interface{}{
		"success":       true,
		"task_id":       taskID,
		"total_targets": len(targets),
		"total_ports":   len(ports),
		"message":       "扫描任务已创建，正在执行...",
	})

	go a.runPortScan(taskID, targets, ports)
}

// portScanTimeout 从系统设置读取单连接探测超时（秒），默认 2s。
func portScanTimeout() time.Duration {
	v := strings.TrimSpace(database.GetAppSetting("portscan_timeout_seconds"))
	if v == "" {
		return 2 * time.Second
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 60 {
		return time.Duration(n) * time.Second
	}
	return 2 * time.Second
}

// portScanConcurrency 从系统设置读取并发连接数，默认 100。
func portScanConcurrency() int {
	v := strings.TrimSpace(database.GetAppSetting("portscan_concurrency"))
	if v == "" {
		return 100
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
		return n
	}
	return 100
}

// runPortScan 执行扫描并落库结果。
func (a *AdminAPI) runPortScan(taskID string, targets []string, ports []int) {
	opts := portscan.Options{Timeout: portScanTimeout(), Concurrency: portScanConcurrency()}
	log.Printf("[PortScan] 扫描参数 task_id=%s timeout=%v concurrency=%d", taskID, opts.Timeout, opts.Concurrency)
	results := portscan.Scan(targets, ports, opts)

	openCount := 0
	for _, res := range results {
		if res.Open {
			openCount++
		}
		row := database.PortScanResult{
			TaskID:    taskID,
			IP:        res.IP,
			Port:      res.Port,
			PortName:  res.PortName,
			State:     res.State,
			Risk:      res.Risk,
			LatencyMs: res.LatencyMs,
		}
		if err := database.DB.Create(&row).Error; err != nil {
			log.Printf("[PortScan] 写入结果失败: %v", err)
		}
	}

	now := time.Now()
	updates := map[string]interface{}{
		"status":       "completed",
		"open_ports":   openCount,
		"message":      fmt.Sprintf("扫描完成：%d 个目标 × %d 端口，开放 %d 个敏感端口", len(targets), len(ports), openCount),
		"completed_at": &now,
	}
	if err := database.DB.Model(&database.PortScanTask{}).Where("task_id = ?", taskID).Updates(updates).Error; err != nil {
		log.Printf("[PortScan] 更新任务状态失败: %v", err)
	}
}

// handlePortScanTasks GET /api/promai/portscan/tasks
func (a *AdminAPI) handlePortScanTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "不支持的请求方法")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	var total int64
	database.DB.Model(&database.PortScanTask{}).Count(&total)
	var tasks []database.PortScanTask
	database.DB.Order("id desc").Limit(pageSize).Offset((page - 1) * pageSize).Find(&tasks)

	out := make([]map[string]interface{}, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, portScanTaskToMap(t))
	}
	writeJSON(w, map[string]interface{}{"items": out, "total": total, "page": page, "page_size": pageSize})
}

// handlePortScanTaskByID GET/DELETE /api/promai/portscan/tasks/{id}[/results|/export]
func (a *AdminAPI) handlePortScanTaskByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/promai/portscan/tasks/")
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) == 0 || segs[0] == "" {
		writeError(w, 400, "缺少任务 ID")
		return
	}
	id, err := strconv.ParseUint(segs[0], 10, 64)
	if err != nil {
		writeError(w, 400, "任务 ID 无效")
		return
	}

	// 子路径：results / export
	sub := ""
	if len(segs) > 1 {
		sub = segs[1]
	}

	var task database.PortScanTask
	if err := database.DB.First(&task, "id = ?", id).Error; err != nil {
		writeError(w, 404, "任务不存在")
		return
	}

	switch {
	case sub == "results" && r.Method == "GET":
		var results []database.PortScanResult
		database.DB.Where("task_id = ?", task.TaskID).Order("ip asc, port asc").Find(&results)
		writeJSON(w, map[string]interface{}{"items": results, "total": len(results)})
		return
	case sub == "export" && r.Method == "GET":
		a.exportPortScanReport(w, &task)
		return
	case sub == "" && r.Method == "GET":
		writeJSON(w, portScanTaskToMap(task))
		return
	case sub == "" && r.Method == "DELETE":
		database.DB.Where("task_id = ?", task.TaskID).Delete(&database.PortScanResult{})
		database.DB.Delete(&task)
		writeJSON(w, map[string]interface{}{"success": true})
		return
	default:
		writeError(w, 405, "不支持的请求方法或路径")
		return
	}
}

// handlePortScanPorts GET /api/promai/portscan/ports —— 返回内置敏感端口列表。
func (a *AdminAPI) handlePortScanPorts(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "不支持的请求方法")
		return
	}
	writeJSON(w, map[string]interface{}{"items": portscan.DefaultSensitivePorts})
}

// portScanTaskToMap 把任务转成前端友好结构（展开 targets/ports）。
func portScanTaskToMap(t database.PortScanTask) map[string]interface{} {
	var targets []string
	var ports []int
	_ = json.Unmarshal([]byte(t.TargetsJSON), &targets)
	_ = json.Unmarshal([]byte(t.PortsJSON), &ports)
	return map[string]interface{}{
		"id":            t.ID,
		"task_id":       t.TaskID,
		"targets":       targets,
		"ports":         ports,
		"status":        t.Status,
		"total_targets": t.TotalTargets,
		"total_ports":   t.TotalPorts,
		"open_ports":    t.OpenPorts,
		"message":       t.Message,
		"error":         t.Error,
		"started_at":    t.StartedAt,
		"completed_at":  t.CompletedAt,
		"created_at":    t.CreatedAt,
	}
}

// exportPortScanReport 生成独立 HTML 报告并返回下载。
func (a *AdminAPI) exportPortScanReport(w http.ResponseWriter, task *database.PortScanTask) {
	var results []database.PortScanResult
	database.DB.Where("task_id = ?", task.TaskID).Order("ip asc, port asc").Find(&results)

	// 按 IP 分组
	type ipGroup struct {
		IP    string
		Rows  []database.PortScanResult
		Open  int
		Risk  string
	}
	order := []string{}
	groups := map[string]*ipGroup{}
	for _, r := range results {
		g, ok := groups[r.IP]
		if !ok {
			g = &ipGroup{IP: r.IP}
			groups[r.IP] = g
			order = append(order, r.IP)
		}
		g.Rows = append(g.Rows, r)
		if r.State == "open" {
			g.Open++
			if r.Risk == "high" && g.Risk != "high" {
				g.Risk = "high"
			} else if r.Risk == "medium" && g.Risk == "" {
				g.Risk = "medium"
			}
		}
	}
	if len(groups) == 0 {
		// 极端情况：任务无结果，回退到 targets
		var targets []string
		_ = json.Unmarshal([]byte(task.TargetsJSON), &targets)
		for _, ip := range targets {
			groups[ip] = &ipGroup{IP: ip}
			order = append(order, ip)
		}
	}

	var ports []int
	_ = json.Unmarshal([]byte(task.PortsJSON), &ports)

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8"><title>敏感端口检测报告</title>`)
	sb.WriteString(`<style>
body{font-family:-apple-system,"Segoe UI","Microsoft YaHei",sans-serif;margin:0;background:#f5f7fa;color:#1f2d3d}
.container{max-width:1100px;margin:0 auto;padding:24px}
h1{font-size:22px;margin:0 0 4px}
.sub{color:#8492a6;font-size:13px;margin-bottom:20px}
.cards{display:flex;gap:16px;margin-bottom:24px;flex-wrap:wrap}
.card{flex:1;min-width:160px;background:#fff;border-radius:8px;padding:16px 20px;box-shadow:0 1px 3px rgba(0,0,0,.06)}
.card .k{font-size:12px;color:#8492a6}
.card .v{font-size:28px;font-weight:700;margin-top:4px}
.card .v.high{color:#f56c6c}.card .v.warn{color:#e6a23c}.card .v.ok{color:#67c23a}
table{width:100%;border-collapse:collapse;background:#fff;border-radius:8px;overflow:hidden;box-shadow:0 1px 3px rgba(0,0,0,.06)}
th,td{padding:10px 14px;text-align:left;font-size:13px;border-bottom:1px solid #ebeef5}
th{background:#fafbfc;color:#5e6d82;font-weight:600}
.tag{display:inline-block;padding:2px 8px;border-radius:4px;font-size:12px}
.tag.high{background:#fef0f0;color:#f56c6c}.tag.medium{background:#fdf6ec;color:#e6a23c}.tag.low{background:#f4f4f5;color:#909399}
.tag.open{background:#f0f9eb;color:#67c23a}.tag.closed{background:#f4f4f5;color:#c0c4cc}
.group{margin-bottom:20px}
.group-title{font-size:15px;font-weight:600;margin-bottom:8px}
.group-title .badge{margin-left:8px;font-size:12px;font-weight:400;color:#8492a6}
.footer{margin-top:24px;font-size:12px;color:#c0c4cc;text-align:center}
</style></head><body><div class="container">`)
	sb.WriteString(fmt.Sprintf(`<h1>敏感端口检测报告</h1>`))
	sb.WriteString(fmt.Sprintf(`<div class="sub">任务 %s · 扫描时间 %s · 目标 %d 个 · 端口 %d 个</div>`,
		task.TaskID, task.StartedAt.Format("2006-01-02 15:04:05"), task.TotalTargets, task.TotalPorts))
	sb.WriteString(`<div class="cards">`)
	sb.WriteString(fmt.Sprintf(`<div class="card"><div class="k">扫描目标</div><div class="v">%d</div></div>`, task.TotalTargets))
	sb.WriteString(fmt.Sprintf(`<div class="card"><div class="k">检测端口</div><div class="v">%d</div></div>`, task.TotalPorts))
	riskClass := "ok"
	if task.OpenPorts > 0 {
		riskClass = "high"
	}
	sb.WriteString(fmt.Sprintf(`<div class="card"><div class="k">开放敏感端口</div><div class="v %s">%d</div></div>`, riskClass, task.OpenPorts))
	sb.WriteString(`</div>`)

	for _, ip := range order {
		g := groups[ip]
		sb.WriteString(`<div class="group">`)
		sb.WriteString(fmt.Sprintf(`<div class="group-title">%s<span class="badge">开放 %d 个敏感端口</span></div>`, ip, g.Open))
		sb.WriteString(`<table><tr><th>端口</th><th>服务</th><th>状态</th><th>风险</th><th>延迟</th></tr>`)
		for _, r := range g.Rows {
			stateTag := "closed"
			stateText := "关闭"
			if r.State == "open" {
				stateTag = "open"
				stateText = "开放"
			} else if r.State == "timeout" {
				stateText = "超时"
			} else if r.State == "refused" {
				stateText = "拒绝"
			}
			latency := "-"
			if r.State == "open" {
				latency = fmt.Sprintf("%dms", r.LatencyMs)
			}
			sb.WriteString(fmt.Sprintf(`<tr><td>%d</td><td>%s</td><td><span class="tag %s">%s</span></td><td><span class="tag %s">%s</span></td><td>%s</td></tr>`,
				r.Port, r.PortName, stateTag, stateText, r.Risk, r.Risk, latency))
		}
		sb.WriteString(`</table></div>`)
	}
	sb.WriteString(fmt.Sprintf(`<div class="footer">PromAI 敏感端口检测 · 生成于 %s</div>`, time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString(`</div></body></html>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=portscan-%s.html", task.TaskID))
	w.Write([]byte(sb.String()))
}
