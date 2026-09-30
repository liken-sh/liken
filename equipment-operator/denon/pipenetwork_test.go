// The network the fake receiver and its description listen on. Each
// connection is a net.Pipe, whose reads and writes wait on channels,
// so a test in a synctest bubble that talks to a fake waits on nothing
// outside the bubble, and its fake clock advances. The client reaches
// the fakes through its Dial.

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
// fakes.
var testNetwork = &pipeNetwork{listeners: map[string]func(net.Conn){}}

// listen takes a new loopback address, and serves each connection to it
// on a goroutine of its own until the test ends.
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

// dial is the Dial of a client under test.
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
// connection with the handler, the way the receiver's description
// server answers a client that keeps no connection open.
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
