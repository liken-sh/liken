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
	"time"
)

func TestThePrivateLegDialsThePodsOwnAddress(t *testing.T) {
	relay := &forwarder{port: capturePort}
	got := relay.origin(capturePod{IP: "10.42.0.7"})
	if got != "https://10.42.0.7:9201" {
		t.Errorf("the private leg dials %q", got)
	}
}

func TestAForwarderWithNoAnchorHoldsNothing(t *testing.T) {
	relay := newForwarder(func() []byte { return nil })
	if _, err := relay.transport(); !errors.Is(err, ErrNoCertificate) {
		t.Errorf("a forwarder with no CA answered %v", err)
	}
}

func TestACAThatIsNotPEMIsARefusal(t *testing.T) {
	relay := newForwarder(func() []byte { return []byte("not pem") })
	if _, err := relay.transport(); err == nil {
		t.Error("a CA that is not PEM was taken")
	}
}

func TestOnlyTheFourHeadersAreRelayed(t *testing.T) {
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
	// The API builds these itself, so the container's copy never wins.
	for _, name := range []string{"Server", "Link"} {
		if got := to.Get(name); got != "" {
			t.Errorf("%s was relayed: %q", name, got)
		}
	}
}

func TestAStreamThatGoesQuietForTooLongEnds(t *testing.T) {
	at := time.Unix(1789000000, 0)
	quiet := &quietReader{}
	reader := newIdleReader(quiet, 30*time.Second, at)
	reader.now = func() time.Time { return at }

	// Nothing has arrived, and the timeout has not passed.
	if _, err := reader.Read(make([]byte, 8)); err != nil {
		t.Fatalf("a quiet stream inside the timeout ended: %v", err)
	}
	// The timeout passes with nothing delivered.
	reader.now = func() time.Time { return at.Add(31 * time.Second) }
	_, err := reader.Read(make([]byte, 8))
	if err == nil {
		t.Fatal("a stream that went quiet did not end")
	}
	if !strings.Contains(err.Error(), "30s") {
		t.Errorf("the end is reported as %q", err)
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
	// The clock moved past the first count, and the delivery reset it.
	now = at.Add(50 * time.Second)
	if _, err := reader.Read(make([]byte, 7)); !errors.Is(err, io.EOF) {
		t.Errorf("the reader answered %v, want the reader's own end", err)
	}
}

func TestTheIdleCountStartsAfterTheDiscard(t *testing.T) {
	// A tap with t=45,50 discards forty-five seconds. The count starts
	// at the end of the discard, so the thirty-second bound never fires
	// while the container is discarding.
	at := time.Unix(1789000000, 0)
	reader := newIdleReader(&quietReader{}, 30*time.Second, at.Add(45*time.Second))
	reader.now = func() time.Time { return at.Add(40 * time.Second) }
	if _, err := reader.Read(make([]byte, 8)); err != nil {
		t.Fatalf("the bound fired during the discard: %v", err)
	}
}

// quietReader delivers nothing and never ends, which is a container
// that has stopped sending.
type quietReader struct{}

func (quietReader) Read([]byte) (int, error) { return 0, nil }

func TestASilentConnectionIsCutOffFromOutsideTheRead(t *testing.T) {
	// A read on a connection that went silent blocks inside the
	// transport, so the bound has to end the request rather than wait
	// for a read to come back.
	blocked := make(chan struct{})
	reader := newIdleReader(blockingReader{until: blocked}, 20*time.Millisecond, time.Now())
	ended := make(chan struct{})
	reader.watch(func() {
		close(blocked)
		close(ended)
	})
	defer reader.stopWatching()

	if _, err := reader.Read(make([]byte, 8)); err == nil {
		t.Fatal("a silent connection was not cut off")
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("the bound never ended the request")
	}
}

// blockingReader is a connection that went silent: it returns nothing
// until the request that owns it is cut off.
type blockingReader struct{ until chan struct{} }

func (b blockingReader) Read([]byte) (int, error) {
	<-b.until
	return 0, io.EOF
}

// The private leg is classified by how far the request got, not by
// what the transport put in the error. Go reports a server that writes
// garbage and closes as a malformed response, a peek failure, or an
// unexpected EOF depending on which side of the read the close lands,
// so a classifier that read the words answered differently for one
// failure.
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
		// Everything below happened after the connection stood.
		{"a read on a connection that stood",
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

// A container whose Secret has yet to arrive serves a certificate it
// signed itself. That is the plan's "has no certificate yet", it
// clears when the API mints the leaf, and it is a 503 rather than a
// 502 about a broken upstream.
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
	// An answer that did not read is not a certificate problem.
	if !trusted(errors.New("readLoopPeekFailLocked: %!w(<nil>)")) {
		t.Error("a peek failure read as a certificate problem")
	}
}
