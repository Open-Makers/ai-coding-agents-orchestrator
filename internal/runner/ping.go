package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"time"
)

const pingTimeout = 5 * time.Second

// Ping performs a lightweight reachability check for the given provider and model.
// For CLI providers it verifies the binary exists; for Ollama it also checks the model is available.
func Ping(provider, model string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	switch provider {
	case "opencode", "":
		return pingBinary(ctx, "opencode")
	case "claude":
		return pingBinary(ctx, "claude")
	case "codex":
		return pingBinary(ctx, "codex")
	case "copilot":
		return pingBinary(ctx, "copilot")
	case "ollama":
		return pingOllama(ctx, model)
	case "mlx":
		return pingMLX(ctx, model)
	case "lmstudio":
		return pingHTTP(ctx, lmStudioBaseURL+"/v1/models", "lmstudio")
	default:
		return fmt.Errorf("unknown provider %q", provider)
	}
}

// ProviderReachable performs a lightweight check that the given provider's
// backend is usable: the CLI binary is in PATH, or the local HTTP server
// responds. It deliberately does NOT verify a specific model so the result
// can be cached up-front (e.g. to gray out unavailable providers in the
// setup UI). Returns nil when reachable, otherwise a descriptive error.
func ProviderReachable(provider string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	switch provider {
	case "opencode", "":
		return pingBinary(ctx, "opencode")
	case "claude":
		return pingBinary(ctx, "claude")
	case "codex":
		return pingBinary(ctx, "codex")
	case "copilot":
		return pingBinary(ctx, "copilot")
	case "ollama":
		return pingHTTP(ctx, ollamaBaseURL+"/api/tags", "ollama")
	case "mlx":
		return pingHTTP(ctx, mlxBaseURL+"/v1/models", "omlx")
	case "lmstudio":
		return pingHTTP(ctx, lmStudioBaseURL+"/v1/models", "lmstudio")
	default:
		return fmt.Errorf("unknown provider %q", provider)
	}
}

// pingBinary checks that a CLI binary is installed and runnable.
func pingBinary(ctx context.Context, name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s not found in PATH — is it installed?", name)
	}

	cmd := exec.CommandContext(ctx, name, "--version") // #nosec G204 — name is a compile-time constant from Ping()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s found but failed to run: %s", name, truncate(string(out), 120))
	}
	return nil
}

// pingOllama verifies the Ollama server is reachable and the model exists.
func pingOllama(ctx context.Context, model string) error {
	if model == "" {
		model = ollamaDefaultModel
	}

	payload, _ := json.Marshal(map[string]string{"name": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ollamaBaseURL+"/api/show", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("ollama: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not reachable at %s — is it running?", ollamaBaseURL)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("ollama model %q not found — run: ollama pull %s", model, model)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama returned HTTP %d for model %q", resp.StatusCode, model)
	}
	return nil
}

// pingMLX verifies the oMLX server is reachable and the model is loaded.
func pingMLX(ctx context.Context, model string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mlxBaseURL+"/v1/models", nil)
	if err != nil {
		return fmt.Errorf("omlx: %w", err)
	}
	if key := getOMLXAPIKey(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("omlx not reachable at %s — is oMLX running?", mlxBaseURL)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("omlx returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("omlx: decode /v1/models: %w", err)
	}
	if model == "" {
		return nil
	}
	for _, m := range result.Data {
		if m.ID == model {
			return nil
		}
	}
	return fmt.Errorf("omlx model %q not loaded in the running server", model)
}

// pingHTTP issues a GET against a local server endpoint and reports whether
// it answered with a 2xx status.
func pingHTTP(ctx context.Context, url, label string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	if label == "omlx" {
		if key := getOMLXAPIKey(); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s not reachable — is the server running?", label)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned HTTP %d", label, resp.StatusCode)
	}
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
