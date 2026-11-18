package main

// The request from the API to the capture container on one node. The
// caller's request to the API is the public leg, and the API's request
// to the container is the private leg.
//
// The API authenticates with a ServiceAccount token that the kubelet
// projects for the audience audio-capture. It reads the token file on
// every request, because the kubelet rewrites the file when it
// refreshes the token.
//
// The API dials the pod's IP address and verifies the container's
// certificate for the name audio-capture against the API's own CA, so
// another process that listens on that address cannot pass as the
// container.

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
	"syscall"
	"time"
)

// capturePort is the containerPort named capture in the operator's
// DaemonSet.
const capturePort = 9201

const captureTokenVariable = "CAPTURE_TOKEN_FILE"

// captureTokenFile is the mount path of the projected token in
// deploy/api.yaml.
const captureTokenFile = "/var/run/secrets/audio.liken.sh/capture/token"

// headerTimeout is how long the container has to send its response
// headers, and forward adds the tap's begin offset to it. idleTimeout
// is how long a body may send nothing before the API ends it. A body
// that keeps sending has no limit, because an open-ended tap runs until
// the caller closes it.
const (
	headerTimeout = 10 * time.Second
	idleTimeout   = 30 * time.Second
)

// The errors of forward. relayFailure returns ErrCaptureRefused as a
// 503, ErrCaptureMalformed as a 502, and ErrCaptureTimeout as a 504.
//
// forward picks the error by how far the request got, not by the error's
// text, because Go's transport reports a server that writes garbage and
// closes as a malformed response, a peek failure, or an unexpected EOF,
// depending on when the close arrives. A connection that was never made
// and a certificate that the API's CA did not sign are ErrCaptureRefused,
// because the container is still starting or has no certificate yet, and
// both clear on their own.
var (
	ErrCaptureRefused   = errors.New("the capture container refused the connection")
	ErrCaptureMalformed = errors.New("the API cannot read the capture container's response")
	ErrCaptureTimeout   = errors.New("the capture container sent no headers in time")
)

type forwarder struct {
	// anchor returns the PEM of the CA that signs the container's
	// certificate. transport calls it on each request, because the API
	// mints a new CA when the old one is missing or expiring.
	anchor func() []byte

	tokenFile string

	// A test sets scheme, address, and dial to point the forwarder at an
	// httptest server. address replaces the pod's IP and port.
	port    int
	scheme  string
	address string
	dial    func(ctx context.Context, network, address string) (net.Conn, error)

	mu       sync.Mutex
	clientCA string
	client   *http.Client
}

func newForwarder(anchor func() []byte) *forwarder {
	file := os.Getenv(captureTokenVariable)
	if file == "" {
		file = captureTokenFile
	}
	return &forwarder{anchor: anchor, tokenFile: file, port: capturePort}
}

// transport returns the HTTP client for the current CA and builds a new
// one when the CA changes. Each client keeps its connection pool, so
// taps on the same node reuse a connection and its TLS handshake.
func (f *forwarder) transport() (*http.Client, error) {
	f.mu.Lock()
	cached := f.client
	clientCA := f.clientCA
	f.mu.Unlock()
	if f.anchor == nil {
		if cached == nil {
			return nil, ErrNoCertificate
		}
		return cached, nil
	}
	anchor := string(f.anchor())
	if anchor == "" {
		return nil, ErrNoCertificate
	}
	if cached != nil && clientCA == anchor {
		return cached, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Another request can build the client while this one reads the
	// anchor without the lock.
	if f.client != nil && f.clientCA == anchor {
		return f.client, nil
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(anchor)) {
		return nil, errors.New("the capture CA's PEM contains no certificates")
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
				// The API dials an IP address, and the container's
				// certificate is for the name audio-capture.
				ServerName: captureAudience,
				MinVersion: tls.VersionTLS12,
			},
			// The client has no whole-request timeout, because a tap
			// streams for as long as the caller reads it.
			IdleConnTimeout: 60 * time.Second,
		},
	}
	f.clientCA = anchor
	return f.client, nil
}

// forward sends a GET to the container on pod and returns its response.
// The caller closes the response body, which also cancels the request's
// context.
func (f *forwarder) forward(ctx context.Context, pod capturePod,
	path, rawQuery string, begin time.Duration) (*http.Response, error) {
	client, err := f.transport()
	if err != nil {
		return nil, err
	}
	token, err := os.ReadFile(f.tokenFile)
	if err != nil {
		return nil, fmt.Errorf("reading the capture token: %w", err)
	}

	target := f.origin(pod) + path
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))

	// A deadline on the request context would also cut off the body, so
	// a timer cancels the request when the headers are late, and forward
	// stops the timer when client.Do returns. The timeout includes begin
	// so that a tap with a begin offset is not cut off before the
	// container responds.
	headers, cancel := context.WithCancel(ctx)
	bound := headerTimeout + begin
	deadline := time.AfterFunc(bound, cancel)
	response, err := client.Do(request.WithContext(headers))
	// The timer caused the error only when it already fired and the
	// caller's context is still live.
	expired := !deadline.Stop() && ctx.Err() == nil
	if err != nil {
		cancel()
		switch {
		case expired:
			return nil, fmt.Errorf("%w within %s: %w", ErrCaptureTimeout, bound, err)
		case !reachable(err), !trusted(err), ctx.Err() != nil:
			return nil, fmt.Errorf("%w: %w", ErrCaptureRefused, err)
		default:
			// The connection was made, and the response could not be
			// read.
			return nil, fmt.Errorf("%w: %w", ErrCaptureMalformed, err)
		}
	}
	response.Body = &cancellingBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

// reachable reports whether the connection to the container was made.
// Go reports a failed dial as a net.OpError with Op "dial". The errno
// values are what the kernel returns when nothing listens on the port,
// when the node is gone, and when the pod network is not up.
func reachable(err error) bool {
	var operation *net.OpError
	if errors.As(err, &operation) && operation.Op == "dial" {
		return false
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED),
		errors.Is(err, syscall.EHOSTUNREACH),
		errors.Is(err, syscall.ENETUNREACH):
		return false
	}
	return true
}

// trusted reports whether the container's certificate chains to the
// API's CA. A listener that does not speak TLS is untrusted.
//
// Until the Secret audio-capture-server reaches the container, the
// container serves a self-signed certificate. That clears when the API
// mints the certificate and the kubelet updates the volume, so forward
// returns ErrCaptureRefused, a 503 with Retry-After, and not a 502.
func trusted(err error) bool {
	var unverified *tls.CertificateVerificationError
	if errors.As(err, &unverified) {
		return false
	}
	var record tls.RecordHeaderError
	return !errors.As(err, &record)
}

func (f *forwarder) origin(pod capturePod) string {
	scheme := f.scheme
	if scheme == "" {
		scheme = "https"
	}
	if f.address != "" {
		return scheme + "://" + f.address
	}
	return scheme + "://" + net.JoinHostPort(pod.IP, strconv.Itoa(f.port))
}

// cancellingBody cancels the request's context when the caller closes
// the body, so the context does not outlive the request.
type cancellingBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancellingBody) Close() error {
	b.cancel()
	return b.ReadCloser.Close()
}

// idleReader ends a body that has sent nothing for after. The silence
// counts from the last byte read or, before the first byte, from the
// time given to newIdleReader. serveTap gives the end of the begin
// offset, so the timeout cannot fire while the container discards.
//
// A read on a silent connection blocks inside the transport, so Read
// alone cannot end it. The timer that watch starts cancels the request,
// and the blocked read then returns.
type idleReader struct {
	from  io.Reader
	after time.Duration

	// Tests replace now to move the clock.
	now func() time.Time

	mu    sync.Mutex
	last  time.Time
	timer *time.Timer
	fired bool
}

func newIdleReader(from io.Reader, after time.Duration, started time.Time) *idleReader {
	return &idleReader{from: from, after: after, now: time.Now, last: started}
}

// watch starts a timer that calls stop, the cancel function of the
// request, when the body has been silent for r.after.
func (r *idleReader) watch(stop func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timer = time.AfterFunc(r.after, func() { r.expire(stop) })
	r.rearm()
}

// expire calls stop when the body is still silent. A byte can arrive
// after the timer fires and before expire takes the lock, so expire
// checks the silence again and resets the timer when it is too short.
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

// rearm sets the timer to fire when the current silence reaches
// r.after. The caller holds r.mu.
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

func (r *idleReader) stopWatching() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
}

// Read reads from the body. When it reads no bytes after the timer
// fired, or after a silence longer than r.after, it returns an error
// that names the timeout in place of the body's error.
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

// relayed lists the headers that relayHeaders copies from a container's
// problem response. The API cannot derive the container's Retry-After
// on its own. On a 200, serveTap copies none of the container's headers
// and sets its own.
var relayed = []string{"Content-Type", "Cache-Control", "Accept-Ranges", "Retry-After"}

func relayHeaders(from, to http.Header) {
	for _, name := range relayed {
		if value := from.Get(name); value != "" {
			to.Set(name, value)
		}
	}
}
