package report

import (
	"archive/zip"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGenerateDocx 验证 Word 导出：生成的 .docx 为合法 zip，包含标准部件与关键内容
func TestGenerateDocx(t *testing.T) {
	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "inspection_report_test.html")
	data := ReportData{
		Timestamp:  time.Now(),
		Project:    "测试项目",
		Datasource: "prometheus-test",
		MetricGroups: map[string]*MetricGroup{
			"CPU": {
				MetricsByName: map[string][]MetricData{
					"cpu_usage": {
						{
							Name: "cpu_usage", Instance: "node-1", Value: 85.5,
							Threshold: 80, Unit: "%", Status: "critical",
							Labels: []LabelData{{Name: "instance", Value: "192.168.1.1:9100"}},
						},
					},
				},
				MetricOrder: []string{"cpu_usage"},
				Stats:       GroupStats{TotalCount: 1, AlertCount: 1, CriticalCount: 1, MaxValue: 85.5, MinValue: 85.5},
			},
		},
	}

	path, err := GenerateDocx(htmlPath, data)
	if err != nil {
		t.Fatalf("GenerateDocx: %v", err)
	}
	if !strings.HasSuffix(path, ".docx") {
		t.Fatalf("unexpected output path: %s", path)
	}

	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("docx 不是合法 zip: %v", err)
	}
	defer zr.Close()

	names := make(map[string]bool, len(zr.File))
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, n := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
		if !names[n] {
			t.Fatalf("docx 缺少部件 %s", n)
		}
	}

	f, err := zr.Open("word/document.xml")
	if err != nil {
		t.Fatalf("打开 document.xml: %v", err)
	}
	defer f.Close()
	content, _ := io.ReadAll(f)
	doc := string(content)
	for _, want := range []string{"测试项目", "cpu_usage", "85.50%", "80.00%", "严重"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("document.xml 缺少内容 %q", want)
		}
	}
}
