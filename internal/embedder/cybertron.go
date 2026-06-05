package embedder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/nlpodyssey/cybertron/pkg/models/bert"
	"github.com/nlpodyssey/cybertron/pkg/tasks"
	"github.com/nlpodyssey/cybertron/pkg/tasks/textencoding"
)

// cybertronEmbedder runs sentence-transformer inference fully in-process
// using github.com/nlpodyssey/cybertron. The model is downloaded once into
// modelsDir (default ~/.orchestrator/cybertron-models) and then cached on
// disk; subsequent runs are fully offline.
//
// Loading is lazy: the heavy model load happens on first Embed call so that
// orchestrator startup stays fast for users who never trigger memory recall.
type cybertronEmbedder struct {
	modelName string
	modelsDir string

	mu    sync.Mutex
	model textencoding.Interface
	dim   int
}

func newCybertronEmbedder(modelName, modelsDir string) (*cybertronEmbedder, error) {
	if modelName == "" {
		modelName = textencoding.DefaultModel // sentence-transformers/all-MiniLM-L6-v2
	}
	if modelsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("%w: cybertron: resolve home: %v", ErrEmbedderUnavailable, err)
		}
		modelsDir = filepath.Join(home, ".orchestrator", "cybertron-models")
	}
	if err := os.MkdirAll(modelsDir, 0o750); err != nil {
		return nil, fmt.Errorf("%w: cybertron: mkdir models: %v", ErrEmbedderUnavailable, err)
	}
	return &cybertronEmbedder{modelName: modelName, modelsDir: modelsDir}, nil
}

func (c *cybertronEmbedder) ensureLoaded() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.model != nil {
		return nil
	}
	m, err := tasks.Load[textencoding.Interface](&tasks.Config{
		ModelsDir: c.modelsDir,
		ModelName: c.modelName,
	})
	if err != nil {
		return fmt.Errorf("%w: cybertron load %s: %v", ErrEmbedderUnavailable, c.modelName, err)
	}
	c.model = m
	return nil
}

func (c *cybertronEmbedder) Dim() int { return c.dim }

func (c *cybertronEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := c.ensureLoaded(); err != nil {
		return nil, err
	}
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		resp, err := c.model.Encode(ctx, t, int(bert.MeanPooling))
		if err != nil {
			return nil, fmt.Errorf("%w: cybertron encode: %v", ErrEmbedderRequest, err)
		}
		vec := resp.Vector.Data().F32()
		cp := make([]float32, len(vec))
		copy(cp, vec)
		if c.dim == 0 {
			c.dim = len(cp)
		}
		out = append(out, cp)
	}
	return out, nil
}
