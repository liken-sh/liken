package main

// The network the fake receivers and amps listen on. Each
// connection to a fake is a net.Pipe, whose reads and writes wait on
// channels. So a test in a synctest bubble that talks to a fake waits
// on nothing outside the bubble, and its fake clock advances. The
// operator reaches the fakes through the dialFunc that the controller
// and the clients take, and opens each WiiM's event listener through
// the network's listenFunc.

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

// pipeNetwork hands each dial to an address a fake listens on one end
// of a new pipe, and the fake the other end. A dial to any other
// address fails with ECONNREFUSED, the way a closed port on loopback
// answers.
type pipeNetwork struct {
	mutex     sync.Mutex
	ports     int
	listeners map[string]func(net.Conn)
}

// testNetwork is the network every test's fakes listen on. Each fake
// takes an address of its own, so two tests never reach each other's
// fakes, and the address of a fake that stopped refuses.
var testNetwork = &pipeNetwork{listeners: map[string]func(net.Conn){}}

// listen takes a new loopback address, and serves each connection to it
// on a goroutine of its own until the test ends.
func (n *pipeNetwork) listen(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	address := n.take(serve)
	t.Cleanup(func() { n.free(address) })
	return address
}

// take holds a new loopback address for serve.
func (n *pipeNetwork) take(serve func(net.Conn)) string {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	n.ports++
	address := fmt.Sprintf("127.0.0.1:%d", 20000+n.ports)
	n.listeners[address] = serve
	return address
}

// free gives an address up, so a dial to it is refused.
func (n *pipeNetwork) free(address string) {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	delete(n.listeners, address)
}

// bind is the listenFunc of a test. It opens a listener at a new
// loopback address, whatever address the caller asks for, and the
// address is free again when the listener closes.
func (n *pipeNetwork) bind(string, string) (net.Listener, error) {
	listener := &pipeListener{accepted: make(chan net.Conn), closed: make(chan struct{})}
	address := n.take(listener.hand)
	listener.address, _ = net.ResolveTCPAddr("tcp", address)
	listener.free = func() { n.free(address) }
	return listener, nil
}

// serveHTTP answers every request to a new address with handler until
// the test ends, and answers the address. It serves TLS with
// certificate when certificate is not nil.
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

// pipeListener takes the server end of each pipe a dial to its address
// opens.
type pipeListener struct {
	address  *net.TCPAddr
	accepted chan net.Conn
	closed   chan struct{}
	once     sync.Once
	free     func()
}

// hand gives one connection to Accept, or closes it when the listener
// has closed.
func (l *pipeListener) hand(conn net.Conn) {
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

// dial is the dialFunc of a test.
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
