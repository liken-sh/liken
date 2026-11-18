package main

// The capture listener reads its server certificate from a Secret
// volume, and reloads it when the kubelet updates the volume.
// The listener uses HTTPS because a microphone's audio must not cross
// the pod network unencrypted, and a plain listener exposes the bearer
// token to replay.
//
// audio-api writes the leaf certificate to the audio-capture-server
// Secret after this DaemonSet is applied, so the Secret is missing at
// the first rollout. The volume is optional, so the pod starts with an
// empty directory and does not wait in ContainerCreating.
//
// Until the files appear, the listener serves a self-signed certificate.
// The kubelet does not verify the certificate of an HTTPS probe, so the
// liveness probe on /healthz still succeeds, and the container is not
// restarted again and again when audio-api is late or not installed.
// audio-api trusts only the capture CA, so it refuses the self-signed
// certificate and returns 503 with the reason, and /readyz returns 503.

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

const captureTLSDirVariable = "CAPTURE_TLS_DIR"

const (
	// captureTLSDir is used when captureTLSDirVariable is not set.
	captureTLSDir = "/var/run/audio-capture-tls"
	tlsCertFile   = "tls.crt"
	tlsKeyFile    = "tls.key"
	tlsCABundle   = "ca.crt"
	// leafFallback is how often the container reads the directory while
	// it cannot start an inotify watch on it, as on a node whose
	// fs.inotify.max_user_instances is used up. Each read tries the
	// watch again, and the timer runs only while the watch fails.
	leafFallback = 10 * time.Minute
)

// ErrNoCertificate is the text of /readyz before the Secret's leaf is
// loaded, and the error of a handshake when the container cannot
// generate a self-signed certificate.
var ErrNoCertificate = errors.New("no server certificate is loaded yet")

// leaf keeps the listener's server certificate in memory. It watches
// the files of the volume and not the Secret in the API server, because
// the kubelet updates the files some time after the Secret changes, and
// the listener serves what the files contain. The ServiceAccount needs
// no permission on the Secret.
type leaf struct {
	directory string

	// now sets the validity of the self-signed certificate. A test
	// replaces it.
	now func() time.Time

	// fallbackInterval is leafFallback outside tests.
	fallbackInterval time.Duration

	mu                sync.RWMutex
	secretCert        *tls.Certificate
	loadedFingerprint string
	reloads           int
	selfSignedCert    *tls.Certificate
}

func newLeaf(directory string) *leaf {
	return &leaf{directory: directory, now: time.Now, fallbackInterval: leafFallback}
}

// certificate is the listener's GetCertificate hook. It returns the
// Secret's leaf when one is loaded, and otherwise the self-signed
// certificate.
func (l *leaf) certificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	l.mu.RLock()
	secretCert, selfSignedCert := l.secretCert, l.selfSignedCert
	l.mu.RUnlock()
	if secretCert != nil {
		return secretCert, nil
	}
	if selfSignedCert != nil {
		return selfSignedCert, nil
	}
	return l.mintSelfSigned()
}

// mintSelfSigned creates the self-signed certificate on the first
// handshake before the Secret's leaf is loaded, and keeps it, so later
// handshakes generate no keys.
func (l *leaf) mintSelfSigned() (*tls.Certificate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.secretCert != nil {
		return l.secretCert, nil
	}
	if l.selfSignedCert != nil {
		return l.selfSignedCert, nil
	}
	pair, err := selfSigned(captureAudience, l.now())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoCertificate, err)
	}
	l.selfSignedCert = &pair
	return l.selfSignedCert, nil
}

// loaded reports whether the Secret's leaf is in memory, for
// audio_capture_ready and /readyz. The self-signed certificate does not
// count, because audio-api does not trust it.
func (l *leaf) loaded() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.secretCert != nil
}

// loadCount returns how many times the files have been loaded. Tests
// read it.
func (l *leaf) loadCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.reloads
}

// reload loads the files when they have changed since the last load.
// When the files are missing, it discards the loaded leaf and returns
// no error, because the directory is empty until audio-api writes the
// Secret.
func (l *leaf) reload() error {
	fingerprint, found := l.fingerprint()
	if !found {
		l.mu.Lock()
		l.secretCert, l.loadedFingerprint = nil, ""
		l.mu.Unlock()
		return nil
	}
	l.mu.RLock()
	unchanged := fingerprint == l.loadedFingerprint
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
	l.secretCert, l.loadedFingerprint, l.reloads = &pair, fingerprint, l.reloads+1
	l.mu.Unlock()
	return nil
}

// fingerprint returns a string that changes when the certificate or
// key file changes, or false when a file cannot be read. It includes a
// SHA-256 digest of the certificate because two writes within one tick
// of the filesystem's modification time can have the same size and
// time.
func (l *leaf) fingerprint() (string, bool) {
	var fingerprint string
	for _, name := range []string{tlsCertFile, tlsKeyFile} {
		path := filepath.Join(l.directory, name)
		info, err := os.Stat(path)
		if err != nil {
			return "", false
		}
		fingerprint += fmt.Sprintf("%s:%d:%d;", name, info.Size(), info.ModTime().UnixNano())
	}
	body, err := os.ReadFile(filepath.Join(l.directory, tlsCertFile))
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(body)
	return fingerprint + hex.EncodeToString(sum[:]), true
}

// watch reloads the leaf now and after each update of the volume until
// done is closed, and records each result in readings.
func (l *leaf) watch(done <-chan struct{}, readings *captureMetrics, complain func(error)) {
	l.watchWith(done, func(ready bool, err error) {
		if err != nil {
			complain(err)
			readings.failed(failureCertificate)
		}
		readings.readiness(ready)
	}, complain)
}

// watchWith is watch with a callback for each read, so a test can see
// every read. complain receives the error when the inotify watch cannot
// start, which is not a failure of the certificate.
//
// The watch is armed before each read, so an update during the read
// triggers another read and is not lost.
func (l *leaf) watchWith(done <-chan struct{}, report func(ready bool, err error), complain func(error)) {
	swaps := newVolumeSwaps(l.directory, tlsCertFile, tlsKeyFile)
	defer swaps.close()
	for {
		var rereadTimer <-chan time.Time
		if err := swaps.arm(); err != nil {
			complain(fmt.Errorf("watching %s for the kubelet's updates: %w", l.directory, err))
			rereadTimer = time.After(l.fallbackInterval)
		}
		err := l.reload()
		report(l.loaded(), err)
		select {
		case <-done:
			return
		case <-swaps.swapped:
		case <-rereadTimer:
		}
	}
}

// tlsConfig reads the certificate from the leaf on every handshake, so
// a reload needs no listener restart.
func (l *leaf) tlsConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: l.certificate,
		MinVersion:     tls.VersionTLS12,
	}
}
