// The network the fake amps and their UPnP sides listen on, in memory.
// Each connection is a net.Pipe, whose reads and writes wait on
// channels, so a test in a synctest bubble that talks to a fake waits
// on nothing outside the bubble, and its fake clock advances. The
// client reaches the fakes through its Dial, and the fakes reach the
// client's event callback through the listener its Listen opens.

package wiim

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

// pipeNetwork hands each dial to an address a listener holds one end of
// a new pipe, and the listener the other end. A dial to any other
// address fails with ECONNREFUSED, the way a closed port on loopback
// answers.
type pipeNetwork struct {
	mutex     sync.Mutex
	ports     int
	listeners map[string]*pipeListener
}

// testNetwork is the network every test's fakes listen on. Each
// listener takes an address of its own, so two tests never reach each
// other's fakes.
var testNetwork = &pipeNetwork{listeners: map[string]*pipeListener{}}

// Listen is the Listen of a client under test. It opens a listener at a
// new loopback address, whatever address the client asks for.
func (n *pipeNetwork) Listen(string, string) (net.Listener, error) {
	return n.open(), nil
}

func (n *pipeNetwork) open() *pipeListener {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	n.ports++
	listener := &pipeListener{
		network:  n,
		address:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 20000 + n.ports},
		accepted: make(chan net.Conn),
		closed:   make(chan struct{}),
	}
	n.listeners[listener.address.String()] = listener
	return listener
}

// dial is the Dial of a client under test, and of a fake that calls the
// client back.
func (n *pipeNetwork) dial(ctx context.Context, network, address string) (net.Conn, error) {
	n.mutex.Lock()
	listener, listening := n.listeners[address]
	n.mutex.Unlock()
	refused := &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	if !listening {
		return nil, refused
	}
	client, server := net.Pipe()
	select {
	case listener.accepted <- server:
		return client, nil
	case <-listener.closed:
		return nil, refused
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// serve answers each request to a new address with handler until the
// test ends, and answers the address. A fake amp serves over TLS, the
// way the amp's control API does, and its UPnP side serves plain HTTP.
func (n *pipeNetwork) serve(t *testing.T, handler http.Handler, overTLS bool) string {
	t.Helper()
	listener := n.open()
	var serving net.Listener = listener
	if overTLS {
		serving = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{testCertificate(t)}})
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(serving) }()
	t.Cleanup(func() { _ = server.Close() })
	return listener.address.String()
}

// pipeListener takes the server end of each pipe a dial to its address
// opens.
type pipeListener struct {
	network  *pipeNetwork
	address  *net.TCPAddr
	accepted chan net.Conn
	closed   chan struct{}
	once     sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.accepted:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// Close frees the address, so a later dial to it is refused.
func (l *pipeListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
		l.network.mutex.Lock()
		defer l.network.mutex.Unlock()
		delete(l.network.listeners, l.address.String())
	})
	return nil
}

func (l *pipeListener) Addr() net.Addr { return l.address }

// testClient is a client for address that reaches the test network.
func testClient(address string, listener func(equipment.Event)) *Client {
	client := NewClient(address, listener)
	client.Dial = testNetwork.dial
	client.Listen = testNetwork.Listen
	return client
}

// testCertificate is a self-signed certificate for the fake amps. The
// client skips verification, the way it does for a real amp, so any
// certificate serves.
func testCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	mustSucceed(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "amp.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	mustSucceed(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
