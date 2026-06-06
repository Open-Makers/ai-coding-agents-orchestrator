package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// This file is the evaluation for the scoped-context spike (issue ...-5vk).
// It compares the current whole-file rendering (expandWithGraph, targets==nil)
// against symbol-scoped rendering (buildScopedSourceContext with per-file
// symbol targets) on the same candidate file set, measuring input tokens and
// recall of the task-relevant declarations. It is deterministic (no embedder,
// no git, no network) so the value hypothesis can be checked in CI.
//
// Hypothesis: symbol scoping clearly reduces input tokens while preserving (or
// improving, under a tight budget) recall of the declarations a task touches.

// spikeRepo writes a small multi-package repo. Each candidate file pairs a
// large, high-priority but task-irrelevant exported function with a small
// task-relevant function placed after it. This is the realistic failure mode
// for whole-file rendering: under a per-file budget the big irrelevant decl is
// kept (it sorts first) and the relevant decl is dropped, whereas symbol
// scoping renders only the relevant decl. Returns the root, the ordered
// candidate file list, and the ground-truth targets (file -> relevant names).
func spikeRepo(t *testing.T) (root string, files []string, truth map[string][]string) {
	t.Helper()
	root = t.TempDir()

	// A big exported function body that dominates a file's size but is
	// irrelevant to the task. ~9 KB so it exceeds the per-file budget alone.
	big := func(name string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "// %s is unrelated to the task.\nfunc %s() string {\n", name, name)
		for i := 0; i < 120; i++ {
			fmt.Fprintf(&b, "\t_ = %q\n", strings.Repeat("padding-", 8))
		}
		b.WriteString("\treturn \"\"\n}\n")
		return b.String()
	}

	write := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("go.mod", "module example.com/spike\n\ngo 1.22\n")

	// Each file: huge irrelevant decl FIRST, then the small relevant decl.
	write("internal/b/b.go", "package b\n\n"+big("HugeB")+"\n"+
		"// TaskB is the task-relevant declaration.\nfunc TaskB(x int) int { return x + 1 }\n")
	write("internal/c/c.go", "package c\n\n"+big("HugeC")+"\n"+
		"// TaskC is the task-relevant declaration.\nfunc TaskC() string { return \"c\" }\n")
	write("internal/d/d.go", "package d\n\n"+big("HugeD")+"\n"+
		"// TaskD is the task-relevant declaration.\nfunc TaskD() bool { return true }\n")

	files = []string{"internal/b/b.go", "internal/c/c.go", "internal/d/d.go"}
	truth = map[string][]string{
		"internal/b/b.go": {"TaskB"},
		"internal/c/c.go": {"TaskC"},
		"internal/d/d.go": {"TaskD"},
	}
	return root, files, truth
}

// relevantSignatures are unique substrings that must appear in rendered context
// for the task-relevant declarations to count as "recalled".
var relevantSignatures = []string{
	"func TaskB(x int) int",
	"func TaskC() string",
	"func TaskD() bool",
}

func recall(out string) float64 {
	hit := 0
	for _, sig := range relevantSignatures {
		if strings.Contains(out, sig) {
			hit++
		}
	}
	return float64(hit) / float64(len(relevantSignatures))
}

type spikeResult struct {
	tokens int
	chars  int
	recall float64
	dur    time.Duration
}

func measure(label, root string, files []string, targets map[string][]string, total, perFile int) spikeResult {
	start := time.Now()
	out := buildScopedSourceContext(label, root, files, nil, targets, total, perFile, 0)
	return spikeResult{
		tokens: tokenutil.EstimateTokens(out),
		chars:  len(out),
		recall: recall(out),
		dur:    time.Since(start),
	}
}

func TestSpike_ScopedContext_GenerousBudget(t *testing.T) {
	root, files, truth := spikeRepo(t)

	const total, perFile = 40000, 6000
	whole := measure("spike-whole", root, files, nil, total, perFile)
	scoped := measure("spike-scoped", root, files, truth, total, perFile)

	t.Logf("generous budget (total=%d perFile=%d):", total, perFile)
	t.Logf("  whole : %5d tok  %6d chars  recall=%.2f  %s", whole.tokens, whole.chars, whole.recall, whole.dur)
	t.Logf("  scoped: %5d tok  %6d chars  recall=%.2f  %s", scoped.tokens, scoped.chars, scoped.recall, scoped.dur)
	t.Logf("  token reduction: %.1f%%", 100*(1-float64(scoped.tokens)/float64(whole.tokens)))

	if scoped.recall < 1.0 {
		t.Errorf("scoped recall = %.2f, want 1.0 (all relevant decls present under generous budget)", scoped.recall)
	}
	if scoped.recall < whole.recall {
		t.Errorf("scoped recall %.2f worse than whole %.2f", scoped.recall, whole.recall)
	}
	if scoped.tokens*2 > whole.tokens {
		t.Errorf("expected scoped tokens (%d) to be < 50%% of whole (%d)", scoped.tokens, whole.tokens)
	}
}

func TestSpike_ScopedContext_TightBudget(t *testing.T) {
	root, files, truth := spikeRepo(t)

	// Budget large enough for ~one whole big file but not all three.
	const total, perFile = 7000, 6000
	whole := measure("spike-whole", root, files, nil, total, perFile)
	scoped := measure("spike-scoped", root, files, truth, total, perFile)

	t.Logf("tight budget (total=%d perFile=%d):", total, perFile)
	t.Logf("  whole : %5d tok  %6d chars  recall=%.2f", whole.tokens, whole.chars, whole.recall)
	t.Logf("  scoped: %5d tok  %6d chars  recall=%.2f", scoped.tokens, scoped.chars, scoped.recall)

	// Under a tight budget the whole-file approach spends its budget on large
	// irrelevant bodies and drops later files, so it cannot recall everything.
	// Scoped rendering fits all relevant decls and must do at least as well.
	if scoped.recall < whole.recall {
		t.Errorf("scoped recall %.2f worse than whole %.2f under tight budget", scoped.recall, whole.recall)
	}
	if scoped.recall < 1.0 {
		t.Errorf("scoped recall = %.2f, want 1.0 (relevant decls are tiny and should all fit)", scoped.recall)
	}
	if scoped.tokens >= whole.tokens {
		t.Errorf("expected scoped tokens (%d) < whole tokens (%d)", scoped.tokens, whole.tokens)
	}
}
