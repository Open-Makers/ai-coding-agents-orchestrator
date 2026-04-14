package runner

import (
	"context"
	"fmt"
)

// MockRunner returns pre-programmed responses for testing.
type MockRunner struct {
	Responses []string
	MockUsage *TokenUsage // if set, attached to the Done token of each response
	idx       int
}

func (m *MockRunner) Complete(_ context.Context, _ CompletionRequest) (<-chan Token, error) {
	if m.idx >= len(m.Responses) {
		return nil, fmt.Errorf("mock: no more responses (called %d times)", m.idx+1)
	}
	resp := m.Responses[m.idx]
	m.idx++

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
