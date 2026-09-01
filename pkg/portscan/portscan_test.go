package portscan

import (
	"reflect"
	"testing"
)

func TestParseTargets(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{
			name: "单行单个 IP",
			raw:  "1.2.3.4",
			want: []string{"1.2.3.4"},
		},
		{
			name: "批量换行",
			raw:  "1.2.3.4\n5.6.7.8\n9.10.11.12",
			want: []string{"1.2.3.4", "5.6.7.8", "9.10.11.12"},
		},
		{
			name: "逗号分隔 + 去重",
			raw:  "1.2.3.4, 5.6.7.8, 1.2.3.4",
			want: []string{"1.2.3.4", "5.6.7.8"},
		},
		{
			name: "带端口后缀忽略端口",
			raw:  "1.2.3.4:3306\n5.6.7.8:22",
			want: []string{"1.2.3.4", "5.6.7.8"},
		},
		{
			name: "注释行与空行忽略",
			raw:  "1.2.3.4\n# 这是注释\n\n5.6.7.8",
			want: []string{"1.2.3.4", "5.6.7.8"},
		},
		{
			name: "CIDR 展开 /30",
			raw:  "10.0.0.0/30",
			want: []string{"10.0.0.0", "10.0.0.1", "10.0.0.2", "10.0.0.3"},
		},
		{
			name:    "非法 IP",
			raw:     "999.1.1.1",
			wantErr: true,
		},
		{
			name:    "空输入",
			raw:     "  \n  ",
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseTargets(c.raw, 4096)
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际成功返回 %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望报错，得到 %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("结果不符：got=%v want=%v", got, c.want)
			}
		})
	}
}

func TestParseTargetsCIDRTooLarge(t *testing.T) {
	if _, err := ParseTargets("10.0.0.0/8", 4096); err == nil {
		t.Fatalf("过大 CIDR /8 应被拒绝")
	}
}

func TestParsePorts(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    []int
		wantErr bool
	}{
		{name: "列表", raw: "22, 3306, 6379", want: []int{22, 3306, 6379}},
		{name: "去重", raw: "22, 22, 3306", want: []int{22, 3306}},
		{name: "范围", raw: "8000-8003", want: []int{8000, 8001, 8002, 8003}},
		{name: "换行分隔", raw: "22\n3306", want: []int{22, 3306}},
		{name: "非法", raw: "abc", wantErr: true},
		{name: "越界", raw: "70000", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParsePorts(c.raw)
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际成功返回 %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望报错，得到 %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("结果不符：got=%v want=%v", got, c.want)
			}
		})
	}
}

func TestDefaultSensitivePortsNoDuplicates(t *testing.T) {
	seen := map[int]struct{}{}
	for _, p := range DefaultSensitivePorts {
		if p.Port < 1 || p.Port > 65535 {
			t.Fatalf("端口越界: %d", p.Port)
		}
		if _, ok := seen[p.Port]; ok {
			t.Fatalf("端口重复: %d", p.Port)
		}
		seen[p.Port] = struct{}{}
		if p.Risk != "high" && p.Risk != "medium" && p.Risk != "low" {
			t.Fatalf("风险等级非法: %s (%d)", p.Risk, p.Port)
		}
	}
}
