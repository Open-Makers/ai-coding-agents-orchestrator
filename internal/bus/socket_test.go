package bus

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// tempSock returns a short socket path under /tmp to stay within the 104-byte
// macOS limit for Unix socket paths.
func tempSock(t *testing.T, suffix string) string {
	t.Helper()
	path := fmt.Sprintf("/tmp/orch-test-%s.sock", suffix)
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

func startServer(t *testing.T, b *Bus, path string) chan struct{} {
	t.Helper()
	stop := make(chan struct{})
	if err := b.ServeUnix(path, stop); err != nil {
		t.Fatalf("ServeUnix: %v", err)
	}
	time.Sleep(10 * time.Millisecond) // let Accept goroutine start
	return stop
}

func TestServeDialUnix_RoundTrip(t *testing.T) {
	b := New()
	sock := tempSock(t, "rt")
	stop := startServer(t, b, sock)
	defer close(stop)

	client, err := DialUnix(sock)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	defer func() { _ = client.Close() }()

	incoming := client.Subscribe()
	time.Sleep(10 * time.Millisecond) // let subscription be registered

	want := NewMessage(RolePlanner, RoleCoder, MsgEvent, "hello")
	b.Publish(want)

	select {
	case got := <-incoming:
		if got.ID != want.ID {
			t.Errorf("ID mismatch: got %q, want %q", got.ID, want.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message from bus")
	}
}

func TestDialPublish_ForwardsToServerBus(t *testing.T) {
	b := New()
	sock := tempSock(t, "pub")
	stop := startServer(t, b, sock)
	defer close(stop)

	serverSub := b.Subscribe()

	client, err := DialUnix(sock)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	defer func() { _ = client.Close() }()

	time.Sleep(10 * time.Millisecond) // let connection establish

	want := NewMessage(RoleCoder, RoleTester, MsgEvent, "from-client")
	if err := client.Publish(want); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-serverSub:
			if got.ID == want.ID {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for client message on server bus")
		}
	}
}

func TestHandleSocketConn_NoGoroutineLeak(t *testing.T) {
	b := New()
	sock := tempSock(t, "leak")
	stop := startServer(t, b, sock)
	defer close(stop)

	client, err := DialUnix(sock)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	_ = client.Close() // close immediately

	// Publish after close — writer goroutine must not block indefinitely.
	b.Publish(NewMessage(RoleSystem, "", MsgEvent, "post-close"))
	time.Sleep(50 * time.Millisecond) // give handler time to notice the close
}
