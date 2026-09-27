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
// path. The three file names are the kubernetes.io/tls shape's own.
//
// leafFallback is the longest the container goes without reading the
// directory. The kubelet's swap of the volume starts every read that
// matters, so the fallback reads only when the inotify watch could not
// start: a node whose fs.inotify.max_user_instances is used up refuses
// a new watch, and the container then still takes a new leaf within
// this bound.
const (
	captureTLSDir = "/var/run/audio-capture-tls"
	tlsCertFile   = "tls.crt"
	tlsKeyFile    = "tls.key"
	tlsCABundle   = "ca.crt"
	leafFallback  = 10 * time.Minute
)

// ErrNoCertificate is what /readyz reports before the Secret arrives,
// and what a listener with no certificate at all fails a handshake
// with.
var ErrNoCertificate = errors.New("the capture container holds no server certificate yet")

// leaf holds the certificate and reloads it when the files change.
//
// The reload watches the volume rather than the API server. The
// kubelet updates a Secret volume on its own sync period after the
// Secret changes, so the volume's swap is the event, and watching the
// volume needs no grant on the Secret.
type leaf struct {
	directory string

	// now is a field so a test drives the reload on its own clock.
	now func() time.Time

	// rereadAfter is a field so a test sets it to an hour, and only
	// the volume's swap can start a read in time.
	rereadAfter time.Duration

	mu       sync.RWMutex
	held     *tls.Certificate
	stamps   string
	reloads  int
	fallback *tls.Certificate
}

func newLeaf(directory string) *leaf {
	return &leaf{directory: directory, now: time.Now, rereadAfter: leafFallback}
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

// watch reads the files now and on every swap of the volume, until
// the run ends, and reports each read on the capture metrics.
func (l *leaf) watch(done <-chan struct{}, readings *captureMetrics, complain func(error)) {
	l.watchWith(done, func(ready bool, err error) {
		if err != nil {
			complain(err)
			readings.failed(failureCertificate)
		}
		readings.readiness(ready)
	}, complain)
}

// watchWith is watch with the report of each read as a function, so a
// test sees every read. complain carries a watch that could not start,
// which is not a failure of the certificate.
//
// The watch is armed before each read, so a swap that lands during
// the read starts one more read and is not lost.
func (l *leaf) watchWith(done <-chan struct{}, report func(ready bool, err error), complain func(error)) {
	swaps := newVolumeSwaps(l.directory, tlsCertFile, tlsKeyFile)
	defer swaps.close()
	fallback := time.NewTimer(l.rereadAfter)
	defer fallback.Stop()
	for {
		if err := swaps.arm(); err != nil {
			complain(fmt.Errorf("watching %s for the kubelet's updates: %w", l.directory, err))
		}
		err := l.reload()
		report(l.loaded(), err)
		select {
		case <-done:
			return
		case <-swaps.swapped:
		case <-fallback.C:
		}
		fallback.Reset(l.rereadAfter)
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
