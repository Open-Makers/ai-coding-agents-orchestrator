package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

const defaultRingSize = 500
const defaultSubBuf = 100

type Bus struct {
	mu      sync.RWMutex
	ring    []Message
	head    int
	count   int
	subs    []chan Message
	logPath string
	logFile *os.File
}

func New() *Bus {
	return &Bus{
		ring: make([]Message, defaultRingSize),
	}
}

// SetLogPath opens the file for append-writing each published message as JSON.
func (b *Bus) SetLogPath(path string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // #nosec G304 -- path from workspace config
	if err != nil {
		return fmt.Errorf("bus: open log: %w", err)
	}
	if b.logFile != nil {
		_ = b.logFile.Close()
	}
	b.logPath = path
	b.logFile = f
	return nil
}

// Subscribe returns a buffered channel that receives all future messages.
func (b *Bus) Subscribe() <-chan Message {
	ch := make(chan Message, defaultSubBuf)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

// Publish adds msg to the ring buffer, fans out to subscribers, and optionally persists.
func (b *Bus) Publish(m Message) {
	b.mu.Lock()
	// write to ring
	b.ring[b.head%defaultRingSize] = m
	b.head++
	if b.count < defaultRingSize {
		b.count++
	}
	// persist
	if b.logFile != nil {
		data, _ := json.Marshal(m)
		_, _ = fmt.Fprintf(b.logFile, "%s\n", data)
	}
	// snapshot subs
	subs := make([]chan Message, len(b.subs))
	copy(subs, b.subs)
	b.mu.Unlock()

	// fan-out (non-blocking per sub)
	for _, ch := range subs {
		select {
		case ch <- m:
		default:
		}
	}
}

// Recent returns the last n messages from the ring (oldest first).
func (b *Bus) Recent(n int) []Message {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if n > b.count {
		n = b.count
	}
	if n == 0 {
		return nil
	}

	out := make([]Message, n)
	start := b.head - n
	for i := 0; i < n; i++ {
		out[i] = b.ring[(start+i+defaultRingSize)%defaultRingSize]
	}
	return out
}

// Close closes the log file if open.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.logFile != nil {
		_ = b.logFile.Close()
		b.logFile = nil
	}
}
