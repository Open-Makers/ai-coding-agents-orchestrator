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

// NewMemoryEmbedder constructs the embedder used by the project-memory
// subsystem. Backends:
//
//   - "openai"     — OpenAI-compatible HTTP POST /v1/embeddings (also works
//     against Voyage, Together, vLLM, llama.cpp `--server`, LM Studio…).
//   - "ollama"     — local Ollama server at base_url.
//   - "cybertron"  — placeholder: pure-Go transformers (planned; returns
//     ErrEmbedderUnavailable so callers fall back to BM25 cleanly).
//   - ""           — defaults to "openai" if a base_url is set, otherwise
//     "ollama" (preserving existing behaviour for users with Ollama).
func NewMemoryEmbedder(mc config.MemoryConfig) (Embedder, error) {
	backend := strings.ToLower(mc.Embedder)
	if backend == "" {
		if mc.EmbedderBaseURL != "" {
			backend = "openai"
		} else {
			backend = "ollama"
		}
	}
	switch backend {
	case "openai":
		base := mc.EmbedderBaseURL
		if base == "" {
			base = "https://api.openai.com"
		}
		model := mc.EmbedderModel
		if model == "" {
			model = "text-embedding-3-small"
		}
		return &openaiEmbedder{
			baseURL: strings.TrimRight(base, "/"),
			model:   model,
			apiKey:  mc.EmbedderAPIKey,
			http:    &http.Client{Timeout: 30 * time.Second},
		}, nil
	case "ollama":
		base := mc.EmbedderBaseURL
		if base == "" {
			base = "http://127.0.0.1:11434"
		}
		model := mc.EmbedderModel
		if model == "" {
			model = "nomic-embed-text"
		}
		return &ollamaEmbedder{baseURL: base, model: model, http: &http.Client{Timeout: 30 * time.Second}}, nil
	case "cybertron":
		// Pure-Go transformers via github.com/nlpodyssey/cybertron.
		// First call downloads the model into mc.EmbedderModelsDir (default
		// ~/.orchestrator/cybertron-models); subsequent runs are offline.
		return newCybertronEmbedder(mc.EmbedderModel, mc.EmbedderModelsDir)
	default:
		return nil, fmt.Errorf("%w: unknown memory embedder %q", ErrEmbedderUnavailable, mc.Embedder)
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
