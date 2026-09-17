package main

// The private leg: the API's call into one node's capture container.
//
// The API sends its own ServiceAccount token, projected with the
// audience audio-capture and a ten-minute life. It reads the file on
// every request, because the kubelet refreshes it in place.
//
// The connection goes to the pod's own address, with the ServerName
// audio-capture and the domain's CA as the only trust anchor, so the
// API reaches the container it minted the leaf for and nothing else
// that answers on the address.
//
// Two bounds apply. The header timeout is ten seconds plus begin, so
// it never fires while the container is discarding. The idle timeout
// on the body is thirty seconds, counted from the first body byte or
// from begin, whichever is later. A stream that keeps delivering has
// no bound at all, because an open-ended tap runs until the client
// closes.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// capturePort is where the container listens, which is the container
// port named capture in the DaemonSet.
const capturePort = 9201

// captureTokenVariable names the projected token's file.
const captureTokenVariable = "CAPTURE_TOKEN_FILE"

// captureTokenFile is where the projected volume mounts when nothing
// names another path.
const captureTokenFile = "/var/run/secrets/audio.liken.sh/capture/token"

// headerTimeout and idleTimeout are the two bounds on the private
// leg. The three capture APIs share both numbers.
const (
	headerTimeout = 10 * time.Second
	idleTimeout   = 30 * time.Second
)

// The three ways the private leg fails, and the status each one is.
// A container this API could not reach at all is a 503, an answer
// that is not HTTP is a 502, and a container that sent no headers
// within the bound is a 504.
var (
	ErrCaptureRefused   = errors.New("the capture container refused the connection")
	ErrCaptureMalformed = errors.New("the capture container answered something that is not HTTP")
	ErrCaptureTimeout   = errors.New("the capture container sent no headers in time")
)

// malformedAnswers are net/http's own words for a response its
// transport could not read as HTTP. There is no sentinel error for it,
// so the text the transport wrote is what this API reads.
var malformedAnswers = []string{
	"malformed HTTP response",
	"malformed HTTP status code",
	"malformed HTTP version",
	"too many transfer encodings",
	"unexpected EOF reading trailer",
}

// forwarder dials one node's capture container.
type forwarder struct {
	// anchor is the CA the container's leaf is signed by. It is read
	// per request because the API mints a new one when the old one
	// nears its end.
	anchor func() []byte

	// tokenFile is where the projected token lands.
	tokenFile string

	// port is the container's own, a field so a test points the
	// forwarder at an httptest server.
	port int

	// scheme and address stand in for the pod's own address. They are
	// fields for the port's reason: a test points the forwarder at an
	// httptest server, where the TLS this file speaks in a cluster is
	// proved by the certificate tests instead.
	scheme  string
	address string

	// dial is how the connection is made, a field for the same reason.
	dial func(ctx context.Context, network, address string) (net.Conn, error)

	// headers bounds the wait for the container's status line, over
	// and above the span's own begin. It is a field so a test drives
	// the bound without waiting the real ten seconds.
	headers time.Duration

	mu       sync.Mutex
	anchored string
	client   *http.Client
}

func newForwarder(anchor func() []byte) *forwarder {
	file := os.Getenv(captureTokenVariable)
	if file == "" {
		file = captureTokenFile
	}
	return &forwarder{anchor: anchor, tokenFile: file, port: capturePort}
}

// transport builds the client, and builds it again when the CA has
// changed. One client holds the connection pool, so a second tap on a
// node reuses the first one's connection and its handshake.
func (f *forwarder) transport() (*http.Client, error) {
	f.mu.Lock()
	held := f.client
	anchored := f.anchored
	f.mu.Unlock()
	if f.anchor == nil {
		if held == nil {
			return nil, ErrNoCertificate
		}
		return held, nil
	}
	anchor := string(f.anchor())
	if anchor == "" {
		return nil, ErrNoCertificate
	}
	if held != nil && anchored == anchor {
		return held, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// The lock was dropped to read the anchor, so another request may
	// have built the client in between.
	if f.client != nil && f.anchored == anchor {
		return f.client, nil
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(anchor)) {
		return nil, errors.New("the capture CA holds no certificates")
	}
	dial := f.dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	f.client = &http.Client{
		Transport: &http.Transport{
			DialContext: dial,
			TLSClientConfig: &tls.Config{
				RootCAs: roots,
				// The leaf carries this one name, and the API dials the
				// pod's address, so the name has to be stated.
				ServerName: captureAudience,
				MinVersion: tls.VersionTLS12,
			},
			// A stream holds its response open for as long as the
			// client reads it, so the whole-request timeout that bounds
			// an ordinary read has no place here.
			IdleConnTimeout: 60 * time.Second,
		},
	}
	f.anchored = anchor
	return f.client, nil
}

// headerBound is how long this API waits for the container to answer.
func (f *forwarder) headerBound() time.Duration {
	if f.headers > 0 {
		return f.headers
	}
	return headerTimeout
}

// forward makes one call. The caller owns the answer's body.
func (f *forwarder) forward(ctx context.Context, held capturePod,
	path, rawQuery string, begin time.Duration) (*http.Response, error) {
	client, err := f.transport()
	if err != nil {
		return nil, err
	}
	token, err := os.ReadFile(f.tokenFile)
	if err != nil {
		return nil, fmt.Errorf("reading the capture token: %w", err)
	}

	target := f.origin(held) + path
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))

	// The header bound is ten seconds plus begin, so a tap that
	// discards a minute is not cut off before its first byte.
	// The bound covers the wait for the status line and the headers and
	// nothing after it. A deadline on the request context would cut the
	// body as well, and a tap is unbounded on purpose: what bounds the
	// body is the idle watchdog in serveTap and the caller hanging up.
	// So the bound is a timer this code stops the moment the headers
	// arrive.
	headers, cancel := context.WithCancel(ctx)
	bound := f.headerBound() + begin
	deadline := time.AfterFunc(bound, cancel)
	answer, err := client.Do(request.WithContext(headers))
	// Stop answers false when the timer has already run, which is the
	// one case that is a 504.
	expired := !deadline.Stop() && ctx.Err() == nil
	if err != nil {
		cancel()
		switch {
		case malformed(err):
			return nil, fmt.Errorf("%w: %w", ErrCaptureMalformed, err)
		case expired:
			return nil, fmt.Errorf("%w within %s: %w", ErrCaptureTimeout, bound, err)
		default:
			return nil, fmt.Errorf("%w: %w", ErrCaptureRefused, err)
		}
	}
	answer.Body = &cancellingBody{ReadCloser: answer.Body, cancel: cancel}
	return answer, nil
}

// malformed says whether an error from the transport is an answer it
// could not read as HTTP, which is the one failure of the private leg
// that is a 502.
func malformed(err error) bool {
	text := err.Error()
	for _, words := range malformedAnswers {
		if strings.Contains(text, words) {
			return true
		}
	}
	return false
}

// origin is where one node's container answers. The pod's own address
// and the container's port are the whole of it.
func (f *forwarder) origin(held capturePod) string {
	scheme := f.scheme
	if scheme == "" {
		scheme = "https"
	}
	if f.address != "" {
		return scheme + "://" + f.address
	}
	return scheme + "://" + net.JoinHostPort(held.IP, strconv.Itoa(f.port))
}

// cancellingBody releases the header bound when the body is closed, so
// the context that bounded the headers does not outlive the request.
type cancellingBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancellingBody) Close() error {
	b.cancel()
	return b.ReadCloser.Close()
}

// idleReader ends a stream that has delivered nothing for idleTimeout.
//
// The count starts at the first body byte or at the end of begin,
// whichever is later, so it never fires while the container is
// discarding.
//
// There is a timer as well as a clock. A read on a connection that
// went silent blocks inside the transport, and a check that runs when
// the read returns would never run at all, so the timer ends the
// request from outside and the read comes back with the reason.
type idleReader struct {
	from  io.Reader
	after time.Duration

	// now is a field so a test drives the timeout without waiting
	// thirty seconds.
	now func() time.Time

	mu    sync.Mutex
	last  time.Time
	timer *time.Timer
	fired bool
}

// newIdleReader bounds one body. started is when the count begins, and
// stop ends the request when the bound passes.
func newIdleReader(from io.Reader, after time.Duration, started time.Time) *idleReader {
	return &idleReader{from: from, after: after, now: time.Now, last: started}
}

// watch arms the timer that ends a read blocked on a silent
// connection. stop is what cuts the request off, which is the
// forward's own cancel.
func (r *idleReader) watch(stop func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timer = time.AfterFunc(r.after, func() { r.expire(stop) })
	r.rearm()
}

// expire ends the stream, unless a block arrived while the timer was
// running, in which case the bound starts again from that block.
func (r *idleReader) expire(stop func()) {
	r.mu.Lock()
	if r.now().Sub(r.last) < r.after {
		r.rearm()
		r.mu.Unlock()
		return
	}
	r.fired = true
	r.mu.Unlock()
	stop()
}

// rearm sets the timer to the moment the current quiet would reach the
// bound. The caller holds the lock.
func (r *idleReader) rearm() {
	if r.timer == nil {
		return
	}
	remaining := r.after - r.now().Sub(r.last)
	if remaining < 0 {
		remaining = 0
	}
	r.timer.Reset(remaining)
}

// stopWatching releases the timer when the stream ends for any reason.
func (r *idleReader) stopWatching() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
}

// Read answers what the stream delivered, and reports the idle end as
// the error the log line carries.
func (r *idleReader) Read(into []byte) (int, error) {
	read, err := r.from.Read(into)
	r.mu.Lock()
	defer r.mu.Unlock()
	if read > 0 {
		r.last = r.now()
		r.rearm()
		return read, err
	}
	if r.fired || r.now().Sub(r.last) > r.after {
		return read, fmt.Errorf("the capture container sent nothing for %s", r.after)
	}
	return read, err
}

// relayed is the set of headers the API copies from a problem the
// container answered with. On a 200 the API writes the type and the
// cache directives itself, because it chose the representation in
// the negotiation, so nothing of the container's is copied onto it.
// On a problem the container's own answer is what the caller gets,
// and Retry-After is the one field whose meaning the API cannot
// derive on its own.
var relayed = []string{"Content-Type", "Cache-Control", "Accept-Ranges", "Retry-After"}

func relayHeaders(from, to http.Header) {
	for _, name := range relayed {
		if value := from.Get(name); value != "" {
			to.Set(name, value)
		}
	}
}
