package main

// The server certificate the capture listener serves, reloaded when
// the file on disk changes.
//
// The private leg is HTTPS because sound from a room's microphone
// must not cross the pod network in the clear, and because a bearer
// token on a plain listener is replayable by anything on the path.
//
// The Secret volume is optional. The API mints the leaf into
// audio-capture-server after this DaemonSet is applied, so the Secret
// does not exist at the first rollout. An optional volume holds
// nothing rather than parking the pod in ContainerCreating, and it
// means the DaemonSet's ServiceAccount needs no get and no watch on
// the Secret.
//
// Before the file arrives the listener serves a certificate this
// container signed itself. The listener answers, so the liveness
// probe on /healthz passes instead of restarting the container every
// three minutes while the API has yet to mint the leaf, and an owner
// who runs the DaemonSet without audio-api gets a container that
// stays up.
//
// A tap gets nothing from that certificate. The API anchors on the
// domain's CA alone, so it refuses the handshake, audio_capture_ready
// is 0, /readyz is 503, and the API answers 503 with the reason.

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// captureTLSDirVariable names the directory the Secret volume mounts.
const captureTLSDirVariable = "CAPTURE_TLS_DIR"

// captureTLSDir is where the leaf lands when nothing names another
// path. The three file names are the kubernetes.io/tls shape's own,
// and leafPollPeriod is how often the directory is read.
const (
	captureTLSDir  = "/var/run/audio-capture-tls"
	tlsCertFile    = "tls.crt"
	tlsKeyFile     = "tls.key"
	tlsCABundle    = "ca.crt"
	leafPollPeriod = 10 * time.Second
)

// ErrNoCertificate is what /readyz reports before the Secret arrives,
// and what a listener with no certificate at all fails a handshake
// with.
var ErrNoCertificate = errors.New("the capture container holds no server certificate yet")

// leaf holds the certificate and reloads it when the files change.
//
// The reload watches the file rather than the API server. The kubelet
// refreshes a Secret volume on its own period, so the file is the
// event, and watching it needs no grant on the Secret.
type leaf struct {
	directory string

	// now is a field so a test drives the reload on its own clock.
	now func() time.Time

	mu       sync.RWMutex
	held     *tls.Certificate
	stamps   string
	reloads  int
	fallback *tls.Certificate
}

func newLeaf(directory string) *leaf {
	return &leaf{directory: directory, now: time.Now}
}

// certificate is what the TLS listener calls on every handshake. The
// leaf from the Secret comes first. Before the Secret arrives the
// answer is this container's own self-signed certificate, so the
// liveness probe completes its handshake while the API, which trusts
// the domain's CA alone, refuses it and reports 503 with the reason.
func (l *leaf) certificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	l.mu.RLock()
	held, standing := l.held, l.fallback
	l.mu.RUnlock()
	if held != nil {
		return held, nil
	}
	if standing != nil {
		return standing, nil
	}
	return l.mintFallback()
}

// mintFallback makes this container's own certificate once, the first
// time a handshake arrives with no Secret in place. It is kept for
// the life of the process, so every later handshake before the
// Secret costs no key generation.
func (l *leaf) mintFallback() (*tls.Certificate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held != nil {
		return l.held, nil
	}
	if l.fallback != nil {
		return l.fallback, nil
	}
	pair, err := selfSigned(captureAudience, l.now())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoCertificate, err)
	}
	l.fallback = &pair
	return l.fallback, nil
}

// loaded reports whether the leaf from the Secret is in memory, which
// is what audio_capture_ready carries and what /readyz answers. The
// self-signed fallback does not count: no client trusts it.
func (l *leaf) loaded() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.held != nil
}

// reloaded is how many times the files on disk have been read into
// memory, which is what a test reads to prove a changed Secret is
// picked up without a restart.
func (l *leaf) reloaded() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.reloads
}

// reload reads the files when they have changed since the last read. A
// directory with no files is not a failure: it is the state before the
// API has minted the leaf.
func (l *leaf) reload() error {
	stamps, found := l.stamp()
	if !found {
		l.mu.Lock()
		l.held, l.stamps = nil, ""
		l.mu.Unlock()
		return nil
	}
	l.mu.RLock()
	unchanged := stamps == l.stamps
	l.mu.RUnlock()
	if unchanged {
		return nil
	}
	pair, err := tls.LoadX509KeyPair(
		filepath.Join(l.directory, tlsCertFile),
		filepath.Join(l.directory, tlsKeyFile))
	if err != nil {
		return fmt.Errorf("reading the capture leaf from %s: %w", l.directory, err)
	}
	l.mu.Lock()
	l.held, l.stamps, l.reloads = &pair, stamps, l.reloads+1
	l.mu.Unlock()
	return nil
}

// stamp is what says the files changed: a digest of the certificate
// beside the size and modification time of each file.
//
// The digest is there because the time alone is not enough. A
// filesystem's modification time has a granularity of its own, and
// two writes inside one tick would otherwise read as no change at
// all.
func (l *leaf) stamp() (string, bool) {
	var stamp string
	for _, name := range []string{tlsCertFile, tlsKeyFile} {
		path := filepath.Join(l.directory, name)
		info, err := os.Stat(path)
		if err != nil {
			return "", false
		}
		stamp += fmt.Sprintf("%s:%d:%d;", name, info.Size(), info.ModTime().UnixNano())
	}
	body, err := os.ReadFile(filepath.Join(l.directory, tlsCertFile))
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(body)
	return stamp + hex.EncodeToString(sum[:]), true
}

// watch reads the files now and on every tick, until the run ends.
//
// This is a poll and not an inotify watch. The file arrives once and
// changes about once a year, one poll costs two stat calls and one
// read, and the kubelet delivers a Secret update as a symlink swap
// that an inotify watch on the file itself would miss, so the watch
// would need this same poll behind it.
func (l *leaf) watch(done <-chan struct{}, readings *captureMetrics, complain func(error)) {
	tick := time.NewTicker(leafPollPeriod)
	defer tick.Stop()
	for {
		if err := l.reload(); err != nil {
			complain(err)
			readings.failed(failureCertificate)
		}
		readings.readiness(l.loaded())
		select {
		case <-done:
			return
		case <-tick.C:
		}
	}
}

// tlsConfig is what the capture listener serves with: the leaf from
// memory on every handshake, and TLS 1.2 as the floor, which is what
// Go's own default and every client in this cluster take.
func (l *leaf) tlsConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: l.certificate,
		MinVersion:     tls.VersionTLS12,
	}
}
