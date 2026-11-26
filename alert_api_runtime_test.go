package main

import "testing"

// TestParseSQLiteTime 覆盖生产日志中出现的各类 SQLite 时间字符串格式。
// 背景：/build/alert_api_runtime.go:693 曾因把 MAX(COALESCE(...)) 的
// string 结果直接 Scan 到 time.Time 而刷出 "unsupported Scan" 错误。
func TestParseSQLiteTime(t *testing.T) {
	cases := []struct {
		in   string
		want bool
		year int
	}{
		// 生产日志实际格式：occurred_at >= "2026-11-25 02:53:08.51"
		{"2026-11-25 02:53:08.51", true, 2026},
		{"2026-11-25 02:53:08", true, 2026},
		{"2026-11-25 02:53:08.510000000", true, 2026},
		// 带时区偏移（GORM 常见写入格式）
		{"2026-11-25 02:53:08.51+08:00", true, 2026},
		{"2026-11-25 02:53:08+00:00", true, 2026},
		// RFC3339 / ISO8601
		{"2026-11-25T02:53:08Z", true, 2026},
		{"2026-11-25T02:53:08.51+08:00", true, 2026},
		// 仅日期
		{"2026-11-25", true, 2026},
		// 异常/空值：不 panic，返回 false 交由兜底逻辑处理
		{"", false, 0},
		{"   ", false, 0},
		{"not-a-time", false, 0},
		{"0001-01-01 00:00:00", true, 1}, // 解析成功但 year<=1970，调用方会忽略
	}
	for _, c := range cases {
		got, ok := parseSQLiteTime(c.in)
		if ok != c.want {
			t.Errorf("parseSQLiteTime(%q) ok=%v, want %v", c.in, ok, c.want)
			continue
		}
		if ok && got.Year() != c.year {
			t.Errorf("parseSQLiteTime(%q) year=%d, want %d", c.in, got.Year(), c.year)
		}
	}
}

// TestParseSQLiteTimeLeapAndBoundary 边界值不应 panic 或误解析
func TestParseSQLiteTimeLeapAndBoundary(t *testing.T) {
	for _, s := range []string{"2024-02-29 23:59:59.999", "1970-01-01 00:00:00", "9999-12-31 23:59:59"} {
		if _, ok := parseSQLiteTime(s); !ok {
			t.Errorf("parseSQLiteTime(%q) 应可解析", s)
		}
	}
}
