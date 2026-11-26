package main

import (
	"testing"
)

func TestJSONFieldString(t *testing.T) {
	item := map[string]any{
		"level":  "ERROR",
		"count":  float64(42),
		"nested": map[string]any{"a": "b"},
		"_msg":   "hello",
	}
	cases := []struct{ field, want string }{
		{"level", "ERROR"},
		{"count", "42"},
		{"_msg", "hello"},
		{"missing", ""},
	}
	for _, c := range cases {
		if got := jsonFieldString(item, c.field); got != c.want {
			t.Fatalf("jsonFieldString(%s)=%q, want %q", c.field, got, c.want)
		}
	}
}

func TestDetectTraceID(t *testing.T) {
	cases := []struct {
		name string
		item map[string]any
		want string
	}{
		{"traceId 字段", map[string]any{"traceId": "abc-123"}, "abc-123"},
		{"下划线命名", map[string]any{"trace_id": "xyz"}, "xyz"},
		{"requestId 兜底", map[string]any{"requestId": "req-1"}, "req-1"},
		{"traceparent 提取", map[string]any{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}, "4bf92f3577b34da6a3ce929d0e0e4736"},
		{"无 traceId", map[string]any{"_msg": "no trace"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detectTraceID(c.item); got != c.want {
				t.Fatalf("detectTraceID=%q, want %q", got, c.want)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "value", "other"); got != "value" {
		t.Fatalf("firstNonEmpty=%q, want value", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("firstNonEmpty=%q, want empty", got)
	}
}
