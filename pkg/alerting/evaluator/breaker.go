package evaluator

import (
	"sync"
	"sync/atomic"
	"time"
)

// breaker 是一个简单的指数退避熔断器，per datasource 维护。
//
// 状态：
//   closed   → 正常：所有评估正常执行
//   open     → 退避中：跳过查询，等待 openUntil 时间过去
//
// 失败计数达到 BreakerThreshold 后切换到 open；
// open 期间过去后下一次评估会先尝试一次（半开），成功则重置，失败则继续 backoff（指数翻倍）。
type breaker struct {
	mu              sync.Mutex
	consecutiveFail int
	openUntil       time.Time
	currentBackoff  time.Duration
}

func (b *breaker) isOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return false
	}
	return time.Now().Before(b.openUntil)
}

func (b *breaker) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFail = 0
	b.openUntil = time.Time{}
	b.currentBackoff = 0
}

func (b *breaker) recordFailure(cfg Config) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFail++
	if b.consecutiveFail < cfg.BreakerThreshold {
		return
	}
	if b.currentBackoff == 0 {
		b.currentBackoff = cfg.BreakerInitialBackoff
	} else {
		b.currentBackoff *= 2
		if b.currentBackoff > cfg.BreakerMaxBackoff {
			b.currentBackoff = cfg.BreakerMaxBackoff
		}
	}
	b.openUntil = time.Now().Add(b.currentBackoff)
}

func (e *Evaluator) breakerFor(id uint) *breaker {
	v, ok := e.breakers.Load(id)
	if ok {
		return v.(*breaker)
	}
	newB := &breaker{}
	actual, loaded := e.breakers.LoadOrStore(id, newB)
	if loaded {
		return actual.(*breaker)
	}
	e.stats.BreakerOpenCount.Add(0) // 仅占位
	return newB
}

// 避免 unused atomic import 在某些重构后报警
var _ = atomic.Int64{}
