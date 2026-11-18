package apiservertest

// The client side of the fake API server. It sends each request on a
// new connection and closes that connection when the caller closes the
// response body.
//
// net/http's Transport cannot be used inside a synctest bubble. When a
// caller closes a streaming body early, such as a watch that stops, the
// Transport drains the rest of the body for up to 50 milliseconds so it
// can reuse the connection. The watch's reader holds the body's lock
// while it waits for the next event, so the drain waits on that lock. A
// goroutine that waits on a lock is not durably blocked, so the fake
// clock never advances to 50 milliseconds and the test never ends. This
// transport closes the connection first, which ends the reader's wait.

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"

	"k8s.io/client-go/rest"
)

// Host is the address that Config and Client send each request to. The
// server accepts any address, because each request reaches it through
// an in-memory pipe and no name is resolved.
const Host = "http://apiserver.test"

// Config returns a client-go configuration whose requests reach the
// server. Each call returns a new configuration, so a test can change
// it, for example to wrap its Transport.
func (s *Server) Config() *rest.Config {
	return &rest.Config{Host: Host, Transport: s}
}

// Client returns an HTTP client whose requests reach the server, for
// code that does not use client-go.
func (s *Server) Client() *http.Client {
	return &http.Client{Transport: s}
}

// RoundTrip sends one request to the server on a new connection and
// returns the server's response. The connection closes when the
// request's context ends or the caller closes the body.
func (s *Server) RoundTrip(req *http.Request) (*http.Response, error) {
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
	// The server can respond before it has read the whole request, so
	// write the request while reading the response.
	go func() { _ = req.Write(conn) }()
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		stop()
		_ = conn.Close()
		// Return the context's error, as net/http's Transport does, and
		// not the read error that closing the connection caused.
		if ended := context.Cause(req.Context()); ended != nil {
			return nil, ended
		}
		return nil, err
	}
	resp.Body = &connBody{ReadCloser: resp.Body, conn: conn, stop: stop}
	return resp, nil
}

// connBody is a response body whose Close also closes the connection.
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
