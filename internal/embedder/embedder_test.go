package embedder

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

func TestOllamaEmbedder_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ollamaResp{Embedding: []float32{0.1, 0.2, 0.3}})
	}))
	defer srv.Close()

	emb, err := New(config.SemanticIndexConfig{Embedder: "ollama", Model: "x", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	out, err := emb.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || len(out[0]) != 3 {
		t.Errorf("unexpected output shape: %v", out)
	}
	if emb.Dim() != 3 {
		t.Errorf("Dim = %d, want 3", emb.Dim())
	}
}

func TestOllamaEmbedder_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	emb, _ := New(config.SemanticIndexConfig{Embedder: "ollama", BaseURL: srv.URL})
	_, err := emb.Embed(context.Background(), []string{"x"})
	if !errors.Is(err, ErrEmbedderRequest) {
		t.Errorf("expected ErrEmbedderRequest, got %v", err)
	}
}

func TestOllamaEmbedder_Unreachable(t *testing.T) {
	emb, _ := New(config.SemanticIndexConfig{Embedder: "ollama", BaseURL: "http://127.0.0.1:1"})
	_, err := emb.Embed(context.Background(), []string{"x"})
	if !errors.Is(err, ErrEmbedderUnavailable) {
		t.Errorf("expected ErrEmbedderUnavailable, got %v", err)
	}
}

func TestNew_UnknownEmbedder(t *testing.T) {
	_, err := New(config.SemanticIndexConfig{Embedder: "voyage"})
	if !errors.Is(err, ErrEmbedderUnavailable) {
		t.Errorf("expected ErrEmbedderUnavailable, got %v", err)
	}
}
