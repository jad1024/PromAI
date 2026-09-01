// Package portscan 提供敏感端口主动探测能力。
//
// 与巡检体系解耦：手动触发、不接告警/通知链路、结果独立成报告。
// 核心是并发 TCP connect 探测：对目标 IP × 端口 组合发起连接，
// 连接成功（或对端在 SYN 后返回 RST 之外仍能建立）即视为端口「开放」。
package portscan

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PortInfo 一个敏感端口的元信息。
type PortInfo struct {
	Port int    `json:"port"`
	Name string `json:"name"` // 服务名，如 ssh / mysql
	Risk string `json:"risk"` // high / medium / low
}

// DefaultSensitivePorts 内置敏感端口列表（公网 IP 一旦开放即视为暴露风险）。
// 分级依据：
//   - high：数据库/缓存/消息队列/容器管理/中间件后台直接暴露，可被直接入侵或拖库；
//   - medium：远程管理/文件共享端口，可被爆破或横向渗透；
//   - low：常见管理后台/备用 Web 端口，属暴露面但危害相对有限。
var DefaultSensitivePorts = []PortInfo{
	// —— 高危：数据库 / 缓存 / 消息队列 / 容器 / 中间件 ——
	{3306, "MySQL", "high"},
	{5432, "PostgreSQL", "high"},
	{1433, "MSSQL", "high"},
	{1521, "Oracle", "high"},
	{6379, "Redis", "high"},
	{27017, "MongoDB", "high"},
	{9200, "Elasticsearch", "high"},
	{9300, "ES Transport", "high"},
	{11211, "Memcached", "high"},
	{2375, "Docker API", "high"},
	{2376, "Docker TLS", "high"},
	{2379, "Etcd", "high"},
	{2380, "Etcd Peer", "high"},
	{9092, "Kafka", "high"},
	{9042, "Cassandra", "high"},
	{7001, "WebLogic", "high"},
	{8848, "Nacos", "high"},
	{2181, "ZooKeeper", "high"},
	{5601, "Kibana", "high"},
	{50070, "Hadoop NameNode", "high"},
	// —— 中危：远程管理 / 文件共享 ——
	{22, "SSH", "medium"},
	{23, "Telnet", "medium"},
	{21, "FTP", "medium"},
	{3389, "RDP", "medium"},
	{445, "SMB", "medium"},
	{135, "RPC", "medium"},
	{139, "NetBIOS", "medium"},
	{5900, "VNC", "medium"},
	{5800, "VNC Web", "medium"},
	// —— 低危：常见管理后台 / 备用 Web ——
	{8080, "HTTP-Alt", "low"},
	{8443, "HTTPS-Alt", "low"},
	{8000, "HTTP-Alt", "low"},
	{8888, "HTTP-Alt", "low"},
	{9090, "HTTP-Alt", "low"},
	{5000, "HTTP-Alt", "low"},
}

// PortInfoByPort 返回按端口号索引的元信息；未知端口返回 low 级占位。
func PortInfoByPort(port int) PortInfo {
	for _, p := range DefaultSensitivePorts {
		if p.Port == port {
			return p
		}
	}
	return PortInfo{Port: port, Name: fmt.Sprintf("port-%d", port), Risk: "low"}
}

// Result 单次端口探测结果。
type Result struct {
	IP        string
	Port      int
	PortName  string
	Risk      string
	Open      bool
	State     string // open / closed / timeout / refused
	LatencyMs int64
}

// Options 扫描选项。
type Options struct {
	Timeout     time.Duration // 单连接超时，默认 2s
	Concurrency int           // 并发连接数，默认 100
}

// Scan 对 targets × ports 做并发 TCP 探测，返回全部结果（含未开放的端口）。
func Scan(targets []string, ports []int, opts Options) []Result {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 100
	}

	type job struct {
		ip   string
		port int
	}
	jobs := make([]job, 0, len(targets)*len(ports))
	for _, ip := range targets {
		for _, p := range ports {
			jobs = append(jobs, job{ip: ip, port: p})
		}
	}

	results := make([]Result, len(jobs))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, j job) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx] = probe(j.ip, j.port, timeout)
		}(i, j)
	}
	wg.Wait()
	return results
}

// probe 探测单个 ip:port 是否开放。
func probe(ip string, port int, timeout time.Duration) Result {
	info := PortInfoByPort(port)
	r := Result{IP: ip, Port: port, PortName: info.Name, Risk: info.Risk, State: "closed"}

	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	r.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		// 区分超时与明确拒绝：连接被拒说明端口关闭，超时说明被过滤（防火墙丢包）
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			r.State = "timeout"
		} else if strings.Contains(err.Error(), "refused") {
			r.State = "refused"
		} else {
			r.State = "closed"
		}
		r.Open = false
		return r
	}
	_ = conn.Close()
	r.Open = true
	r.State = "open"
	return r
}

// ParseTargets 解析批量粘贴的目标（一行一个）。
// 支持：单个 IPv4、IPv4:端口（端口忽略）、CIDR（如 10.0.0.0/24，会展开）。
// 返回去重后的 IP 列表；CIDR 展开总量受 maxExpand 限制。
func ParseTargets(raw string, maxExpand int) ([]string, error) {
	if maxExpand <= 0 {
		maxExpand = 4096
	}
	seen := map[string]struct{}{}
	var out []string

	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 去掉行内注释与多余空白（支持逗号/分号/空格分隔）
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		// 一行可能用逗号/分号/空白分隔多个目标
		for _, token := range strings.FieldsFunc(line, func(r rune) bool {
			return r == ',' || r == ';' || r == ' ' || r == '\t'
		}) {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			// 去掉可选端口后缀 ip:port
			if h, _, err := net.SplitHostPort(token); err == nil {
				token = h
			}
			// CIDR
			if strings.Contains(token, "/") {
				ips, err := expandCIDR(token)
				if err != nil {
					return nil, fmt.Errorf("无法解析目标 %q: %w", token, err)
				}
				for _, ip := range ips {
					if _, ok := seen[ip]; !ok {
						seen[ip] = struct{}{}
						out = append(out, ip)
						if len(out) > maxExpand {
							return nil, fmt.Errorf("目标数量超过上限 %d，请缩小范围", maxExpand)
						}
					}
				}
				continue
			}
			ip := net.ParseIP(token)
			if ip == nil {
				return nil, fmt.Errorf("无法解析目标 IP %q", token)
			}
			if ip.To4() == nil {
				return nil, fmt.Errorf("暂不支持 IPv6 目标 %q", token)
			}
			s := ip.String()
			if _, ok := seen[s]; !ok {
				seen[s] = struct{}{}
				out = append(out, s)
				if len(out) > maxExpand {
					return nil, fmt.Errorf("目标数量超过上限 %d，请缩小范围", maxExpand)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("未解析到任何目标 IP")
	}
	sort.Strings(out)
	return out, nil
}

// expandCIDR 展开一个 IPv4 CIDR 为全部主机 IP。
func expandCIDR(cidr string) ([]string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	if ip.To4() == nil {
		return nil, fmt.Errorf("暂不支持 IPv6 CIDR %q", cidr)
	}
	ip4 := ip.To4()
	mask := ipnet.Mask
	// 主机位数量
	ones, bits := mask.Size()
	hostBits := bits - ones
	if hostBits > 16 { // 超过 /16 视为过大，拒绝展开
		return nil, fmt.Errorf("CIDR %q 过大（%d 个地址），请使用更小网段", cidr, 1<<hostBits)
	}
	count := 1 << hostBits
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		a := ip4[0]
		b := ip4[1]
		c := ip4[2]
		d := ip4[3]
		v := uint32(i)
		if hostBits > 0 {
			d += byte(v & 0xff)
		}
		if hostBits > 8 {
			c += byte((v >> 8) & 0xff)
		}
		if hostBits > 16 {
			b += byte((v >> 16) & 0xff)
		}
		out = append(out, net.IPv4(a, b, c, d).String())
	}
	return out, nil
}

// ParsePorts 解析端口列表（逗号/空白分隔，支持范围如 8000-8100）。
func ParsePorts(raw string) ([]int, error) {
	seen := map[int]struct{}{}
	var out []int
	for _, token := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	}) {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if strings.Contains(token, "-") {
			parts := strings.SplitN(token, "-", 2)
			lo, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
			hi, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err1 != nil || err2 != nil || lo < 1 || hi > 65535 || lo > hi {
				return nil, fmt.Errorf("端口范围 %q 无效", token)
			}
			for p := lo; p <= hi; p++ {
				if _, ok := seen[p]; !ok {
					seen[p] = struct{}{}
					out = append(out, p)
				}
			}
			continue
		}
		p, err := strconv.Atoi(token)
		if err != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("端口 %q 无效", token)
		}
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("未解析到任何端口")
	}
	sort.Ints(out)
	return out, nil
}
