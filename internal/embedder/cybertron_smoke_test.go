//go:build cybertronsmoke

package embedder

import (
	"context"
	"testing"
)

// TestCybertronSmoke does a real model download + encode. Skipped by default
// (build tag cybertronsmoke). Run with:
//
//	go test ./internal/embedder -tags=cybertronsmoke -run TestCybertronSmoke -v -timeout=10m
func TestCybertronSmoke(t *testing.T) {
	e, err := newCybertronEmbedder("", t.TempDir())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	vecs, err := e.Embed(context.Background(), []string{"hello world"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		t.Fatalf("bad output: %v", vecs)
	}
	t.Logf("dim=%d first3=%v", len(vecs[0]), vecs[0][:3])
}
