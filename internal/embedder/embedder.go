// Package embedder produces dense vector embeddings for text snippets.
// The default backend is Ollama's /api/embeddings HTTP endpoint.
package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

// Sentinel errors for typed handling by callers.
var (
	ErrEmbedderUnavailable = errors.New("embedder unavailable")
	ErrEmbedderRequest     = errors.New("embedder request failed")
)

// Embedder turns a batch of texts into a batch of vectors.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dim() int
}

// New creates an Embedder from configuration. Currently only "ollama" is
// supported; other values fall back to a no-op embedder that returns
// ErrEmbedderUnavailable on use.
func New(cfg config.SemanticIndexConfig) (Embedder, error) {
	switch strings.ToLower(cfg.Embedder) {
	case "", "ollama":
		base := cfg.BaseURL
		if base == "" {
			base = "http://127.0.0.1:11434"
		}
		model := cfg.Model
		if model == "" {
			model = "nomic-embed-text"
		}
		return &ollamaEmbedder{baseURL: base, model: model, http: &http.Client{Timeout: 30 * time.Second}}, nil
	default:
		return nil, fmt.Errorf("%w: unknown embedder %q", ErrEmbedderUnavailable, cfg.Embedder)
	}
}

type ollamaEmbedder struct {
	baseURL string
	model   string
	http    *http.Client
	dim     int
}

type ollamaReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaResp struct {
	Embedding []float32 `json:"embedding"`
}

func (o *ollamaEmbedder) Dim() int { return o.dim }

// Embed sends one HTTP request per text. Ollama's embeddings endpoint does
// not (currently) support batching, so requests are issued sequentially.
func (o *ollamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		v, err := o.embedOne(ctx, t)
		if err != nil {
			return nil, err
		}
		if o.dim == 0 {
			o.dim = len(v)
		}
		out = append(out, v)
	}
	return out, nil
}

func (o *ollamaEmbedder) embedOne(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(ollamaReq{Model: o.model, Prompt: text})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEmbedderUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrEmbedderRequest, resp.StatusCode)
	}

	var parsed ollamaResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrEmbedderRequest, err)
	}
	if len(parsed.Embedding) == 0 {
		return nil, fmt.Errorf("%w: empty embedding", ErrEmbedderRequest)
	}
	return parsed.Embedding, nil
}
