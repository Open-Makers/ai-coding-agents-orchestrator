package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

func TestContextTokensForRAM_ScalesWithRAM(t *testing.T) {
	cfg := config.AgentConfig{Runner: "lmstudio", Model: "x"} // no metadata → default weights

	low := ContextTokensForRAM(cfg, 8)
	high := ContextTokensForRAM(cfg, 32)

	if low <= 0 || high <= 0 {
		t.Fatalf("expected positive token estimates, got low=%d high=%d", low, high)
	}
	if high <= low {
		t.Errorf("more RAM should yield a larger context: low(8GB)=%d high(32GB)=%d", low, high)
	}
	if low%1024 != 0 || high%1024 != 0 {
		t.Errorf("estimates should be rounded to 1024: low=%d high=%d", low, high)
	}
	if high > maxContextTokens {
		t.Errorf("estimate %d exceeds clamp %d", high, maxContextTokens)
	}
}

func TestContextTokensForRAM_ZeroRAM(t *testing.T) {
	if got := ContextTokensForRAM(config.AgentConfig{Runner: "ollama"}, 0); got != 0 {
		t.Errorf("zero RAM should yield 0, got %d", got)
	}
}

func TestContextTokensForRAM_TinyRAMFloor(t *testing.T) {
	// RAM smaller than the weights+overhead must not go negative; it floors.
	if got := ContextTokensForRAM(config.AgentConfig{Runner: "lmstudio"}, 1); got != minContextTokens {
		t.Errorf("tiny RAM should floor at %d, got %d", minContextTokens, got)
	}
}

func TestApplyModelMemory_ContextMode(t *testing.T) {
	cfg := &config.Config{
		ModelMemory: config.ModelMemoryConfig{Mode: "context", MaxContextTokens: 4096},
		Agents: map[string]config.AgentConfig{
			"pm":    {Runner: "ollama", Model: "a"},
			"coder": {Runner: "ollama", Model: "b", MaxContextTokens: 9000}, // explicit override wins
		},
	}
	ApplyModelMemory(cfg)

	if cfg.Agents["pm"].MaxContextTokens != 4096 {
		t.Errorf("pm: want 4096, got %d", cfg.Agents["pm"].MaxContextTokens)
	}
	if cfg.Agents["coder"].MaxContextTokens != 9000 {
		t.Errorf("coder override should be preserved, got %d", cfg.Agents["coder"].MaxContextTokens)
	}
}

func TestApplyModelMemory_RAMMode(t *testing.T) {
	cfg := &config.Config{
		ModelMemory: config.ModelMemoryConfig{Mode: "ram", MaxRAMGB: 16},
		Agents:      map[string]config.AgentConfig{"pm": {Runner: "lmstudio", Model: "a"}},
	}
	ApplyModelMemory(cfg)

	if cfg.Agents["pm"].MaxContextTokens <= 0 {
		t.Errorf("ram mode should derive a positive context, got %d", cfg.Agents["pm"].MaxContextTokens)
	}
}

func TestApplyModelMemory_DisabledNoop(t *testing.T) {
	for _, mode := range []string{"", "off"} {
		cfg := &config.Config{
			ModelMemory: config.ModelMemoryConfig{Mode: mode, MaxContextTokens: 4096},
			Agents:      map[string]config.AgentConfig{"pm": {Runner: "ollama"}},
		}
		ApplyModelMemory(cfg)
		if cfg.Agents["pm"].MaxContextTokens != 0 {
			t.Errorf("mode %q should be a no-op, got %d", mode, cfg.Agents["pm"].MaxContextTokens)
		}
	}
}

func TestUnloadFor_CloudIsNoop(t *testing.T) {
	// Cloud runners hold no local RAM; UnloadFor must not error or hit network.
	if err := UnloadFor(context.Background(), config.AgentConfig{Runner: "claude", Model: "x"}); err != nil {
		t.Errorf("cloud unload should be no-op, got %v", err)
	}
}

func TestOllamaUnload_SendsKeepAliveZero(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/generate" {
			t.Errorf("unexpected path %q", req.URL.Path)
		}
		_ = json.NewDecoder(req.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := &OllamaRunner{Model: "qwen", BaseURL: srv.URL}
	if err := r.Unload(context.Background(), "qwen"); err != nil {
		t.Fatalf("unload: %v", err)
	}
	if got["model"] != "qwen" {
		t.Errorf("model: want qwen, got %v", got["model"])
	}
	if ka, ok := got["keep_alive"].(float64); !ok || ka != 0 {
		t.Errorf("keep_alive: want 0, got %v", got["keep_alive"])
	}
}

func TestOllamaComplete_SetsNumCtx(t *testing.T) {
	var got ollamaChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewDecoder(req.Body).Decode(&got)
		b, _ := json.Marshal(ollamaChatResponse{
			Message: ollamaChatMessage{Role: "assistant", Content: "ok"},
			Done:    true,
		})
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	r := &OllamaRunner{Model: "qwen", BaseURL: srv.URL, NumCtx: 4096}
	ch, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []ConvMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	for range ch {
	}
	if got.Options.NumCtx != 4096 {
		t.Errorf("num_ctx: want 4096, got %d", got.Options.NumCtx)
	}
}
