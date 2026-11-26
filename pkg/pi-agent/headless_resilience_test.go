package piagent

import (
	"context"
	"errors"
	"testing"
	"time"

	"PromAI/pkg/config"
	"PromAI/pkg/llm"
)

// TestRunHeadlessBreakerRejectsWithoutPanic 验证熔断打开时：
//  1. 不再进入 newAgent（不会发起真实 HTTP 请求）；
//  2. 返回非 nil 的 HeadlessResult，避免 AnalyzeInspectionWithPrompt 等调用方 res.ModelName panic。
func TestRunHeadlessBreakerRejectsWithoutPanic(t *testing.T) {
	// 用独立 key，避免污染全局 DefaultBreaker 中其他测试的状态
	apiKey := "sk-breaker-reject-test-unique"
	key := llm.NewBreakerKey("test-provider", "test-model", apiKey)

	// 直接把全局熔断器打到 OPEN（本测试串行执行，安全）
	llm.DefaultBreaker.RecordFailure(key)
	llm.DefaultBreaker.RecordFailure(key)
	llm.DefaultBreaker.RecordFailure(key)
	llm.DefaultBreaker.RecordFailure(key)
	llm.DefaultBreaker.RecordFailure(key)
	if got := llm.DefaultBreaker.State(key); got != "open" {
		t.Fatalf("预热后应为 open，实际 %s", got)
	}

	h := &AgentHandler{config: &config.Config{AI: config.AIConfig{
		Enabled:      true,
		DefaultModel: "test",
		Models: []config.AIModelConfig{{
			Name:     "test",
			Provider: "test-provider",
			Model:    "test-model",
			APIKey:   apiKey,
		}},
	}}}

	if got := h.llmBreakerKey(); got != key {
		t.Fatalf("熔断键不一致: got=%v want=%v", got, key)
	}

	res, err := h.RunHeadless(context.Background(), "test", time.Second)
	var be *llm.BreakerError
	if !errors.As(err, &be) {
		t.Fatalf("熔断期间应返回 BreakerError，实际 %T: %v", err, err)
	}
	if res == nil {
		t.Fatal("熔断拒绝时必须返回非 nil 结果，否则调用方会 nil panic")
	}
	if res.ModelName != "test" {
		t.Fatalf("拒绝时也应填充模型名，实际 %q", res.ModelName)
	}
}

func TestLLMBreakerKeyHidesAPIKey(t *testing.T) {
	h := &AgentHandler{config: &config.Config{AI: config.AIConfig{
		Models: []config.AIModelConfig{{
			Name:     "m",
			Provider: "p",
			Model:    "gpt-x",
			APIKey:   "sk-super-secret-value",
		}},
	}}}
	key := h.llmBreakerKey()
	if key.Provider != "p" || key.Model != "gpt-x" {
		t.Fatalf("键应包含 provider/model，实际 %v", key)
	}
	if key.KeyID == "sk-super-secret-value" || len(key.KeyID) != 8 {
		t.Fatalf("KeyID 应为短哈希，实际 %q", key.KeyID)
	}
}
