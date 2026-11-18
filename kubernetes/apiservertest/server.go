// Package apiservertest serves a fake API server to a test over
// in-memory connections, so the test can run on the fake clock of
// testing/synctest.
//
// A synctest bubble advances its clock only when every goroutine in it
// is durably blocked, and a goroutine that reads a socket is not,
// because an event outside the bubble can wake it. So when a test's
// client reads from an httptest.Server, the clock does not advance, and
// a reflector backoff, a lease, or a retry wait takes its full duration
// in real time. This package uses net.Pipe connections instead, whose
// reads block on channels. Start and each dial run inside the test, so
// inside a bubble the server and its connections belong to the bubble,
// and a wait of a minute takes no real time.
//
// Serve a handler with Start, and give client-go the configuration that
// Config returns:
//
//	synctest.Test(t, func(t *testing.T) {
//		server := apiservertest.Start(t, handler)
//		client, err := dynamic.NewForConfig(server.Config())
//		...
//		informer.Start(t.Context(), client, source, options)
//		time.Sleep(time.Minute)
//		synctest.Wait()
//	})
//
// The server closes itself and each of its connections when the test
// ends. synctest.Test waits for every goroutine in the bubble to exit
// and fails the test if they block forever. So a goroutine that the test
// starts, such as an informer, a watch, or a retry loop, must stop when
// the test ends. Start it with t.Context(), which ends before the
// cleanups run, or stop it in a cleanup. A loop with its own context
// keeps sending to the closed server after each backoff, and the bubble
// never ends.
//
// One wait in client-go's reflector does not end with its context. When
// a streaming list fails with a refused connection or a 429, the
// reflector waits out its backoff, which is less than a minute. A test
// that takes the server down while a reflector runs should sleep for a
// minute in a cleanup, so that wait ends before the bubble does.
//
// The same server works outside a bubble, so a test can move to it
// before it moves to synctest.
//
// Importing the package also removes apimachinery's global rate limit on
// unhandled errors, because in a bubble that rate limit stops the
// reflector (see errors.go).
package apiservertest

import (
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"testing"
)

// Server serves one handler over in-memory connections.
type Server struct {
	handler http.Handler

	mu       sync.Mutex
	serving  *http.Server
	listener *pipeListener
}

// Start serves the handler until the test ends.
func Start(t testing.TB, handler http.Handler) *Server {
	s := &Server{handler: handler}
	s.SetDown(false)
	t.Cleanup(func() { s.SetDown(true) })
	return s
}

// SetDown takes the server down, or brings it back up. Going down closes
// every open connection, as a stopped process does, so each watch ends.
// While the server is down, each new connection fails with ECONNREFUSED,
// the error a client gets from a restarting API server. client-go's
// reflector retries a watch that fails with that error without a new
// list.
func (s *Server) SetDown(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if down == (s.serving == nil) {
		return
	}
	if down {
		_ = s.serving.Close()
		s.serving, s.listener = nil, nil
		return
	}
	listener, serving := newPipeListener(), &http.Server{Handler: s.handler}
	s.listener, s.serving = listener, serving
	go func() { _ = serving.Serve(listener) }()
}

// dial opens a pipe to the server and returns the client's end after
// Accept receives the server's end. It holds mu during the handoff, so
// the server cannot go down between the check and the send.
func (s *Server) dial() (net.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	}
	client, server := net.Pipe()
	s.listener.accepted <- server
	return client, nil
}

// pipeListener is the net.Listener of the server. Accept returns the
// server's end of each pipe that dial opens.
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
