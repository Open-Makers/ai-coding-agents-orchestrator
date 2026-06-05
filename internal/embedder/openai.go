package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// openaiEmbedder calls any service that implements OpenAI's POST /v1/embeddings.
//
// Compatible with: api.openai.com, Together AI, Voyage AI (via /v1/embeddings),
// LM Studio, llama.cpp `--server`, vLLM, and locally-hosted gateways.
type openaiEmbedder struct {
	baseURL string
	model   string
	apiKey  string
	http    *http.Client
	dim     int
}

func (o *openaiEmbedder) Dim() int  { return o.dim }
func (o *openaiEmbedder) Name() string {
	return "openai:" + o.model
}

type openaiReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openaiResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed batches up to 100 texts per request (the OpenAI documented limit).
func (o *openaiEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	const batchSize = 100
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := o.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (o *openaiEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(openaiReq{Model: o.model, Input: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEmbedderUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrEmbedderRequest, resp.StatusCode)
	}
	var parsed openaiResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrEmbedderRequest, err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("%w: got %d embeddings for %d inputs", ErrEmbedderRequest, len(parsed.Data), len(texts))
	}
	vecs := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("%w: empty embedding for input %d", ErrEmbedderRequest, i)
		}
		if o.dim == 0 {
			o.dim = len(d.Embedding)
		}
		vecs[i] = d.Embedding
	}
	return vecs, nil
}
