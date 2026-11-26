package main

import "testing"

func TestMatchScore(t *testing.T) {
	tests := []struct {
		name  string
		cat   string
		kws   string
		ds    string
		want  int
		want2 bool // >=40
	}{
		{"数据源名直接含 category", "mysql", "mysql,数据库", "prod-mysql-01", 100, true},
		{"关键词命中", "redis", "redis,缓存,cache", "cache-cluster", 40, true},
		{"多关键词命中", "mysql", "mysql,数据库,db", "db 数据库服务", 80, true},
		{"完全不匹配", "kafka", "kafka,队列", "web-server", 0, false},
		{"空数据源", "mysql", "mysql", "", 0, false},
	}
	for _, tt := range tests {
		got := matchScore("模板", tt.cat, tt.kws, tt.ds)
		if tt.want2 {
			if got < 40 {
				t.Errorf("%s: got %d want >=40", tt.name, got)
			}
		} else if got != tt.want {
			t.Errorf("%s: got %d want %d", tt.name, got, tt.want)
		}
	}
}

func TestTimelineSort(t *testing.T) {
	points := []FCHealthPoint{{T: 300}, {T: 100}, {T: 200}}
	sortPointsByTime(points)
	if points[0].T != 100 || points[1].T != 200 || points[2].T != 300 {
		t.Errorf("排序失败: %+v", points)
	}
}
