package lts

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// =============================================================================
// Java 应用日志降噪漏斗（LTS → AI prompt 前）
//
// L1 查询侧限死（时间窗/limit/keywords 级别过滤）→ L2 多行合并 → L3 变量/异常/堆栈折叠
// → L4 模式摘要 + 每模式 1 条采样。原始日志上万行，折叠后仅 2k-4k token 进 AI。
// =============================================================================

// FoldedPattern 折叠后的日志模式（模板 + 统计 + 采样原文）。
type FoldedPattern struct {
	Signature string // 折叠后的模式签名（变量归一 + 堆栈折叠）
	Count     int    // 该模式出现次数
	FirstAt   string // 首次出现时间
	LastAt    string // 末次出现时间
	Level     string // 日志级别（ERROR/WARN/...）
	Logger    string // logger 名（如 com.xxx.OrderService）
	TraceID   string // 关联链路 ID（MDC traceId/requestId）
	Sample    string // 1 条完整采样原文（不截断，含完整堆栈，用于留档）
}

// LoggerStat logger 维度统计（ERROR 集中的 logger 本身是定位信号）。
type LoggerStat struct {
	Logger string
	Count  int
}

// FoldResult Java 日志折叠结果。
type FoldResult struct {
	TotalLines  int            // 原始日志行数
	MergedLines int            // 多行合并后的逻辑行数
	Patterns    []FoldedPattern // 按出现次数降序的模式列表
	LoggerStats []LoggerStat    // logger 统计（按次数降序）
	TraceIDs    []string        // 去重后的链路 ID 列表
}

var (
	// 时间戳开头（logback/log4j 常见格式），用于多行合并判定
	tsStartRe = regexp.MustCompile(`^\d{4}[-/]\d{2}[-/]\d{2}[ T]\d{2}:\d{2}:\d{2}`)
	// 日志级别
	levelRe = regexp.MustCompile(`\b(ERROR|FATAL|WARN|WARNING|INFO|DEBUG|TRACE)\b`)
	// logger 名（至少三段点分，如 com.xxx.OrderService）
	loggerRe = regexp.MustCompile(`\b[a-zA-Z_][\w]*(?:\.[a-zA-Z_][\w]*){2,}\b`)
	// traceId / requestId
	traceRe = regexp.MustCompile(`(?i)(?:trace_?id|request_?id|span_?id)\s*[=:]\s*([A-Za-z0-9\-_]{6,64})`)
	// 异常类型
	exceptionRe = regexp.MustCompile(`\b([\w$.]+(?:Exception|Error))\b`)
	// Caused by 链
	causedByRe = regexp.MustCompile(`(?m)Caused by:\s*([\w$.]+(?:Exception|Error))`)
	// 堆栈帧
	stackFrameRe = regexp.MustCompile(`(?m)^\s*at\s+([\w$.]+)\(([^)]*)\)`)
)

// 变量归一正则（顺序敏感：先归一更具体的模式，日期时间需最先处理）
var normRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`\b\d{4}[-/]\d{2}[-/]\d{2}[ T]\d{2}:\d{2}:\d{2}(?:[.,]\d{1,9})?\b`), "<ts>"},
	{regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}(?:[.,]\d{1,9})?\b`), "<ts>"},
	{regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`), "<uuid>"},
	{regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`), "<ip>"},
	{regexp.MustCompile(`\b\d{19}\b`), "<ts>"},
	{regexp.MustCompile(`\b\d{13}\b`), "<ts>"},
	{regexp.MustCompile(`\b0x[0-9a-fA-F]{6,}\b`), "<hex>"},
	{regexp.MustCompile(`\b\d+ms\b`), "<n>ms"},
	{regexp.MustCompile(`\bORA-\d{5}\b`), "ORA-*"},
	{regexp.MustCompile(`\b\d{4,}\b`), "<n>"},
}

// frameworkPrefixes 框架包前缀，堆栈折叠时折叠为 <framework> x N。
// 应用自身包（com.<公司>.* 等）不在列表内，会被保留前 3 帧作为定位信号。
var frameworkPrefixes = []string{
	"java.", "javax.", "sun.", "com.sun.", "jdk.",
	"org.springframework.", "org.apache.", "org.mybatis.", "org.hibernate.",
	"io.netty.", "reactor.", "com.google.", "com.fasterxml.",
	"org.jboss.", "org.eclipse.", "io.grpc.", "org.redisson.",
	"com.alibaba.", "com.zaxxer.", "org.aspectj.",
}

func isFrameworkClass(cls string) bool {
	for _, p := range frameworkPrefixes {
		if strings.HasPrefix(cls, p) {
			return true
		}
	}
	return false
}

// FoldJavaLogs 对 LTS 拉回的原始日志行做降噪折叠。
// levelFilter 为空表示不过滤；否则按逗号分隔的级别（如 ERROR,FATAL）过滤逻辑行。
func FoldJavaLogs(lines []string, levelFilter string) *FoldResult {
	fr := &FoldResult{TotalLines: len(lines)}

	levels := map[string]bool{}
	for _, lv := range strings.Split(levelFilter, ",") {
		if lv = strings.ToUpper(strings.TrimSpace(lv)); lv != "" {
			levels[lv] = true
		}
	}

	// L2 多行合并：非时间戳开头的行归并到上一条（堆栈帧不再是独立行）
	var merged []string
	for _, line := range lines {
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		if tsStartRe.MatchString(line) {
			merged = append(merged, line)
		} else if len(merged) > 0 {
			merged[len(merged)-1] += "\n" + line
		} else {
			// 首行不是时间戳开头（截断的日志），当作独立逻辑行
			merged = append(merged, line)
		}
	}
	fr.MergedLines = len(merged)

	// L3 折叠 + 分组
	type group struct {
		pattern FoldedPattern
		order   int
	}
	groups := map[string]*group{}
	var order []string

	for _, entry := range merged {
		level := detectLevel(entry)
		if len(levels) > 0 && level != "" {
			lv := strings.ToUpper(level)
			if lv == "WARNING" {
				lv = "WARN"
			}
			if !levels[lv] {
				continue
			}
		}
		normalized := normalizeVariables(entry)
		logger := detectLogger(entry)
		traceID := detectTraceID(entry)
		signature := foldStackTrace(normalized)

		g, ok := groups[signature]
		if !ok {
			g = &group{order: len(order)}
			g.pattern = FoldedPattern{
				Signature: signature,
				Level:     level,
				Logger:    logger,
				TraceID:   traceID,
				Sample:    entry,
				FirstAt:   extractTimestamp(entry),
				LastAt:    extractTimestamp(entry),
			}
			groups[signature] = g
			order = append(order, signature)
		} else {
			if g.pattern.Level == "" {
				g.pattern.Level = level
			}
			if g.pattern.Logger == "" {
				g.pattern.Logger = logger
			}
			if g.pattern.TraceID == "" {
				g.pattern.TraceID = traceID
			}
			g.pattern.LastAt = extractTimestamp(entry)
		}
		g.pattern.Count++
	}

	// L4 模式排序（次数降序） + logger 统计 + traceId 去重
	fr.Patterns = make([]FoldedPattern, 0, len(order))
	loggerCount := map[string]int{}
	traceSeen := map[string]bool{}
	for _, sig := range order {
		g := groups[sig]
		fr.Patterns = append(fr.Patterns, g.pattern)
		if g.pattern.Logger != "" {
			loggerCount[g.pattern.Logger] += g.pattern.Count
		}
		if g.pattern.TraceID != "" && !traceSeen[g.pattern.TraceID] {
			traceSeen[g.pattern.TraceID] = true
			fr.TraceIDs = append(fr.TraceIDs, g.pattern.TraceID)
		}
	}
	sort.SliceStable(fr.Patterns, func(i, j int) bool {
		return fr.Patterns[i].Count > fr.Patterns[j].Count
	})

	for lg, c := range loggerCount {
		fr.LoggerStats = append(fr.LoggerStats, LoggerStat{Logger: lg, Count: c})
	}
	sort.SliceStable(fr.LoggerStats, func(i, j int) bool {
		return fr.LoggerStats[i].Count > fr.LoggerStats[j].Count
	})
	if len(fr.LoggerStats) > 10 {
		fr.LoggerStats = fr.LoggerStats[:10]
	}

	return fr
}

// detectLevel 提取日志级别。
func detectLevel(s string) string {
	if m := levelRe.FindStringSubmatch(s); len(m) > 1 {
		return m[1]
	}
	return ""
}

// detectLogger 提取 logger 名（取第一个三段以上点分标识符）。
func detectLogger(s string) string {
	if m := loggerRe.FindStringSubmatch(s); len(m) > 0 {
		return m[0]
	}
	return ""
}

// detectTraceID 提取 MDC 链路 ID。
func detectTraceID(s string) string {
	if m := traceRe.FindStringSubmatch(s); len(m) > 1 {
		return m[1]
	}
	return ""
}

// extractTimestamp 提取日志行的时间戳（用于统计首次/末次出现）。
func extractTimestamp(s string) string {
	loc := tsStartRe.FindStringIndex(s)
	if loc == nil {
		return ""
	}
	ts := s[loc[0]:loc[1]]
	// 兼容 "2024-08-29T14:55:08" 与 "2024-08-29 14:55:08"
	ts = strings.Replace(ts, "T", " ", 1)
	ts = strings.Replace(ts, "/", "-", 2)
	return ts
}

// normalizeVariables 变量归一：IP / UUID / 时间戳 / 长数字 / 请求耗时 → 占位符。
func normalizeVariables(s string) string {
	for _, r := range normRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// foldStackTrace 堆栈折叠：保留异常类型 + Caused by 链 + 应用包名前 3 帧，框架帧折叠。
// 单条 100 行堆栈 ≈ 2k token → 折叠后 ≈ 50 token。
func foldStackTrace(entry string) string {
	// 非堆栈日志：直接返回归一化结果
	if !strings.Contains(entry, "Exception") && !strings.Contains(entry, "Error") && !strings.Contains(entry, "\tat ") {
		return entry
	}

	var b strings.Builder

	// 异常类型（取消息体主异常）
	mainEx := ""
	if m := exceptionRe.FindStringSubmatch(entry); len(m) > 1 {
		mainEx = m[1]
		b.WriteString(mainEx)
	}

	// Caused by 链
	causes := causedByRe.FindAllStringSubmatch(entry, -1)
	for _, c := range causes {
		if len(c) > 1 && c[1] != mainEx {
			b.WriteString(" <== " + c[1])
		}
	}

	// 堆栈帧：应用帧保留前 3，框架帧折叠计数
	appFrames := 0
	frameworkCount := 0
	for _, m := range stackFrameRe.FindAllStringSubmatch(entry, -1) {
		if len(m) < 2 {
			continue
		}
		cls := m[1]
		if isFrameworkClass(cls) {
			frameworkCount++
			continue
		}
		if appFrames < 3 {
			b.WriteString(" | at " + cls)
			appFrames++
		}
	}
	if frameworkCount > 0 {
		b.WriteString(" | <framework> x " + strconv.Itoa(frameworkCount))
	}

	if b.Len() == 0 {
		// 兜底：提取不到结构化堆栈，退回归一化首行
		firstLine := entry
		if idx := strings.Index(entry, "\n"); idx >= 0 {
			firstLine = entry[:idx]
		}
		return firstLine
	}
	return b.String()
}

// RenderSummary 将折叠结果渲染为紧凑的 Markdown 摘要（进 AI prompt 用）。
func RenderSummary(fr *FoldResult) string {
	if fr == nil || len(fr.Patterns) == 0 {
		return "（LTS 检索未返回 ERROR/FATAL 日志，或日志为空）"
	}
	var b strings.Builder
	b.WriteString("检索到日志：")
	b.WriteString(strconv.Itoa(fr.TotalLines))
	b.WriteString(" 行（多行合并后 ")
	b.WriteString(strconv.Itoa(fr.MergedLines))
	b.WriteString(" 条逻辑日志），折叠为 ")
	b.WriteString(strconv.Itoa(len(fr.Patterns)))
	b.WriteString(" 个模式：\n")

	for i, p := range fr.Patterns {
		if i >= 12 { // 最多展示 12 个模式，其余统计概览
			b.WriteString("（其余 ")
			b.WriteString(strconv.Itoa(len(fr.Patterns) - 12))
			b.WriteString(" 个模式省略）\n")
			break
		}
		b.WriteString("- [")
		if p.Level != "" {
			b.WriteString(p.Level)
		} else {
			b.WriteString("?")
		}
		b.WriteString("] x")
		b.WriteString(strconv.Itoa(p.Count))
		b.WriteString("  ")
		b.WriteString(p.Signature)
		if p.Logger != "" {
			b.WriteString("  @")
			b.WriteString(p.Logger)
		}
		if p.FirstAt != "" {
			b.WriteString("  (")
			b.WriteString(p.FirstAt)
			if p.LastAt != "" && p.LastAt != p.FirstAt {
				b.WriteString("~")
				b.WriteString(p.LastAt)
			}
			b.WriteString(")")
		}
		b.WriteString("\n")
	}

	if len(fr.LoggerStats) > 0 {
		b.WriteString("ERROR 集中 logger: ")
		for i, ls := range fr.LoggerStats {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(ls.Logger + "(" + strconv.Itoa(ls.Count) + ")")
		}
		b.WriteString("\n")
	}
	if len(fr.TraceIDs) > 0 {
		b.WriteString("关联 traceId: ")
		b.WriteString(strings.Join(fr.TraceIDs, ", "))
		b.WriteString("\n")
	}
	return b.String()
}
