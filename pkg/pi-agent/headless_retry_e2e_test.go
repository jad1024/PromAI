package piagent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"PromAI/pkg/config"
	"PromAI/pkg/llm"

	"github.com/jay-y/pi/pkg/ai"
)

// TestRunHeadlessRetriesOnHTTP503 端到端验证：假 OpenAI 服务第一次返回 503、
// 第二次返回正常 SSE 流，RunHeadless 应自动重试并返回 AI 文本。
// 这是对外层重试接入是否真正生效的验证，不修改 pi SDK。
func TestRunHeadlessRetriesOnHTTP503(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":"temporary unavailable"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"retry-ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	// 生产环境由 NewAgentHandler 注册；测试直接构造需手动注册
	ai.RegisterBuiltInApiProviders()

	apiKey := "sk-e2e-retry-unique"
	h := &AgentHandler{config: &config.Config{AI: config.AIConfig{
		Enabled:      true,
		DefaultModel: "e2e",
		Models: []config.AIModelConfig{{
			Name:      "e2e",
			Provider:  "test-provider",
			Model:     "test-model",
			BaseURL:   srv.URL,
			APIKey:    apiKey,
			MaxTokens: 100,
		}},
	}}}

	// 用短退避的配置替换全局重试参数（测试串行，结束后恢复）
	origRetry := llm.DefaultRetry
	llm.DefaultRetry = llm.RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	defer func() { llm.DefaultRetry = origRetry }()

	// 熔断 key 隔离，避免与其他测试共享状态
	key := h.llmBreakerKey()
	if got := llm.DefaultBreaker.State(key); got != "closed" {
		t.Fatalf("测试前熔断应为 closed，实际 %s", got)
	}

	res, err := h.RunHeadless(context.Background(), "say hi", 10*time.Second)
	if err != nil {
		t.Fatalf("重试后应成功，实际 err=%v", err)
	}
	if res == nil || !strings.Contains(res.Text, "retry-ok") {
		t.Fatalf("应拿到重试后的 AI 文本，实际 res=%+v", res)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("应恰好请求 2 次（首次 503 + 重试成功），实际 %d", got)
	}
	if got := llm.DefaultBreaker.State(key); got != "closed" {
		t.Fatalf("成功后熔断应保持 closed，实际 %s", got)
	}
}
