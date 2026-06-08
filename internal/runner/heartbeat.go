package runner

import (
	"fmt"
	"time"
)

// defaultHeartbeatInterval is how long the stream may stay silent before a
// keep-alive notice is emitted. CLI agents (copilot, claude) often do long
// stretches of silent tool work; without this the UI looks frozen.
const defaultHeartbeatInterval = 8 * time.Second

// withHeartbeat decorates a Token stream so that, whenever no token arrives for
// interval, a display-only Reasoning "still working" notice is emitted. Real
// tokens reset the idle timer and pass through unchanged. The returned channel
// is closed when in is closed. This is runner-agnostic and does not interfere
// with the underlying blocking reads.
func withHeartbeat(in <-chan Token, interval time.Duration) <-chan Token {
	if interval <= 0 {
		interval = defaultHeartbeatInterval
	}
	out := make(chan Token, 16)
	go func() {
		defer close(out)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case tok, ok := <-in:
				if !ok {
					return
				}
				out <- tok
				if tok.Done {
					return
				}
				// Any real activity resets the idle timer so the heartbeat
				// only fires during genuine silence.
				ticker.Reset(interval)
			case <-ticker.C:
				out <- Token{Reasoning: fmt.Sprintf("⏳ still working… (%s)\n", shortElapsed(time.Since(start)))}
			}
		}
	}()
	return out
}

// shortElapsed renders a compact elapsed string like "12s" or "3m05s".
func shortElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", m, s)
}
