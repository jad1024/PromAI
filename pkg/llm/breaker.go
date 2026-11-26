// Package llm 提供 LLM 调用的 resilience 原语（熔断器 + 重试）。
//
// 设计来源：借鉴 itops-agent-platform 的 LLM 熔断器思路，但修正了两个缺陷：
//  1. 熔断粒度按 (provider, model, key 短哈希) 三元组隔离，避免一个坏 key 拖垮整个 provider；
//  2. API key 只保留 8 位哈希，日志中不泄露明文。
//
// 本包不依赖 pi SDK、不依赖数据库，可独立测试。
package llm

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultFailureThreshold 连续失败达到该次数后熔断。
	DefaultFailureThreshold = 5
	// DefaultResetTimeout 熔断打开后，经过该时长进入半开试探。
	DefaultResetTimeout = 60 * time.Second
	// DefaultHalfOpenMaxCalls 半开状态下允许的最大试探请求数，全部成功才关闭熔断。
	DefaultHalfOpenMaxCalls = 3
)

// BreakerKey 熔断隔离键。
// Provider/Model 是配置里的模型标识，KeyID 是 API key 的短哈希（避免明文出现在日志/内存快照里）。
type BreakerKey struct {
	Provider string
	Model    string
	KeyID    string
}

// String 返回可打印的键（不含完整密钥）。
func (k BreakerKey) String() string {
	return fmt.Sprintf("%s/%s/%s", k.Provider, k.Model, k.KeyID)
}

// NewBreakerKey 从配置构造熔断键。apiKey 仅参与哈希，不会被保存。
func NewBreakerKey(provider, model, apiKey string) BreakerKey {
	return BreakerKey{
		Provider: provider,
		Model:    model,
		KeyID:    shortKeyHash(apiKey),
	}
}

// shortKeyHash 取 SHA-256 前 4 字节的十六进制（8 字符）。
// 空 key 用固定标记，便于区分"未配置 key"的场景。
func shortKeyHash(apiKey string) string {
	if apiKey == "" {
		return "nokey"
	}
	sum := sha256.Sum256([]byte(apiKey))
	return fmt.Sprintf("%x", sum[:4])
}

type circuitState int

const (
	circuitClosed circuitState = iota // 关闭：正常放行
	circuitOpen                        // 打开：直接拒绝
	circuitHalfOpen                    // 半开：限量试探
)

// circuit 单个三元组的熔断状态。
type circuit struct {
	state             circuitState
	failures          int
	openedAt          time.Time
	halfOpenInFlight  int
	halfOpenSuccesses int
}

// Breaker 线程安全的 LLM 熔断器。
type Breaker struct {
	mu               sync.Mutex
	circuits         map[BreakerKey]*circuit
	failureThreshold int
	resetTimeout     time.Duration
	halfOpenMaxCalls int
}

// NewBreaker 创建熔断器；参数 <= 0 时使用默认值。
func NewBreaker(failureThreshold int, resetTimeout time.Duration, halfOpenMaxCalls int) *Breaker {
	if failureThreshold <= 0 {
		failureThreshold = DefaultFailureThreshold
	}
	if resetTimeout <= 0 {
		resetTimeout = DefaultResetTimeout
	}
	if halfOpenMaxCalls <= 0 {
		halfOpenMaxCalls = DefaultHalfOpenMaxCalls
	}
	return &Breaker{
		circuits:         make(map[BreakerKey]*circuit),
		failureThreshold: failureThreshold,
		resetTimeout:     resetTimeout,
		halfOpenMaxCalls: halfOpenMaxCalls,
	}
}

// DefaultBreaker 全局默认熔断器（进程级共享）。
var DefaultBreaker = NewBreaker(0, 0, 0)

// BreakerError 熔断拒绝时返回的错误，携带剩余冷却时间供调用方展示。
type BreakerError struct {
	Key       BreakerKey
	Remaining time.Duration
}

func (e *BreakerError) Error() string {
	return fmt.Sprintf("LLM 熔断中 [%s]，%.0f 秒后恢复试探", e.Key, e.Remaining.Seconds())
}

// Allow 检查是否允许发起调用。
// OPEN 且未到冷却时间 → 拒绝；到时则转半开并放行一次试探；半开状态限量放行。
func (b *Breaker) Allow(key BreakerKey) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	c := b.circuitLocked(key)
	switch c.state {
	case circuitOpen:
		remaining := b.resetTimeout - time.Since(c.openedAt)
		if remaining > 0 {
			return &BreakerError{Key: key, Remaining: remaining}
		}
		// 冷却结束 → 半开；本次放行即算第 1 次试探，必须计入配额，
		// 否则半开实际会多放行一次（maxCalls+1）。
		c.state = circuitHalfOpen
		c.halfOpenInFlight = 1
		c.halfOpenSuccesses = 0
		return nil
	case circuitHalfOpen:
		if c.halfOpenInFlight >= b.halfOpenMaxCalls {
			return &BreakerError{Key: key}
		}
		c.halfOpenInFlight++
		return nil
	default:
		return nil
	}
}

// RecordSuccess 记录一次成功调用。
// 半开状态下，累计成功达到阈值才真正关闭熔断；关闭状态下清零失败计数。
func (b *Breaker) RecordSuccess(key BreakerKey) {
	b.mu.Lock()
	defer b.mu.Unlock()

	c := b.circuitLocked(key)
	if c.state == circuitHalfOpen {
		if c.halfOpenInFlight > 0 {
			c.halfOpenInFlight--
		}
		c.halfOpenSuccesses++
		if c.halfOpenSuccesses >= b.halfOpenMaxCalls {
			c.state = circuitClosed
			c.failures = 0
			c.halfOpenInFlight = 0
			c.halfOpenSuccesses = 0
		}
		return
	}
	c.failures = 0
}

// RecordFailure 记录一次失败调用。
// 半开状态下任意一次失败都会重新打开熔断；关闭状态下累计到阈值后打开。
func (b *Breaker) RecordFailure(key BreakerKey) {
	b.mu.Lock()
	defer b.mu.Unlock()

	c := b.circuitLocked(key)
	if c.state == circuitHalfOpen {
		c.state = circuitOpen
		c.openedAt = time.Now()
		c.halfOpenInFlight = 0
		c.halfOpenSuccesses = 0
		return
	}
	c.failures++
	if c.failures >= b.failureThreshold {
		c.state = circuitOpen
		c.openedAt = time.Now()
	}
}

// State 返回指定键当前状态（供 UI/诊断使用）。
func (b *Breaker) State(key BreakerKey) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.circuits[key]
	if !ok {
		return "closed"
	}
	switch c.state {
	case circuitOpen:
		return "open"
	case circuitHalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

// circuitLocked 获取或创建状态，调用方必须已持有 b.mu。
func (b *Breaker) circuitLocked(key BreakerKey) *circuit {
	c, ok := b.circuits[key]
	if !ok {
		c = &circuit{}
		b.circuits[key] = c
	}
	return c
}
