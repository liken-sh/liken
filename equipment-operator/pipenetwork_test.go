package main

// The network the fake receivers listen on. Each
// connection to a fake is a net.Pipe, whose reads and writes wait on
// channels. So a test in a synctest bubble that talks to a fake waits
// on nothing outside the bubble, and its fake clock advances. The
// operator reaches the fakes through the dialFunc that the controller
// and the Denon client take.

import (
	"context"
	"fmt"
	"net"
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
