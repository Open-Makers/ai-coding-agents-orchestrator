package cpulimit

import (
	"runtime"
	"strings"
	"testing"
)

func TestAllowedCoresDefault(t *testing.T) {
	// Before Apply is called, AllowedCores returns NumCPU.
	// Note: since once.Do is package-level, this test must run before
	// any test that calls Apply. In practice we test via a fresh binary.
	cores := runtime.NumCPU()
	if AllowedCores() != cores && allowedCores == 0 {
		t.Errorf("AllowedCores() before Apply = %d, want %d", AllowedCores(), cores)
	}
}

func TestEnvOverrides(t *testing.T) {
	overrides := EnvOverrides()
	if len(overrides) == 0 {
		t.Fatal("EnvOverrides returned empty slice")
	}

	hasGOMAXPROCS := false
	hasOllama := false
	for _, env := range overrides {
		if strings.HasPrefix(env, "GOMAXPROCS=") {
			hasGOMAXPROCS = true
		}
		if strings.HasPrefix(env, "OLLAMA_NUM_THREADS=") {
			hasOllama = true
		}
	}
	if !hasGOMAXPROCS {
		t.Error("EnvOverrides missing GOMAXPROCS")
	}
	if !hasOllama {
		t.Error("EnvOverrides missing OLLAMA_NUM_THREADS")
	}
}
