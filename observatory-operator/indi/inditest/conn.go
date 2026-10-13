package inditest

import (
	"io"
	"net"
	"sync"
)

// Conn is the server's end of one client connection. It queues what
// the server writes, the way a socket's send buffer does, and a
// goroutine of its own writes the queue to the pipe. A net.Pipe has no
// buffer, so a write blocks until the client reads, and a fake that
// writes while it holds its lock would stop a client that waits for
// that lock. So Send never blocks.
type Conn struct {
	net.Conn
	mu    sync.Mutex
	queue []string
	ready chan struct{}
}

// NewConn starts the writer of one connection.
func NewConn(conn net.Conn) *Conn {
	c := &Conn{Conn: conn, ready: make(chan struct{}, 1)}
	go c.write()
	return c
}

// Send queues text for the client.
func (c *Conn) Send(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queue = append(c.queue, text)
	select {
	case c.ready <- struct{}{}:
	default:
	}
}

// write writes the queue until a write fails, as each one does after
// either end closes the pipe. Close wakes it to find that out.
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

// Close closes the connection, and ends its writer.
func (c *Conn) Close() error {
	err := c.Conn.Close()
	c.Send("")
	return err
}
