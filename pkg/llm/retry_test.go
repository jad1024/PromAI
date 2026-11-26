package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"429", fmt.Errorf("API error 429: {\"error\":\"rate limit\"}"), true},
		{"500", errors.New("API error 500: internal"), true},
		{"502", errors.New("API error 502: bad gateway"), true},
		{"503", errors.New("API error 503: unavailable"), true},
		{"504", errors.New("API error 504: gateway timeout"), true}, // 网关快速返回，可重试（区别于本地 120s 请求超时）
		{"401", errors.New("API error 401: unauthorized"), false},
		{"403", errors.New("API error 403: forbidden"), false},
		{"400", errors.New("API error 400: bad request"), false},
		{"nightingale style", errors.New("HTTP 503 Service Unavailable"), true},
		{"network", errors.New("request failed: dial tcp: connection refused"), true},
		{"eof", errors.New("request failed: unexpected EOF"), true},
		{"generic timeout", errors.New("AI 无头分析超时（1m30s）"), false},
		{"context deadline", context.DeadlineExceeded, false},
		{"context canceled", context.Canceled, false},
		{"empty content", errors.New("AI 无头分析无返回内容"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyError(tc.err); got != tc.retryable {
				t.Fatalf("ClassifyError(%v) = %v, want %v", tc.err, got, tc.retryable)
			}
		})
	}
}

func TestBackoffBoundsAndJitter(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: 10 * time.Second}
	// attempt=1 → 1s ±25%
	d1 := Backoff(1, cfg)
	if d1 < 750*time.Millisecond || d1 > 1250*time.Millisecond {
		t.Fatalf("首次退避超出范围: %v", d1)
	}
	// attempt=10 → 封顶 10s ±25%
	dMax := Backoff(10, cfg)
	if dMax < 7500*time.Millisecond || dMax > 12500*time.Millisecond {
		t.Fatalf("退避上限超出范围: %v", dMax)
	}
	// jitter 确实有随机性
	sawDifferent := false
	first := Backoff(3, cfg)
	for i := 0; i < 20; i++ {
		if Backoff(3, cfg) != first {
			sawDifferent = true
			break
		}
	}
	if !sawDifferent {
		t.Fatal("jitter 未生效：20 次采样结果完全相同")
	}
}

func TestCallWithRetrySucceedsAfterTransientFailure(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	attempts := 0
	got, err := CallWithRetry[string](context.Background(), cfg, func(_ context.Context, n int) (string, error) {
		attempts = n
		if n < 3 {
			return "", fmt.Errorf("API error 503: attempt %d", n)
		}
		return "ok", nil
	})
	if err != nil || got != "ok" {
		t.Fatalf("应重试后成功，got=%q err=%v", got, err)
	}
	if attempts != 3 {
		t.Fatalf("应尝试 3 次，实际 %d", attempts)
	}
}

func TestCallWithRetryDoesNotRetryPermanentError(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	attempts := 0
	_, err := CallWithRetry[string](context.Background(), cfg, func(_ context.Context, n int) (string, error) {
		attempts = n
		return "", fmt.Errorf("API error 401: unauthorized")
	})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("应原样返回 401 错误，实际 %v", err)
	}
	if attempts != 1 {
		t.Fatalf("401 不应重试，实际尝试 %d 次", attempts)
	}
}

func TestCallWithRetryStopsAtMaxAttempts(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	attempts := 0
	_, err := CallWithRetry[string](context.Background(), cfg, func(_ context.Context, n int) (string, error) {
		attempts = n
		return "", fmt.Errorf("API error 429: slow down")
	})
	if err == nil {
		t.Fatal("应返回错误")
	}
	if attempts != 2 {
		t.Fatalf("应在 2 次后停止，实际 %d", attempts)
	}
}

func TestCallWithRetryKeepsPartialResult(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
	res, err := CallWithRetry[*struct{ Name string }](context.Background(), cfg, func(_ context.Context, _ int) (*struct{ Name string }, error) {
		return &struct{ Name string }{"partial"}, errors.New("API error 400: bad")
	})
	if err == nil {
		t.Fatal("应返回错误")
	}
	if res == nil || res.Name != "partial" {
		t.Fatal("失败时应保留部分结果，避免调用方 nil panic")
	}
}

func TestCallWithRetryRespectsContextCancel(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 5, BaseDelay: 200 * time.Millisecond, MaxDelay: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := CallWithRetry[string](ctx, cfg, func(_ context.Context, n int) (string, error) {
		attempts = n
		return "", fmt.Errorf("API error 503: unavailable")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 context.Canceled，实际 %v", err)
	}
	if attempts >= 5 {
		t.Fatalf("取消后不应继续尝试，实际 %d 次", attempts)
	}
}
