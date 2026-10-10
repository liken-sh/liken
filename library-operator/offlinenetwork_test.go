package main

// The tests reach no network. A test that needs a peer, such as a
// catalog agent, a metadata provider, or a Jellyfin server, serves the
// peer's handler over in-memory connections through apiservertest. A
// test that needs a peer that answers nothing uses a transport that
// refuses every request, the way a closed port refuses a connection.
// So no test waits on a socket, and a test in a synctest bubble keeps
// its fake clock.

import (
	"context"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
)

// unreachableAddress is the address a test names for a peer that
// answers nothing. No resolver knows the name, and the refusing
// transport never resolves it.
const unreachableAddress = "http://unreachable.test"

// refusedTransport fails every request with the error a dial to a
// closed port returns.
type refusedTransport struct{}

func (refusedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		_ = request.Body.Close()
	}
	return nil, &net.OpError{Op: "dial", Net: "tcp",
		Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
}

// refusedClient is an HTTP client whose every request is refused.
func refusedClient() *http.Client {
	return &http.Client{Transport: refusedTransport{}}
}

// routedHosts sends each request to the transport of its URL's host,
// such as an apiservertest.Server, and refuses every other host.
type routedHosts map[string]http.RoundTripper

func (r routedHosts) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport, served := r[request.URL.Host]; served {
		return transport.RoundTrip(request)
	}
	return refusedTransport{}.RoundTrip(request)
}

// pipeListener is a listener with no socket, so a server that a test
// starts runs in a synctest bubble. Each connection is one end of a
// net.Pipe, whose other end the listener's own client dials. Accept
// returns net.ErrClosed once the server closes the listener.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
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

// dial hands the server one end of a new pipe and answers the other. A
// listener that is closed refuses the connection.
func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	server, client := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
	case <-ctx.Done():
	}
	_ = server.Close()
	_ = client.Close()
	return nil, &net.OpError{Op: "dial", Net: "pipe", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
}

// Client is an HTTP client whose every request reaches the listener's
// server, on a connection of its own. It keeps no idle connection, so
// no reader outlives the request.
func (l *pipeListener) Client() *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: l.dial, DisableKeepAlives: true}}
}
