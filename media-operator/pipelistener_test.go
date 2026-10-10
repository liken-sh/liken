package main

// A pipeListener serves a real http.Server, with its own TLS, over
// in-memory connections. A test of a TLS handshake then runs in a
// synctest bubble: a goroutine that reads a socket is not durably
// blocked, so the bubble's clock does not advance while one is open,
// and a pipe's reads block on channels instead.

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

// dial opens a pipe and hands the server the far end. It has the
// signature of net.Dialer.DialContext, and it answers every address. A
// listener that has closed refuses the connection, the way a port with
// no server does.
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

// queuedConn is the client's end of a pipe. Its writes return at once,
// and a goroutine of its own delivers them in order, the way a socket's
// send buffer holds bytes the peer has not read yet. A bare pipe holds
// no bytes, so a TLS client that refuses the server's certificate
// blocks on the write of its alert while the server blocks on the rest
// of its own handshake, and neither write ends.
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
