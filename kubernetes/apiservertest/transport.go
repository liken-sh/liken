package apiservertest

// The client side sends each request on a connection of its own, and
// closes the connection when the caller closes the answer's body.
//
// net/http's Transport does not work in a bubble. When a caller closes
// a streaming body early, such as a watch that stops, the Transport
// reads what is left of the body for up to 50 milliseconds, so it can
// keep the connection. The watch's own reader holds the body's lock
// while it waits for the next event, so the drain waits on that lock. A
// goroutine that waits on a lock is not durably blocked, the clock never
// reaches the 50 milliseconds, and the test never ends. Here, a closed
// body closes its connection first, which ends the reader's wait.

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"

	"k8s.io/client-go/rest"
)

// Host is the address that Config and Client send each request to. The
// server answers every address, because each request reaches it through
// a pipe and no name is resolved.
const Host = "http://apiserver.test"

// Config answers a client-go configuration whose requests reach the
// server. Each call answers a new configuration, which a test can
// change, for example to wrap its Transport.
func (s *Server) Config() *rest.Config {
	return &rest.Config{Host: Host, Transport: s}
}

// Client answers an HTTP client whose requests reach the server, for a
// client that is not client-go.
func (s *Server) Client() *http.Client {
	return &http.Client{Transport: s}
}

// RoundTrip sends one request to the server on a new connection, and
// answers the server's response. The connection closes when the
// request's context ends or the caller closes the body.
func (s *Server) RoundTrip(req *http.Request) (*http.Response, error) {
	// A request whose context ended is never sent.
	err := context.Cause(req.Context())
	var conn net.Conn
	if err == nil {
		conn, err = s.dial()
	}
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	stop := context.AfterFunc(req.Context(), func() { _ = conn.Close() })
	// The server can answer before it reads the whole request, so the
	// request is written while the response is read.
	go func() { _ = req.Write(conn) }()
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		stop()
		_ = conn.Close()
		// A request whose context ended fails with the context's error,
		// as it does through net/http's Transport, not with the error of
		// the connection that the end of the context closed.
		if ended := context.Cause(req.Context()); ended != nil {
			return nil, ended
		}
		return nil, err
	}
	resp.Body = &connBody{ReadCloser: resp.Body, conn: conn, stop: stop}
	return resp, nil
}

// connBody is a response body that closes its connection.
type connBody struct {
	io.ReadCloser
	conn net.Conn
	stop func() bool
}

func (b *connBody) Close() error {
	b.stop()
	_ = b.conn.Close()
	return b.ReadCloser.Close()
}
