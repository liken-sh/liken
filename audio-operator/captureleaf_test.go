package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// writeLeaf writes a leaf certificate, its key, and its CA into a
// directory as plain files.
func writeLeaf(t *testing.T, directory string) {
	t.Helper()
	authority, err := mintAuthority(time.Now())
	if err != nil {
		t.Fatalf("minting the CA: %v", err)
	}
	pair, err := authority.mintLeaf(captureAudience, []string{captureAudience}, time.Now())
	if err != nil {
		t.Fatalf("minting the leaf: %v", err)
	}
	for name, body := range map[string][]byte{
		tlsCertFile: pair.Certificate,
		tlsKeyFile:  pair.Key,
		tlsCABundle: authority.Pair.Certificate,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), body, 0o600); err != nil {
			t.Fatalf("writing the leaf's files: %v", err)
		}
	}
}

// publishLeaf writes a leaf into a directory the way the kubelet
// updates a Secret volume. It writes the files into a new directory,
// points a symlink named ..data_tmp at it, and renames that symlink
// onto ..data. The visible file names are symlinks through ..data, so
// the rename replaces every file at once.
func publishLeaf(t *testing.T, directory string, generation int) {
	t.Helper()
	payload := fmt.Sprintf("..generation_%d", generation)
	if err := os.Mkdir(filepath.Join(directory, payload), 0o755); err != nil {
		t.Fatalf("making the payload directory: %v", err)
	}
	writeLeaf(t, filepath.Join(directory, payload))
	staged := filepath.Join(directory, "..data_tmp")
	if err := os.Symlink(payload, staged); err != nil {
		t.Fatalf("linking ..data_tmp: %v", err)
	}
	if err := os.Rename(staged, filepath.Join(directory, "..data")); err != nil {
		t.Fatalf("renaming ..data_tmp onto ..data: %v", err)
	}
	for _, name := range []string{tlsCertFile, tlsKeyFile, tlsCABundle} {
		visible := filepath.Join(directory, name)
		if _, err := os.Lstat(visible); err == nil {
			continue
		}
		if err := os.Symlink(filepath.Join("..data", name), visible); err != nil {
			t.Fatalf("linking the visible name: %v", err)
		}
	}
}

// watchLeaf runs the watch until the test ends, with a fallback
// interval of one hour, so only an update of the volume can trigger a
// read during a test. The returned channel receives after each read.
func watchLeaf(t *testing.T, directory string) (*leaf, chan struct{}) {
	t.Helper()
	return watchLeafEvery(t, directory, time.Hour, func(err error) { t.Errorf("starting the inotify watch: %v", err) })
}

func watchLeafEvery(t *testing.T, directory string, fallback time.Duration,
	complain func(error)) (*leaf, chan struct{}) {
	t.Helper()
	tlsLeaf := newLeaf(directory)
	tlsLeaf.fallbackInterval = fallback
	reads := make(chan struct{}, 64)
	done := make(chan struct{})
	finished := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		<-finished
	})
	go func() {
		defer close(finished)
		tlsLeaf.watchWith(done, func(_ bool, err error) {
			if err != nil {
				t.Errorf("reloading the leaf: %v", err)
			}
			select {
			case reads <- struct{}{}:
			default:
			}
		}, complain)
	}()
	return tlsLeaf, reads
}

// next waits up to five seconds for a value on a channel. The inotify
// watch uses a real file descriptor, so these tests use the real
// clock.
func next[T any](t *testing.T, from chan T, what string) T {
	t.Helper()
	select {
	case got := <-from:
		return got
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s within five seconds", what)
		var none T
		return none
	}
}

// readsUntil waits until the leaf has been loaded loads times. An
// update of the volume can trigger more than one read, so it counts
// loads and not reads.
func readsUntil(t *testing.T, tlsLeaf *leaf, reads chan struct{}, loads int) {
	t.Helper()
	for tlsLeaf.loadCount() < loads {
		next(t, reads, fmt.Sprintf("read for load %d of the files", loads))
	}
}

func TestTheLeafIsReadWhenTheKubeletSwapsTheVolume(t *testing.T) {
	directory := t.TempDir()
	tlsLeaf, reads := watchLeaf(t, directory)
	next(t, reads, "first read")
	if tlsLeaf.loaded() {
		t.Fatal("an empty volume read as a leaf")
	}

	publishLeaf(t, directory, 1)
	readsUntil(t, tlsLeaf, reads, 1)
	first, _ := tlsLeaf.certificate(nil)

	publishLeaf(t, directory, 2)
	readsUntil(t, tlsLeaf, reads, 2)
	second, _ := tlsLeaf.certificate(nil)

	if string(first.Certificate[0]) == string(second.Certificate[0]) {
		t.Error("the new leaf was not served")
	}
}

// While the inotify watch works, the volume is read after an update
// and at no other time. The fallback interval is one millisecond, so a
// timer that read the volume would read it many times during the wait.
func TestAWatchedVolumeIsNotReadOnATimer(t *testing.T) {
	_, reads := watchLeafEvery(t, t.TempDir(), time.Millisecond, func(err error) { t.Errorf("starting the inotify watch: %v", err) })
	next(t, reads, "first read")
	select {
	case <-reads:
		t.Error("the volume was read again with no swap")
	case <-time.After(200 * time.Millisecond):
	}
}

// The directory does not exist, so the inotify watch cannot start. The
// synctest bubble lets the test wait leafFallback without a real wait.
func TestAVolumeWithNoWatchIsReadOnTheFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "not-mounted")
		var complaints atomic.Int64
		_, reads := watchLeafEvery(t, missing, leafFallback, func(error) { complaints.Add(1) })
		<-reads
		for range 3 {
			start := time.Now()
			<-reads
			if waited := time.Since(start); waited != leafFallback {
				t.Errorf("the volume was read again after %v, want %v", waited, leafFallback)
			}
		}
		if got := complaints.Load(); got != 4 {
			t.Errorf("the watch reported %d errors, want 4, one for each read", got)
		}
	})
}

func TestADirectoryWithNoSecretStillCompletesAHandshake(t *testing.T) {
	// Before audio-api writes the Secret, the listener must still
	// complete handshakes, or the liveness probe on /healthz fails and
	// the kubelet restarts the container.
	tlsLeaf := newLeaf(t.TempDir())
	if err := tlsLeaf.reload(); err != nil {
		t.Fatalf("reloading with no Secret returned %v, want nil", err)
	}
	if tlsLeaf.loaded() {
		t.Error("a directory with no files loaded a leaf, want none")
	}
	fallback, err := tlsLeaf.certificate(nil)
	if err != nil || fallback == nil {
		t.Fatalf("a handshake before the Secret arrives got certificate %v and error %v, want a self-signed certificate", fallback, err)
	}
	// A probe or a scrape must not generate a key pair on every
	// request.
	again, err := tlsLeaf.certificate(nil)
	if err != nil || again != fallback {
		t.Errorf("the self-signed certificate was minted again: got %v and error %v, want the first one", again, err)
	}
}

func TestTheSecretsLeafReplacesTheSelfSignedCertificate(t *testing.T) {
	directory := t.TempDir()
	tlsLeaf := newLeaf(directory)
	fallback, err := tlsLeaf.certificate(nil)
	if err != nil {
		t.Fatalf("minting the self-signed certificate: %v", err)
	}
	writeLeaf(t, directory)
	if err := tlsLeaf.reload(); err != nil {
		t.Fatalf("reloading the leaf: %v", err)
	}
	if !tlsLeaf.loaded() {
		t.Fatal("the API's leaf was not read")
	}
	served, err := tlsLeaf.certificate(nil)
	if err != nil || served == fallback {
		t.Error("the container kept serving its own certificate after the Secret arrived")
	}
}

func TestTheLeafIsReadOnceTheSecretArrives(t *testing.T) {
	directory := t.TempDir()
	tlsLeaf := newLeaf(directory)
	if err := tlsLeaf.reload(); err != nil {
		t.Fatalf("reloading the empty directory: %v", err)
	}

	writeLeaf(t, directory)
	if err := tlsLeaf.reload(); err != nil {
		t.Fatalf("reading the Secret: %v", err)
	}
	if !tlsLeaf.loaded() {
		t.Fatal("the leaf was not read")
	}
	pair, err := tlsLeaf.certificate(nil)
	if err != nil || pair == nil {
		t.Fatalf("the handshake got certificate %v and error %v, want the Secret's leaf", pair, err)
	}
}

func TestAnUnchangedSecretIsNotReadAgain(t *testing.T) {
	directory := t.TempDir()
	writeLeaf(t, directory)
	tlsLeaf := newLeaf(directory)
	for range 5 {
		if err := tlsLeaf.reload(); err != nil {
			t.Fatalf("reloading the leaf: %v", err)
		}
	}
	if tlsLeaf.loadCount() != 1 {
		t.Errorf("the files were read %d times, want 1", tlsLeaf.loadCount())
	}
}

func TestAChangedSecretIsLoadedAgain(t *testing.T) {
	directory := t.TempDir()
	writeLeaf(t, directory)
	tlsLeaf := newLeaf(directory)
	if err := tlsLeaf.reload(); err != nil {
		t.Fatalf("reloading the leaf: %v", err)
	}
	first, _ := tlsLeaf.certificate(nil)

	// The digest in the fingerprint detects this change even when the
	// filesystem's timestamps do not.
	writeLeaf(t, directory)
	if err := tlsLeaf.reload(); err != nil {
		t.Fatalf("reloading the changed leaf: %v", err)
	}
	if tlsLeaf.loadCount() != 2 {
		t.Errorf("the files were read %d times, want 2", tlsLeaf.loadCount())
	}
	second, _ := tlsLeaf.certificate(nil)
	if string(first.Certificate[0]) == string(second.Certificate[0]) {
		t.Error("the new leaf was not served")
	}
}

func TestASecretThatIsNotAKeyPairIsAFailure(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{tlsCertFile, tlsKeyFile} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("not pem"), 0o600); err != nil {
			t.Fatalf("writing the files that are not PEM: %v", err)
		}
	}
	tlsLeaf := newLeaf(directory)
	if err := tlsLeaf.reload(); err == nil {
		t.Fatal("a Secret that is not a key pair was read")
	}
	if tlsLeaf.loaded() {
		t.Error("a Secret that is not a key pair was served")
	}
}

func TestARemovedCertificateFileUnloadsTheLeaf(t *testing.T) {
	directory := t.TempDir()
	writeLeaf(t, directory)
	tlsLeaf := newLeaf(directory)
	if err := tlsLeaf.reload(); err != nil {
		t.Fatalf("reloading the leaf: %v", err)
	}
	if err := os.Remove(filepath.Join(directory, tlsCertFile)); err != nil {
		t.Fatalf("removing the certificate file: %v", err)
	}
	if err := tlsLeaf.reload(); err != nil {
		t.Fatalf("reloading the leaf after the removal: %v", err)
	}
	if tlsLeaf.loaded() {
		t.Error("the leaf is still loaded after its certificate file was removed, want none")
	}
}

func TestTheReadinessGaugeFollowsTheLeaf(t *testing.T) {
	directory := t.TempDir()
	readings := newCaptureMetrics("dev")
	tlsLeaf := newLeaf(directory)

	done := make(chan struct{})
	close(done)
	tlsLeaf.watch(done, readings, func(error) {})
	if testutil.ToFloat64(readings.ready) != 0 {
		t.Error("audio_capture_ready is one with no Secret")
	}

	writeLeaf(t, directory)
	tlsLeaf.watch(done, readings, func(error) {})
	if testutil.ToFloat64(readings.ready) != 1 {
		t.Error("audio_capture_ready is zero with a Secret in place")
	}
}
