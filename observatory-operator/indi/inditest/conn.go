package inditest

import (
	"io"
	"net"
	"sync"
)

// Conn is the server's end of one client connection. A net.Pipe has no
// buffer, so a write blocks until the client reads. A fake server that
// wrote while it held its own lock could deadlock with a client that
// waits for that lock. So Send only queues the text, and a goroutine
// writes the queue to the connection.
type Conn struct {
	net.Conn
	mu    sync.Mutex
	queue []string
	ready chan struct{}
}

// NewConn returns a Conn over conn and starts its writer goroutine.
func NewConn(conn net.Conn) *Conn {
	c := &Conn{Conn: conn, ready: make(chan struct{}, 1)}
	go c.write()
	return c
}

// Send queues text to be written to the client.
func (c *Conn) Send(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queue = append(c.queue, text)
	select {
	case c.ready <- struct{}{}:
	default:
	}
}

// write writes the queued text until a write fails, which happens once
// either end has closed the pipe. Close sends an empty text so that
// write wakes, fails, and returns.
func (c *Conn) write() {
	for range c.ready {
		c.mu.Lock()
		queue := c.queue
		c.queue = nil
		c.mu.Unlock()
		for _, text := range queue {
			if _, err := io.WriteString(c.Conn, text); err != nil {
				return
			}
		}
	}
}

// Close closes the connection and stops the writer goroutine.
func (c *Conn) Close() error {
	err := c.Conn.Close()
	c.Send("")
	return err
}
