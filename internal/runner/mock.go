package runner

import (
	"context"
	"fmt"
)

// MockRunner returns pre-programmed responses for testing.
type MockRunner struct {
	Responses []string
	MockUsage *TokenUsage // if set, attached to the Done token of each response
	// Requests captures every CompletionRequest passed to Complete, in order.
	// Useful for asserting on prompt construction in tests.
	Requests []CompletionRequest
	idx      int
}

func (m *MockRunner) Complete(_ context.Context, req CompletionRequest) (<-chan Token, error) {
	if m.idx >= len(m.Responses) {
		return nil, fmt.Errorf("mock: no more responses (called %d times)", m.idx+1)
	}
	resp := m.Responses[m.idx]
	m.idx++
	m.Requests = append(m.Requests, req)

	ch := make(chan Token, 2)
	go func() {
		ch <- Token{Text: resp}
		ch <- Token{Done: true, Usage: m.MockUsage}
		close(ch)
	}()
	return ch, nil
}

// Reset resets the response index so the mock can be reused.
func (m *MockRunner) Reset() {
	m.idx = 0
}
