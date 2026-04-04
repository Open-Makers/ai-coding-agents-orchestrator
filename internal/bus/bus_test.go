package bus

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPublishSubscribe(t *testing.T) {
	b := New()
	ch := b.Subscribe()

	msg := NewMessage(RolePlanner, RoleCoder, MsgRequest, "hello")
	b.Publish(msg)

	select {
	case got := <-ch:
		if got.From != RolePlanner {
			t.Errorf("expected from=planner, got %q", got.From)
		}
		if got.Payload != "hello" {
			t.Errorf("unexpected payload: %v", got.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	b := New()

	for i := 0; i < defaultRingSize+10; i++ {
		b.Publish(NewMessage(RoleSystem, "", MsgEvent, i))
	}

	recent := b.Recent(defaultRingSize)
	if len(recent) != defaultRingSize {
		t.Errorf("expected %d messages, got %d", defaultRingSize, len(recent))
	}
}

func TestRecent_FewerThanN(t *testing.T) {
	b := New()
	b.Publish(NewMessage(RoleSystem, "", MsgEvent, "a"))
	b.Publish(NewMessage(RoleSystem, "", MsgEvent, "b"))

	recent := b.Recent(10)
	if len(recent) != 2 {
		t.Errorf("expected 2 messages, got %d", len(recent))
	}
}

func TestConcurrentPublish(t *testing.T) {
	b := New()
	_ = b.Subscribe()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			b.Publish(NewMessage(RoleSystem, "", MsgEvent, n))
		}(i)
	}
	wg.Wait()

	recent := b.Recent(100)
	if len(recent) != 100 {
		t.Errorf("expected 100 messages, got %d", len(recent))
	}
}

func TestLogFile(t *testing.T) {
	b := New()
	logPath := filepath.Join(t.TempDir(), "runlog.jsonl")

	if err := b.SetLogPath(logPath); err != nil {
		t.Fatal(err)
	}

	b.Publish(NewMessage(RolePlanner, "", MsgEvent, "logged"))
	b.Close()

	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("log file not created: %v", err)
	}
	if fi.Size() == 0 {
		t.Error("log file is empty")
	}
}
