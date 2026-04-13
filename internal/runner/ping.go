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
	case "ollama":
		return pingOllama(ctx, model)
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
