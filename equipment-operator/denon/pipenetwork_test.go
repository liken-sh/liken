// An in-memory network for the fake receiver and its description
// server. Each connection is a net.Pipe, whose reads and writes wait on
// channels, so a test in a synctest bubble never waits on anything
// outside the bubble and its fake clock keeps advancing.

package denon

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"syscall"
	"testing"
)

// pipeNetwork connects each dial to an address that a fake listens on
// with a new pipe: the client gets one end and the fake gets the other.
// A dial to any other address fails with ECONNREFUSED, as a closed port
// on loopback does.
type pipeNetwork struct {
	mutex     sync.Mutex
	ports     int
	listeners map[string]func(net.Conn)
}

// testNetwork is the network that every test's fakes listen on. Each
// fake gets its own address, so parallel tests never reach each other's
// fakes.
var testNetwork = &pipeNetwork{listeners: map[string]func(net.Conn){}}

// listen registers serve under a new loopback address and returns the
// address. serve runs on its own goroutine for each connection. The
// address is removed when the test ends.
func (n *pipeNetwork) listen(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	n.mutex.Lock()
	n.ports++
	address := fmt.Sprintf("127.0.0.1:%d", 20000+n.ports)
	n.listeners[address] = serve
	n.mutex.Unlock()
	t.Cleanup(func() {
		n.mutex.Lock()
		defer n.mutex.Unlock()
		delete(n.listeners, address)
	})
	return address
}

// dial is the Dial function for a client under test.
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

// serveHTTP listens on the test network and answers one request on each
// connection with handler, then closes the connection. The receiver's
// description server behaves this way for a client that does not keep
// its connection open.
func (n *pipeNetwork) serveHTTP(t *testing.T, handler http.Handler) string {
	t.Helper()
	return n.listen(t, func(conn net.Conn) {
		defer conn.Close()
		request, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		_ = recorder.Result().Write(conn)
	})
}
