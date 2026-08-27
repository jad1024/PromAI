package report

import (
	"archive/zip"
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// escapeXML 转义 XML 特殊字符，防止指标名/标签中的 & < > 破坏 docx 文档结构
func escapeXML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

// docxRun 生成一个 Word run（可选加粗/字号）
func docxRun(text string, bold bool, size int) string {
	rpr := ""
	if bold || size > 0 {
		var sb strings.Builder
		sb.WriteString("<w:rPr>")
		if bold {
			sb.WriteString("<w:b/>")
		}
		if size > 0 {
			sb.WriteString(fmt.Sprintf("<w:sz w:val=\"%d\"/><w:szCs w:val=\"%d\"/>", size, size))
		}
		sb.WriteString("</w:rPr>")
		rpr = sb.String()
	}
	return "<w:r>" + rpr + "<w:t xml:space=\"preserve\">" + escapeXML(text) + "</w:t></w:r>"
}

// docxPara 生成一个段落
func docxPara(text string, bold bool, size int, center bool) string {
	var sb strings.Builder
	sb.WriteString("<w:p>")
	if center {
		sb.WriteString("<w:pPr><w:jc w:val=\"center\"/></w:pPr>")
	}
	sb.WriteString(docxRun(text, bold, size))
	sb.WriteString("</w:p>")
	return sb.String()
}

// docxTable 生成一个带边框的表格
func docxTable(headers []string, rows [][]string) string {
	var sb strings.Builder
	sb.WriteString("<w:tbl><w:tblPr><w:tblW w:w=\"0\" w:type=\"auto\"/><w:tblBorders>")
	for _, b := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		sb.WriteString(fmt.Sprintf("<w:%s w:val=\"single\" w:sz=\"4\" w:space=\"0\" w:color=\"999999\"/>", b))
	}
	sb.WriteString("</w:tblBorders></w:tblPr>")
	// 表头行
	sb.WriteString("<w:tr>")
	for _, h := range headers {
		sb.WriteString("<w:tc><w:tcPr><w:tcW w:w=\"2000\" w:type=\"dxa\"/><w:shd w:val=\"clear\" w:color=\"auto\" w:fill=\"EEF1F6\"/></w:tcPr><w:p><w:pPr><w:jc w:val=\"center\"/></w:pPr>" + docxRun(h, true, 0) + "</w:p></w:tc>")
	}
	sb.WriteString("</w:tr>")
	for _, row := range rows {
		sb.WriteString("<w:tr>")
		for _, c := range row {
			sb.WriteString("<w:tc><w:tcPr><w:tcW w:w=\"2000\" w:type=\"dxa\"/></w:tcPr><w:p>" + docxRun(c, false, 0) + "</w:p></w:tc>")
		}
		sb.WriteString("</w:tr>")
	}
	sb.WriteString("</w:tbl>")
	return sb.String()
}

// GenerateDocx 将巡检报告数据导出为 Word (.docx) 文档，保存到与 HTML 报告同目录同名位置。
// 使用标准库 archive/zip 手写最小合法 docx（document.xml + 包关系文件），无第三方依赖、支持中文。
// 返回 docx 文件路径；生成失败不影响 HTML 报告主流程。
func GenerateDocx(htmlPath string, data ReportData) (string, error) {
	docxPath := strings.TrimSuffix(htmlPath, filepath.Ext(htmlPath)) + ".docx"

	ts := data.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	var body strings.Builder

	// 标题
	title := "巡检报告"
	if data.Project != "" {
		title = "巡检报告 · " + data.Project
	}
	body.WriteString(docxPara(title, true, 36, true))
	sub := ""
	if data.Datasource != "" {
		sub += "数据源：" + data.Datasource + "    "
	}
	sub += "生成时间：" + ts.Format("2006-01-02 15:04:05")
	body.WriteString(docxPara(sub, false, 22, true))
	body.WriteString(docxPara("", false, 0, false))

	// 总体统计
	totalMetrics, totalAlerts, critical, warning, normal := 0, 0, 0, 0, 0
	groupOrder := make([]string, 0, len(data.MetricGroups))
	for name, g := range data.MetricGroups {
		groupOrder = append(groupOrder, name)
		totalMetrics += g.Stats.TotalCount
		totalAlerts += g.Stats.AlertCount
		critical += g.Stats.CriticalCount
		warning += g.Stats.WarningCount
	}
	normal = totalMetrics - totalAlerts
	sort.Strings(groupOrder)

	body.WriteString(docxPara("总体统计", true, 28, false))
	body.WriteString(docxPara(fmt.Sprintf("总指标数：%d    异常：%d（严重 %d，警告 %d）    正常：%d",
		totalMetrics, totalAlerts, critical, warning, normal), false, 0, false))
	body.WriteString(docxPara("", false, 0, false))

	// 分类明细
	for _, gname := range groupOrder {
		g := data.MetricGroups[gname]
		body.WriteString(docxPara("【"+gname+"】", true, 26, false))
		body.WriteString(docxPara(fmt.Sprintf("总指标 %d，异常 %d（严重 %d，警告 %d），正常 %d",
			g.Stats.TotalCount, g.Stats.AlertCount, g.Stats.CriticalCount, g.Stats.WarningCount,
			g.Stats.TotalCount-g.Stats.AlertCount), false, 0, false))

		if len(g.MetricOrder) == 0 {
			body.WriteString(docxPara("", false, 0, false))
			continue
		}
		rows := make([][]string, 0, 16)
		for _, mname := range g.MetricOrder {
			metrics := g.MetricsByName[mname]
			if len(metrics) == 0 {
				continue
			}
			first := metrics[0]
			labels := ""
			if len(first.Labels) > 0 {
				parts := make([]string, 0, len(first.Labels))
				for _, l := range first.Labels {
					parts = append(parts, l.Name+"="+l.Value)
				}
				labels = strings.Join(parts, ", ")
			}
			unit := first.Unit
			val := fmt.Sprintf("%.2f%s", first.Value, unit)
			thr := ""
			if first.Threshold > 0 {
				thr = fmt.Sprintf("%.2f%s", first.Threshold, unit)
			}
			rows = append(rows, []string{mname, labels, val, thr, GetStatusText(first.Status)})
		}
		if len(rows) > 0 {
			body.WriteString(docxTable([]string{"指标", "实例/标签", "当前值", "阈值", "状态"}, rows))
		}
		body.WriteString(docxPara("", false, 0, false))
	}

	body.WriteString(docxPara("由 PromAI 自动生成", false, 0, true))

	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		body.String() + `<w:sectPr/></w:body></w:document>`

	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`</Types>`

	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
		`</Relationships>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"[Content_Types].xml": contentTypes,
		"_rels/.rels":         rels,
		"word/document.xml":   document,
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			return "", fmt.Errorf("创建 %s 失败: %w", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return "", fmt.Errorf("写入 %s 失败: %w", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return "", fmt.Errorf("关闭 zip 失败: %w", err)
	}
	if err := os.WriteFile(docxPath, buf.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("写入 docx 文件失败: %w", err)
	}
	log.Printf("Word 报告生成成功: %s", docxPath)
	return docxPath, nil
}
