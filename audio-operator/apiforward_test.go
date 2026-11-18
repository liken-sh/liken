package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

func TestThePrivateLegDialsThePodsOwnAddress(t *testing.T) {
	relay := &forwarder{port: capturePort}
	got := relay.origin(capturePod{IP: "10.42.0.7"})
	if got != "https://10.42.0.7:9201" {
		t.Errorf("the private leg dials %q, want https://10.42.0.7:9201", got)
	}
}

func TestAForwarderWithNoAnchorBuildsNoClient(t *testing.T) {
	relay := newForwarder(func() []byte { return nil })
	if _, err := relay.transport(); !errors.Is(err, ErrNoCertificate) {
		t.Errorf("transport() with no CA returned %v, want ErrNoCertificate", err)
	}
}

func TestACAThatIsNotPEMIsARefusal(t *testing.T) {
	relay := newForwarder(func() []byte { return []byte("not pem") })
	if _, err := relay.transport(); err == nil {
		t.Error("transport() with a CA that is not PEM returned no error, want one")
	}
}

func TestRelayHeadersCopiesOnlyTheRelayedList(t *testing.T) {
	from := http.Header{}
	from.Set("Content-Type", "audio/flac")
	from.Set("Cache-Control", "no-store")
	from.Set("Accept-Ranges", "none")
	from.Set("Retry-After", "5")
	from.Set("Server", "the capture container")
	from.Set("Link", "<somewhere>; rel=\"alternate\"")

	to := http.Header{}
	relayHeaders(from, to)
	for name, want := range map[string]string{
		"Content-Type":  "audio/flac",
		"Cache-Control": "no-store",
		"Accept-Ranges": "none",
		"Retry-After":   "5",
	} {
		if got := to.Get(name); got != want {
			t.Errorf("%s was relayed as %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"Server", "Link"} {
		if got := to.Get(name); got != "" {
			t.Errorf("%s was relayed as %q, want no such header", name, got)
		}
	}
}

func TestAStreamThatGoesQuietForTooLongEnds(t *testing.T) {
	at := time.Unix(1789000000, 0)
	quiet := &quietReader{}
	reader := newIdleReader(quiet, 30*time.Second, at)
	reader.now = func() time.Time { return at }

	if _, err := reader.Read(make([]byte, 8)); err != nil {
		t.Fatalf("a quiet stream inside the timeout ended: %v", err)
	}
	reader.now = func() time.Time { return at.Add(31 * time.Second) }
	_, err := reader.Read(make([]byte, 8))
	if err == nil {
		t.Fatal("a stream that went quiet returned no error, want the idle end")
	}
	if !strings.Contains(err.Error(), "30s") {
		t.Errorf("the end is reported as %q, want a message that names 30s", err)
	}
}

func TestAStreamThatDeliversResetsTheIdleCount(t *testing.T) {
	at := time.Unix(1789000000, 0)
	reader := newIdleReader(strings.NewReader("samples"), 30*time.Second, at)
	now := at
	reader.now = func() time.Time { return now }

	now = at.Add(29 * time.Second)
	if _, err := reader.Read(make([]byte, 7)); err != nil {
		t.Fatalf("a stream that delivered ended: %v", err)
	}
	// 21 seconds have passed since the byte at 29 seconds, which is under
	// the timeout. Counted from the start, 50 seconds would be over it.
	now = at.Add(50 * time.Second)
	if _, err := reader.Read(make([]byte, 7)); !errors.Is(err, io.EOF) {
		t.Errorf("the reader returned %v, want io.EOF from the underlying reader", err)
	}
}

func TestTheIdleCountStartsAtBegin(t *testing.T) {
	// A tap with t=45,50 drops 45 seconds of audio before the body
	// starts, so the silence counts from begin.
	at := time.Unix(1789000000, 0)
	reader := newIdleReader(&quietReader{}, 30*time.Second, at.Add(45*time.Second))
	reader.now = func() time.Time { return at.Add(40 * time.Second) }
	if _, err := reader.Read(make([]byte, 8)); err != nil {
		t.Fatalf("the bound fired before begin: %v", err)
	}
}

// quietReader returns no bytes and no error, like a container that has
// stopped sending.
type quietReader struct{}

func (quietReader) Read([]byte) (int, error) { return 0, nil }

func TestASilentConnectionIsCutOffFromOutsideTheRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		blocked := make(chan struct{})
		started := time.Now()
		reader := newIdleReader(blockingReader{until: blocked}, idleTimeout, started)
		reader.watch(func() { close(blocked) })
		defer reader.stopWatching()

		if _, err := reader.Read(make([]byte, 8)); err == nil {
			t.Fatal("a silent connection returned no error, want the idle end")
		}
		if waited := time.Since(started); waited != idleTimeout {
			t.Errorf("the bound ended the request after %s, want %s", waited, idleTimeout)
		}
	})
}

// blockingReader blocks until its channel is closed, like a silent
// connection until its request is canceled.
type blockingReader struct{ until chan struct{} }

func (b blockingReader) Read([]byte) (int, error) {
	<-b.until
	return 0, io.EOF
}

func TestOnlyAConnectionThatWasNeverMadeIsUnreachable(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		reachable bool
	}{
		{"a dial that failed", &net.OpError{Op: "dial", Err: errors.New("no route")}, false},
		{"nothing listening", syscall.ECONNREFUSED, false},
		{"the node is gone", syscall.EHOSTUNREACH, false},
		{"the pod network is not up", syscall.ENETUNREACH, false},
		{"a dial failure the transport wrapped",
			fmt.Errorf("Get %q: %w", "https://10.42.0.7:9201/",
				&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}), false},
		// These failed after the connection was established.
		{"a read on a connection that was established",
			&net.OpError{Op: "read", Err: errors.New("connection reset by peer")}, true},
		{"a malformed response",
			errors.New(`net/http: HTTP/1.x transport connection broken: malformed HTTP response "x"`), true},
		{"a peek that failed",
			errors.New("readLoopPeekFailLocked: %!w(<nil>)"), true},
		{"an unexpected EOF before the headers", io.ErrUnexpectedEOF, true},
	}
	for _, row := range cases {
		if got := reachable(row.err); got != row.reachable {
			t.Errorf("%s reads as reachable=%v, want %v", row.name, got, row.reachable)
		}
	}
}

func TestACertificateThisAPIDoesNotTrustIsNotABrokenUpstream(t *testing.T) {
	untrusted := &tls.CertificateVerificationError{
		Err: errors.New("x509: certificate signed by unknown authority"),
	}
	if trusted(untrusted) {
		t.Error("a certificate the CA did not sign read as trusted")
	}
	if trusted(fmt.Errorf("Get %q: remote error: %w", "https://10.42.0.7:9201/", untrusted)) {
		t.Error("a wrapped verification failure read as trusted")
	}
	if trusted(tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}) {
		t.Error("a listener that speaks no TLS read as trusted")
	}
	// A response that could not be read is not a certificate problem.
	if !trusted(errors.New("readLoopPeekFailLocked: %!w(<nil>)")) {
		t.Error("a peek failure read as a certificate problem")
	}
}
