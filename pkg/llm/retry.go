package llm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RetryConfig 重试参数。
type RetryConfig struct {
	MaxAttempts int           // 总尝试次数（含首次）
	BaseDelay   time.Duration // 首次重试的基础退避
	MaxDelay    time.Duration // 退避上限
}

// DefaultRetry 默认重试参数：3 次尝试，1s → 2s → 4s（上限 10s），带 ±25% jitter。
var DefaultRetry = RetryConfig{
	MaxAttempts: 3,
	BaseDelay:   1 * time.Second,
	MaxDelay:    10 * time.Second,
}

// pi SDK 的错误格式：doStream 返回 "API error %d: %s" / "request failed: %w"；
// 夜莺风格的错误格式：HTTP 429 / status 503，这里一并兼容。
var statusCodeRe = regexp.MustCompile(`(?i)(?:API error\s+|HTTP\s+|status\s+)(\d{3})\b`)

// transientNetworkRe 匹配可重试的网络层错误。
var transientNetworkRe = regexp.MustCompile(`(?i)request failed|connection refused|connection reset|no such host|broken pipe|unexpected EOF|temporarily unavailable`)

// retryableStatusCodes 瞬时故障状态码。
// 含 504（网关快速返回的超时），但不含 408：pi SDK 的请求不吃外部 context，
// 本地请求超时重试会叠加 120s 等待，拖垮巡检；504 是服务端立即回包，重试安全。
var retryableStatusCodes = map[int]bool{
	425: true, // Too Early
	429: true, // Too Many Requests
	500: true, // Internal Server Error
	502: true, // Bad Gateway
	503: true, // Service Unavailable
	504: true, // Gateway Timeout
	522: true, // Cloudflare connection timed out
	524: true, // Cloudflare timeout
}

// ClassifyError 判断错误是否值得重试。
// 只有明确属于瞬时故障的错误才返回 true；认证/参数/超时等错误一律不重试，
// 避免无意义地重复请求或把一次巡检拖长。
func ClassifyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	msg := err.Error()
	if m := statusCodeRe.FindStringSubmatch(msg); len(m) == 2 {
		code, convErr := strconv.Atoi(m[1])
		if convErr == nil {
			return retryableStatusCodes[code]
		}
		return false
	}

	lower := strings.ToLower(msg)
	// 显式超时不重试（见上方说明）
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") || strings.Contains(lower, "超时") {
		return false
	}
	return transientNetworkRe.MatchString(msg)
}

// Backoff 计算第 attempt 次失败后的退避时长（attempt 从 1 开始）。
// 公式：min(BaseDelay * 2^(attempt-1), MaxDelay)，再乘 ±25% jitter。
func Backoff(attempt int, cfg RetryConfig) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	base := cfg.BaseDelay
	if base <= 0 {
		base = DefaultRetry.BaseDelay
	}
	maxDelay := cfg.MaxDelay
	if maxDelay <= 0 {
		maxDelay = DefaultRetry.MaxDelay
	}
	d := float64(base) * math.Pow(2, float64(attempt-1))
	if d > float64(maxDelay) {
		d = float64(maxDelay)
	}
	jitter := 0.75 + rand.Float64()*0.5 // [0.75, 1.25)
	return time.Duration(d * jitter)
}

// CallWithRetry 在重试循环内执行 fn。
// fn 接收当前尝试序号（从 1 开始），便于调用方打日志。
// 失败时返回最后一次的返回值和错误（不丢弃部分结果，避免调用方 nil panic）。
func CallWithRetry[T any](ctx context.Context, cfg RetryConfig, fn func(ctx context.Context, attempt int) (T, error)) (T, error) {
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultRetry.MaxAttempts
	}

	var lastResult T
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return lastResult, err
		}

		res, err := fn(ctx, attempt)
		lastResult, lastErr = res, err
		if err == nil {
			return res, nil
		}
		if attempt >= maxAttempts || !ClassifyError(err) {
			return res, err
		}

		delay := Backoff(attempt, cfg)
		log.Printf("[LLM Retry] 第 %d/%d 次调用失败（%v），%v 后重试", attempt, maxAttempts, err, delay)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return lastResult, ctx.Err()
		}
	}
	return lastResult, lastErr
}

// FormatRetryError 把最终错误包装成带尝试次数的可读信息，供调用方落库。
func FormatRetryError(err error, attempts int) error {
	if err == nil || attempts <= 1 {
		return err
	}
	return fmt.Errorf("%w（已重试 %d 次）", err, attempts-1)
}
