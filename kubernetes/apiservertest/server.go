// Package apiservertest serves a test's fake API server over in-memory
// connections, so a test that talks to it can run on the fake clock of
// testing/synctest.
//
// A synctest bubble advances its clock only while every goroutine in
// it is durably blocked. A goroutine that reads a socket is not durably
// blocked, because an event outside the bubble can wake it. So a test
// whose client reads from an httptest.Server never advances the clock,
// and client-go's reflector backoff, a lease, or a retry wait takes its
// full duration in real time. This package replaces the socket with
// net.Pipe connections, whose reads block on channels. The server, its
// connections, and the client's connections start in the test's own
// goroutine tree, so in a bubble they are part of it, and a wait of a
// minute takes no real time.
//
// A test serves its handler with Start, and gives client-go the
// configuration that Config answers:
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
// synctest.Test waits for every goroutine in the bubble to exit, and
// fails the test when they block forever. The server closes itself and
// each of its connections when the test ends, so a client's connections
// end too. A goroutine that the test started, such as an informer, a
// watch, or a retry loop, runs until the test stops it. Start each one
// with t.Context(), which ends before the cleanups run, or stop it in a
// cleanup. A loop that keeps its own context sends to the closed server
// again after each backoff, and the bubble never ends.
//
// client-go's reflector has one wait that does not end with its context:
// after a streaming list meets a refused connection or a 429, it waits
// out its backoff, which is less than a minute. A test that takes the
// server down while a reflector runs sleeps for a minute in a cleanup,
// so that wait ends before the bubble does.
//
// The same server works outside a bubble, so a test can move to it
// before it moves to synctest.
//
// A program that imports the package also loses apimachinery's global
// rate limit on unhandled errors, because in a bubble that rate limit
// stops the reflector (errors.go).
package apiservertest

import (
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"testing"
)

// Server serves one handler over in-memory connections. It is up after
// Start, and SetDown takes it down and brings it back.
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

// SetDown takes the server down, or brings it up again. A server that
// goes down closes each connection it holds, the way a stopped process
// does, so a watch ends. Each request then fails with ECONNREFUSED, the
// error the kernel gives a client of an API server that restarts, until
// the server comes up. client-go's reflector checks for that error, and
// retries such a watch without a new list.
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

// dial opens a pipe to the server and answers the client's end, once
// the server has accepted the other end. A server that is down refuses
// the connection. The lock keeps the server up until it accepts, and
// its accept loop takes each pipe at once.
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

// pipeListener hands the server one end of each pipe that a dial opens.
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
