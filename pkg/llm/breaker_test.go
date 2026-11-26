package llm

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBreakerOpensAfterThresholdAndRejects(t *testing.T) {
	b := NewBreaker(3, 500*time.Millisecond, 2)
	key := NewBreakerKey("openai", "gpt-4o", "sk-secret")

	for i := 0; i < 3; i++ {
		if err := b.Allow(key); err != nil {
			t.Fatalf("第 %d 次 Allow 应放行: %v", i+1, err)
		}
		b.RecordFailure(key)
	}
	if got := b.State(key); got != "open" {
		t.Fatalf("3 次失败后应为 open，实际 %s", got)
	}

	err := b.Allow(key)
	var be *BreakerError
	if !errors.As(err, &be) {
		t.Fatalf("熔断期间应返回 BreakerError，实际 %T: %v", err, err)
	}
	if be.Remaining <= 0 || be.Remaining > 500*time.Millisecond {
		t.Fatalf("剩余冷却时间不合理: %v", be.Remaining)
	}
	if strings.Contains(err.Error(), "sk-secret") {
		t.Fatal("错误信息泄露了 API key 明文")
	}
}

func TestBreakerSuccessResetsFailures(t *testing.T) {
	b := NewBreaker(3, time.Hour, 2)
	key := NewBreakerKey("p", "m", "k")

	b.RecordFailure(key)
	b.RecordFailure(key)
	if got := b.State(key); got != "closed" {
		t.Fatalf("未达阈值应仍为 closed，实际 %s", got)
	}
	b.RecordSuccess(key)
	b.RecordFailure(key)
	if got := b.State(key); got != "closed" {
		t.Fatalf("成功后应清零失败计数，实际 %s", got)
	}
}

func TestBreakerHalfOpenTransitions(t *testing.T) {
	t.Run("全部试探成功则关闭", func(t *testing.T) {
		b := NewBreaker(2, 10*time.Millisecond, 3)
		key := NewBreakerKey("p", "m", "k")
		b.RecordFailure(key)
		b.RecordFailure(key)
		time.Sleep(15 * time.Millisecond)

		if err := b.Allow(key); err != nil {
			t.Fatalf("冷却结束应转半开并放行: %v", err)
		}
		if got := b.State(key); got != "half-open" {
			t.Fatalf("应为 half-open，实际 %s", got)
		}
		// 半开最多 3 次
		if err := b.Allow(key); err != nil {
			t.Fatalf("第 2 次试探应放行: %v", err)
		}
		if err := b.Allow(key); err != nil {
			t.Fatalf("第 3 次试探应放行: %v", err)
		}
		if err := b.Allow(key); err == nil {
			t.Fatal("第 4 次试探应被拒绝")
		}
		b.RecordSuccess(key)
		b.RecordSuccess(key)
		b.RecordSuccess(key)
		if got := b.State(key); got != "closed" {
			t.Fatalf("3 次成功后应关闭，实际 %s", got)
		}
	})

	t.Run("半开一次失败即重新打开", func(t *testing.T) {
		b := NewBreaker(1, 10*time.Millisecond, 3)
		key := NewBreakerKey("p", "m", "k")
		b.RecordFailure(key)
		time.Sleep(15 * time.Millisecond)

		if err := b.Allow(key); err != nil {
			t.Fatalf("冷却结束应放行一次试探: %v", err)
		}
		b.RecordFailure(key)
		if got := b.State(key); got != "open" {
			t.Fatalf("试探失败应重新打开，实际 %s", got)
		}
	})
}

func TestBreakerIsolatesByTriple(t *testing.T) {
	b := NewBreaker(1, time.Hour, 2)
	k1 := NewBreakerKey("provider-a", "model-x", "key-1")
	k2 := NewBreakerKey("provider-a", "model-x", "key-2")
	k3 := NewBreakerKey("provider-b", "model-x", "key-1")

	b.RecordFailure(k1)
	if got := b.State(k1); got != "open" {
		t.Fatalf("k1 应熔断，实际 %s", got)
	}
	for _, k := range []BreakerKey{k2, k3} {
		if err := b.Allow(k); err != nil {
			t.Fatalf("不同 key 不应受影响 %v: %v", k, err)
		}
		if got := b.State(k); got != "closed" {
			t.Fatalf("不同 key 应保持 closed %v，实际 %s", k, got)
		}
	}
}

func TestShortKeyHashNeverContainsKey(t *testing.T) {
	key := "sk-this-is-a-very-secret-token"
	h := shortKeyHash(key)
	if len(h) != 8 {
		t.Fatalf("哈希长度应为 8，实际 %d (%s)", len(h), h)
	}
	if strings.Contains(h, "secret") || strings.Contains(h, key) {
		t.Fatalf("哈希泄露了 key: %s", h)
	}
	if shortKeyHash("") != "nokey" {
		t.Fatal("空 key 应返回 nokey 标记")
	}
}

func TestBreakerConcurrentAccess(t *testing.T) {
	b := NewBreaker(100, time.Hour, 3)
	key := NewBreakerKey("p", "m", "k")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = b.Allow(key)
			if i%2 == 0 {
				b.RecordSuccess(key)
			} else {
				b.RecordFailure(key)
			}
			_ = b.State(key)
		}(i)
	}
	wg.Wait()
	if got := b.State(key); got == "" {
		t.Fatal("并发访问后状态不应为空")
	}
}
