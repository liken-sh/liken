package main

// A pipeListener lets a real http.Server with TLS serve over in-memory
// pipes, so a test of a TLS handshake can run in a synctest bubble. A
// goroutine that reads a real socket is not durably blocked, so the
// bubble's clock stops while a socket is open. A pipe's reads block on
// channels, which synctest counts as durably blocked.

import (
	"bytes"
	"context"
	"net"
	"os"
	"sync"
	"syscall"
)

type pipeListener struct {
	accepted chan net.Conn
	closed   chan struct{}
	once     sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{accepted: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.accepted:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.UnixAddr{Name: "pipe", Net: "pipe"} }

// dial has the signature of net.Dialer.DialContext and ignores the
// address. When the listener is closed, it returns a connection refused
// error, like a port with no server.
func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.accepted <- server:
		return queued(client), nil
	case <-l.closed:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
}

// queuedConn is the client's end of a pipe, with a send buffer like a
// socket's. Write returns at once, and a goroutine writes the bytes to
// the pipe in order. A bare net.Pipe has no buffer, so when a TLS client
// rejects the server's certificate, the client blocks writing its alert
// while the server blocks writing the rest of its handshake.
type queuedConn struct {
	net.Conn
	writes chan []byte
	closed chan struct{}
	once   sync.Once
}

func queued(conn net.Conn) *queuedConn {
	q := &queuedConn{Conn: conn, writes: make(chan []byte, 64), closed: make(chan struct{})}
	go q.send()
	return q
}

func (q *queuedConn) Write(payload []byte) (int, error) {
	select {
	case q.writes <- bytes.Clone(payload):
		return len(payload), nil
	case <-q.closed:
		return 0, net.ErrClosed
	}
}

func (q *queuedConn) send() {
	for {
		select {
		case payload := <-q.writes:
			if _, err := q.Conn.Write(payload); err != nil {
				return
			}
		case <-q.closed:
			return
		}
	}
}

func (q *queuedConn) Close() error {
	q.once.Do(func() { close(q.closed) })
	return q.Conn.Close()
}
