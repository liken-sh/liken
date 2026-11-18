package main

// An in-memory network for the fake receivers and amps. Each
// connection is a net.Pipe, which blocks on channels and not on a
// socket, so a test in a synctest bubble never waits outside the bubble
// and the fake clock can advance. Tests give the operator the network's
// dial as its dialFunc and bind as its listenFunc.

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"testing"
)

// pipeNetwork connects a dial to a fake's address with a pipe. A dial
// to an address with no fake fails with ECONNREFUSED, as a closed
// loopback port does.
type pipeNetwork struct {
	mutex     sync.Mutex
	ports     int
	listeners map[string]func(net.Conn)
}

// testNetwork is shared by every test. Each fake gets its own address,
// so one test never reaches another test's fake.
var testNetwork = &pipeNetwork{listeners: map[string]func(net.Conn){}}

// listen returns a new address and runs serve on a new goroutine for
// each connection to it, until the test ends.
func (n *pipeNetwork) listen(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	address := n.reserve(serve)
	t.Cleanup(func() { n.free(address) })
	return address
}

func (n *pipeNetwork) reserve(serve func(net.Conn)) string {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	n.ports++
	address := fmt.Sprintf("127.0.0.1:%d", 20000+n.ports)
	n.listeners[address] = serve
	return address
}

func (n *pipeNetwork) free(address string) {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	delete(n.listeners, address)
}

// bind is the listenFunc that tests pass to the operator. It ignores
// the requested address and listens at a new one.
func (n *pipeNetwork) bind(string, string) (net.Listener, error) {
	listener := &pipeListener{accepted: make(chan net.Conn), closed: make(chan struct{})}
	address := n.reserve(listener.deliver)
	listener.address, _ = net.ResolveTCPAddr("tcp", address)
	listener.free = func() { n.free(address) }
	return listener, nil
}

// serveHTTP serves handler at a new address until the test ends and
// returns the address. It serves TLS when certificate is not nil.
func (n *pipeNetwork) serveHTTP(t *testing.T, handler http.Handler, certificate *tls.Certificate) string {
	t.Helper()
	listener, _ := n.bind("tcp", ":0")
	if certificate != nil {
		listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{*certificate}})
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return listener.Addr().String()
}

// pipeListener is a net.Listener that accepts the server end of each
// pipe that a dial to its address creates.
type pipeListener struct {
	address  *net.TCPAddr
	accepted chan net.Conn
	closed   chan struct{}
	once     sync.Once
	free     func()
}

// deliver passes conn to Accept, or closes it when the listener is
// closed.
func (l *pipeListener) deliver(conn net.Conn) {
	select {
	case l.accepted <- conn:
	case <-l.closed:
		_ = conn.Close()
	}
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
	l.once.Do(func() {
		close(l.closed)
		l.free()
	})
	return nil
}

func (l *pipeListener) Addr() net.Addr { return l.address }

// dial is the dialFunc that tests pass to the operator.
func (n *pipeNetwork) dial(_ context.Context, network, address string) (net.Conn, error) {
	n.mutex.Lock()
	serve, listening := n.listeners[address]
	n.mutex.Unlock()
	if !listening {
		return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	}
	client, server := net.Pipe()
	go serve(server)
	return client, nil
}
