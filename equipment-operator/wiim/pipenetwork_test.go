// An in-memory network for the fake amps and their UPnP servers. Each
// connection is a net.Pipe, which blocks on channels and not on a
// socket, so a test in a synctest bubble never waits outside the bubble
// and the fake clock can advance. A client dials the fakes through its
// Dial field, and the fakes send events to the listener that its Listen
// field opens.

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

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// pipeNetwork connects a dial to a listener's address with a pipe. A
// dial to an address with no listener fails with ECONNREFUSED, as a
// closed loopback port does.
type pipeNetwork struct {
	mutex     sync.Mutex
	ports     int
	listeners map[string]*pipeListener
}

// testNetwork is shared by every test. Each listener gets its own
// address, so one test never reaches another test's fake.
var testNetwork = &pipeNetwork{listeners: map[string]*pipeListener{}}

// Listen is the Listen function that tests give a client. It ignores
// the requested address and listens at a new one.
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

// dial is the Dial function that tests give a client. A fake also uses
// it to send events to the client's listener.
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

// serve serves handler at a new address until the test ends and
// returns the address. A fake amp's control API uses TLS, as a real
// amp's does, and its UPnP server uses plain HTTP.
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

// pipeListener is a net.Listener that accepts the server end of each
// pipe that a dial to its address creates.
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

// Close releases the address, so a later dial to it is refused.
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

func testClient(address string, listener func(equipment.Event)) *Client {
	client := NewClient(address, listener)
	client.Dial = testNetwork.dial
	client.Listen = testNetwork.Listen
	return client
}

// testCertificate returns a self-signed certificate for the fake amps.
// The client does not verify an amp's certificate, so any one works.
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
