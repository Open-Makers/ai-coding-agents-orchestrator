package bus

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
)

// maxSocketMessageSize caps the size of a single JSON message on the socket
// to prevent denial-of-service via memory exhaustion.
const maxSocketMessageSize = 10 << 20 // 10 MiB

// ServeUnix starts a Unix domain socket server at path.
// Each connecting client may:
//   - publish messages to this bus (sends JSON-encoded Message objects)
//   - receive all bus messages (the server fans out to every connection)
//
// The server runs until the provided stop channel is closed or ctx is cancelled.
// Call os.Remove(path) after to clean up.
func (b *Bus) ServeUnix(path string, stop <-chan struct{}) error {
	// Remove stale socket if present.
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("bus: unix listen: %w", err)
	}
	// Restrict socket to owner only to prevent local privilege escalation.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = l.Close()
		return fmt.Errorf("bus: chmod socket: %w", err)
	}

	go func() {
		<-stop
		_ = l.Close()
	}()

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return // listener closed
			}
			go b.handleSocketConn(conn)
		}
	}()
	return nil
}

// handleSocketConn handles a single client connection bidirectionally:
//   - incoming JSON Messages are published to the bus
//   - outgoing: this connection is subscribed and receives all bus events
//
// The writer goroutine is stopped via readerDone when the reader exits,
// preventing a goroutine leak when the client disconnects.
func (b *Bus) handleSocketConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	sub := b.Subscribe()
	readerDone := make(chan struct{})

	// Writer goroutine: fan bus messages to conn.
	// Exits when readerDone is closed (client disconnected) or encode fails.
	go func() {
		enc := json.NewEncoder(conn)
		for {
			select {
			case <-readerDone:
				return
			case m, ok := <-sub:
				if !ok {
					return
				}
				if err := enc.Encode(m); err != nil {
					return
				}
			}
		}
	}()

	// Reader: publish incoming messages from conn to bus.
	dec := json.NewDecoder(conn)
	for {
		var m Message
		if err := dec.Decode(&m); err != nil {
			close(readerDone) // signal writer to stop
			return
		}
		// Reject oversized payloads by checking serialized size.
		if raw, err := json.Marshal(m); err == nil && int64(len(raw)) > maxSocketMessageSize {
			close(readerDone)
			return
		}
		b.Publish(m)
	}
}

// BusClient is a client connected to a remote bus via Unix socket.
type BusClient struct {
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder
}

// DialUnix connects to a bus server at the given Unix socket path.
func DialUnix(path string) (*BusClient, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("bus: dial unix %s: %w", path, err)
	}
	return &BusClient{
		conn: conn,
		enc:  json.NewEncoder(conn),
		dec:  json.NewDecoder(conn),
	}, nil
}

// Publish sends a message to the remote bus.
func (c *BusClient) Publish(m Message) error {
	return c.enc.Encode(m)
}

// Subscribe returns a channel that receives messages from the remote bus.
func (c *BusClient) Subscribe() <-chan Message {
	ch := make(chan Message, 100)
	go func() {
		defer close(ch)
		for {
			var m Message
			if err := c.dec.Decode(&m); err != nil {
				return
			}
			ch <- m
		}
	}()
	return ch
}

// Close closes the connection.
func (c *BusClient) Close() error {
	return c.conn.Close()
}
