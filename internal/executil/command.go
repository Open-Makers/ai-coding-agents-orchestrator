package executil

import (
	"context"
	"os"
	"os/exec"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/cpulimit"
)

// Command wraps exec.Command for internal use where arguments are
// built programmatically from trusted sources (config values, constants).
// Centralises the gosec G204 suppression to a single location.
// CPU limit environment variables are injected automatically.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...) // #nosec G204 -- callers pass internally-built args only
	cmd.Env = cpuLimitedEnv(cmd.Env)
	return cmd
}

// CommandContext wraps exec.CommandContext for internal use where arguments
// are built programmatically from trusted sources.
// CPU limit environment variables are injected automatically.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- callers pass internally-built args only
	cmd.Env = cpuLimitedEnv(cmd.Env)
	return cmd
}

// cpuLimitedEnv merges CPU limit overrides into the given env slice.
// If env is nil, os.Environ() is used as the base.
func cpuLimitedEnv(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	overrides := cpulimit.EnvOverrides()
	return mergeEnv(env, overrides)
}

// mergeEnv merges override KEY=VALUE pairs into base, replacing existing keys.
func mergeEnv(base, overrides []string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	overrideKeys := make(map[string]string, len(overrides))
	for _, ov := range overrides {
		if k, _, ok := splitEnvVar(ov); ok {
			overrideKeys[k] = ov
		}
	}
	for _, entry := range base {
		if k, _, ok := splitEnvVar(entry); ok {
			if _, replaced := overrideKeys[k]; replaced {
				continue // will be added from overrides
			}
		}
		result = append(result, entry)
	}
	result = append(result, overrides...)
	return result
}

// splitEnvVar splits "KEY=VALUE" into key and value.
func splitEnvVar(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}
