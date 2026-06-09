package runner

import (
	"context"
	"sort"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

// Unloader is implemented by local runners that can evict their model from
// memory. Cloud runners do not implement it (they hold no local RAM).
type Unloader interface {
	Unload(ctx context.Context, model string) error
}

// UnloadFor frees the memory held by the local model described by cfg. It
// constructs the appropriate local runner and calls its Unload method. For
// cloud runners (which hold no local RAM) it is a no-op returning nil.
func UnloadFor(ctx context.Context, cfg config.AgentConfig) error {
	if !IsLocalRunner(cfg) {
		return nil
	}
	var u Unloader
	switch cfg.Runner {
	case "ollama":
		u = NewOllamaRunner(cfg.Model)
	case "lmstudio":
		u = NewLMStudioRunner(cfg.Model)
	case "mlx":
		u = NewMLXRunner(cfg.Model)
	default:
		// "opencode" with a local model has no API to unload through.
		return nil
	}
	return u.Unload(ctx, cfg.Model)
}

// Memory-sizing constants for the RAM→context heuristic. These are deliberately
// conservative: it is safer to under-estimate the context window than to load a
// model that exhausts RAM and swaps.
const (
	bytesPerGiB = 1 << 30

	// defaultWeightsBytes is the assumed weight size when model metadata is
	// unavailable (≈ a 7B model quantised to ~4-bit).
	defaultWeightsBytes = int64(4.5 * bytesPerGiB)

	// weightsOverhead scales the raw weight size to account for the loader's
	// working buffers around the weights themselves.
	weightsOverhead = 1.15

	// runtimeOverheadBytes is fixed memory the server needs regardless of the
	// context window (CUDA/Metal context, activations, framework overhead).
	runtimeOverheadBytes = int64(1 * bytesPerGiB)

	// kvBytesPerTokenBaseline is the approximate KV-cache cost per token for a
	// ~4.5 GiB (≈7B) model at fp16. Larger models scale this up linearly with
	// their weight size.
	kvBytesPerTokenBaseline = 512 * 1024 // 0.5 MiB

	minKVBytesPerToken = 128 * 1024       // 128 KiB floor
	maxKVBytesPerToken = 4 * 1024 * 1024  // 4 MiB ceiling
	minContextTokens   = 2048
	maxContextTokens   = 131072
)

// ContextTokensForRAM estimates the largest context window (in tokens) that the
// agent's model can run within ramGB gigabytes of memory. It prefers real model
// metadata (Ollama reports the on-disk weight size) and falls back to a
// heuristic when metadata is unavailable (LM Studio / oMLX). The result is
// clamped to a sane range and rounded down to a multiple of 1024.
//
// The conversion is intentionally approximate: an exact figure depends on the
// model's layer count, head dimensions and KV quantisation, which are not
// generally exposed by the local backends. Under-estimating is the safe failure
// mode, so the heuristic errs low.
func ContextTokensForRAM(cfg config.AgentConfig, ramGB float64) int {
	if ramGB <= 0 {
		return 0
	}
	weights := modelWeightBytes(cfg)
	if weights <= 0 {
		weights = defaultWeightsBytes
	}
	return ContextTokensForWeights(weights, ramGB)
}

// ContextTokensForWeights estimates the largest context window (in tokens) a
// model of the given weight size can run within ramGB gigabytes of memory. It
// is the network-free core of ContextTokensForRAM, exposed so callers that
// already know a model's weight size (e.g. a UI listing several models) can
// compute estimates without re-fetching metadata per keystroke. weightBytes <= 0
// falls back to the default estimate. The result is clamped and rounded down to
// a multiple of 1024.
func ContextTokensForWeights(weightBytes int64, ramGB float64) int {
	if ramGB <= 0 {
		return 0
	}
	if weightBytes <= 0 {
		weightBytes = defaultWeightsBytes
	}
	ramBytes := int64(ramGB * float64(bytesPerGiB))

	kvBudget := ramBytes - int64(float64(weightBytes)*weightsOverhead) - runtimeOverheadBytes
	if kvBudget <= 0 {
		return minContextTokens
	}

	// Scale per-token KV cost by model size relative to the 7B baseline.
	kvPerToken := int64(float64(kvBytesPerTokenBaseline) * float64(weightBytes) / float64(defaultWeightsBytes))
	if kvPerToken < minKVBytesPerToken {
		kvPerToken = minKVBytesPerToken
	}
	if kvPerToken > maxKVBytesPerToken {
		kvPerToken = maxKVBytesPerToken
	}

	tokens := int(kvBudget / kvPerToken)
	if tokens < minContextTokens {
		return minContextTokens
	}
	if tokens > maxContextTokens {
		tokens = maxContextTokens
	}
	return (tokens / 1024) * 1024
}

// modelWeightBytes returns the model's weight size in bytes using backend
// metadata, or 0 when unavailable.
func modelWeightBytes(cfg config.AgentConfig) int64 {
	switch cfg.Runner {
	case "ollama":
		return OllamaModelSizeBytes(cfg.Model)
	default:
		// LM Studio's /api/v0/models and oMLX's /v1/models do not report a
		// reliable on-disk size, so callers fall back to the default estimate.
		return 0
	}
}

// ApplyModelMemory fills each agent's MaxContextTokens from the global
// ModelMemory setting. An explicit per-agent MaxContextTokens always wins and is
// left untouched. When ModelMemory is disabled (Mode "" or "off") nothing
// changes.
//
// In "context" mode the configured token limit is applied verbatim to every
// agent. In "ram" mode the limit is derived per agent from its model via
// ContextTokensForRAM.
func ApplyModelMemory(cfg *config.Config) {
	if cfg == nil {
		return
	}
	mm := cfg.ModelMemory
	switch mm.Mode {
	case "ram", "context":
	default:
		return
	}
	for role, ac := range cfg.Agents {
		if ac.MaxContextTokens > 0 {
			continue // explicit per-agent override wins
		}
		var tokens int
		switch mm.Mode {
		case "context":
			tokens = mm.MaxContextTokens
		case "ram":
			tokens = ContextTokensForRAM(ac, mm.MaxRAMGB)
		}
		if tokens > 0 {
			ac.MaxContextTokens = tokens
			cfg.Agents[role] = ac
		}
	}
}

// LocalModel describes a locally-hosted model discovered on one of the local
// backends, with its weight size when the backend reports it.
type LocalModel struct {
	Runner      string // "ollama" | "lmstudio" | "mlx"
	Model       string
	WeightBytes int64 // 0 when the backend does not report a size (heuristic used)
}

// ListLocalModels enumerates models currently available across the local
// backends (Ollama, LM Studio, oMLX). Unreachable backends are skipped silently
// so the result reflects whatever is up right now. Ollama entries include their
// on-disk weight size; LM Studio and oMLX entries leave WeightBytes at 0 because
// those servers do not report a reliable size.
func ListLocalModels() []LocalModel {
	var out []LocalModel

	if sizes, err := OllamaInstalledWithSizes(); err == nil {
		names := make([]string, 0, len(sizes))
		for name := range sizes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			out = append(out, LocalModel{Runner: "ollama", Model: name, WeightBytes: sizes[name]})
		}
	}

	if models, err := LMStudioListInstalled(); err == nil {
		for _, name := range models {
			out = append(out, LocalModel{Runner: "lmstudio", Model: name})
		}
	}

	if models, err := MLXListInstalled(); err == nil {
		for _, name := range models {
			out = append(out, LocalModel{Runner: "mlx", Model: name})
		}
	}

	return out
}
