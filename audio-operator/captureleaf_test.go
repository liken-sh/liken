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

// writeLeaf puts a signed leaf into a directory the way the kubelet
// puts a Secret volume there.
func writeLeaf(t *testing.T, directory string) {
	t.Helper()
	authority, err := mintAuthority(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := authority.mintLeaf(captureAudience, []string{captureAudience}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{
		tlsCertFile: pair.Certificate,
		tlsKeyFile:  pair.Key,
		tlsCABundle: authority.Pair.Certificate,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// publishLeaf puts a signed leaf into a directory the way the kubelet
// updates a Secret volume: the files go into a new directory, a
// symlink ..data_tmp names it, and a rename moves that symlink onto
// ..data. The visible names are symlinks through ..data, so the rename
// swaps every file at once.
func publishLeaf(t *testing.T, directory string, generation int) {
	t.Helper()
	payload := fmt.Sprintf("..generation_%d", generation)
	if err := os.Mkdir(filepath.Join(directory, payload), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLeaf(t, filepath.Join(directory, payload))
	staged := filepath.Join(directory, "..data_tmp")
	if err := os.Symlink(payload, staged); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, filepath.Join(directory, "..data")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{tlsCertFile, tlsKeyFile, tlsCABundle} {
		visible := filepath.Join(directory, name)
		if _, err := os.Lstat(visible); err == nil {
			continue
		}
		if err := os.Symlink(filepath.Join("..data", name), visible); err != nil {
			t.Fatal(err)
		}
	}
}

// watchLeaf runs the watch until the test ends, with a fallback of an
// hour, so only the kubelet's swap can bring a change in time. Every
// read is reported on the channel.
func watchLeaf(t *testing.T, directory string) (*leaf, chan struct{}) {
	t.Helper()
	return watchLeafEvery(t, directory, time.Hour, func(err error) { t.Error(err) })
}

// watchLeafEvery runs the watch with the fallback interval a test
// chooses, and hands a watch that could not start to complain.
func watchLeafEvery(t *testing.T, directory string, fallback time.Duration,
	complain func(error)) (*leaf, chan struct{}) {
	t.Helper()
	held := newLeaf(directory)
	held.rereadAfter = fallback
	reads := make(chan struct{}, 64)
	done := make(chan struct{})
	finished := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		<-finished
	})
	go func() {
		defer close(finished)
		held.watchWith(done, func(_ bool, err error) {
			if err != nil {
				t.Error(err)
			}
			select {
			case reads <- struct{}{}:
			default:
			}
		}, complain)
	}()
	return held, reads
}

// next waits for one value on a channel, for at most five seconds. The
// inotify watch reads a real descriptor, so these tests run on the real
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

// readsUntil waits on each read until the leaf has been loaded from
// the files the given number of times. One update of the volume can
// wake more than one read, so the test counts loads, not reads.
func readsUntil(t *testing.T, held *leaf, reads chan struct{}, loads int) {
	t.Helper()
	for held.reloaded() < loads {
		next(t, reads, fmt.Sprintf("read for load %d of the files", loads))
	}
}

func TestTheLeafIsReadWhenTheKubeletSwapsTheVolume(t *testing.T) {
	directory := t.TempDir()
	held, reads := watchLeaf(t, directory)
	next(t, reads, "first read")
	if held.loaded() {
		t.Fatal("an empty volume read as a leaf")
	}

	publishLeaf(t, directory, 1)
	readsUntil(t, held, reads, 1)
	first, _ := held.certificate(nil)

	publishLeaf(t, directory, 2)
	readsUntil(t, held, reads, 2)
	second, _ := held.certificate(nil)

	if string(first.Certificate[0]) == string(second.Certificate[0]) {
		t.Error("the new leaf was not served")
	}
}

// A volume the inotify watch covers is read when it swaps and at no
// other time. The fallback interval here is a millisecond, so a timer
// that read the volume on its own would read it many times during the
// wait.
func TestAWatchedVolumeIsNotReadOnATimer(t *testing.T) {
	_, reads := watchLeafEvery(t, t.TempDir(), time.Millisecond, func(err error) { t.Error(err) })
	next(t, reads, "first read")
	select {
	case <-reads:
		t.Error("the volume was read again with no swap")
	case <-time.After(200 * time.Millisecond):
	}
}

// A container whose inotify watch could not start, such as on a node
// whose fs.inotify.max_user_instances is used up, still reads the
// volume on the fallback interval, so a new leaf reaches it. A watch
// that could not start runs no reader of its own, so the test runs in
// a bubble at the production interval.
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
			t.Errorf("the watch complained %d times, want once for each of the 4 reads", got)
		}
	})
}

func TestADirectoryWithNoSecretStillAnswersAHandshake(t *testing.T) {
	// The listener has to answer before the API has minted the leaf, or
	// the liveness probe on /healthz restarts the container every three
	// minutes. It answers with a certificate of its own, which no
	// client in this cluster trusts.
	held := newLeaf(t.TempDir())
	if err := held.reload(); err != nil {
		t.Fatalf("an absent Secret is not a failure: %v", err)
	}
	if held.loaded() {
		t.Error("a directory with no files reported the API's leaf")
	}
	standing, err := held.certificate(nil)
	if err != nil || standing == nil {
		t.Fatalf("a handshake before the Secret arrives got %v (%v)", standing, err)
	}
	// It is minted once and kept, so a scrape does not mint a key pair
	// on every request.
	again, err := held.certificate(nil)
	if err != nil || again != standing {
		t.Errorf("the standing certificate was minted again: %v (%v)", again, err)
	}
}

func TestTheAPIsOwnLeafReplacesTheStandingOne(t *testing.T) {
	directory := t.TempDir()
	held := newLeaf(directory)
	standing, err := held.certificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	writeLeaf(t, directory)
	if err := held.reload(); err != nil {
		t.Fatal(err)
	}
	if !held.loaded() {
		t.Fatal("the API's leaf was not read")
	}
	served, err := held.certificate(nil)
	if err != nil || served == standing {
		t.Error("the container kept serving its own certificate after the Secret arrived")
	}
}

func TestTheLeafIsReadOnceTheSecretArrives(t *testing.T) {
	directory := t.TempDir()
	held := newLeaf(directory)
	if err := held.reload(); err != nil {
		t.Fatal(err)
	}

	writeLeaf(t, directory)
	if err := held.reload(); err != nil {
		t.Fatalf("reading the Secret: %v", err)
	}
	if !held.loaded() {
		t.Fatal("the leaf was not read")
	}
	pair, err := held.certificate(nil)
	if err != nil || pair == nil {
		t.Fatalf("the handshake got %v (%v)", pair, err)
	}
}

func TestAnUnchangedSecretIsNotReadAgain(t *testing.T) {
	directory := t.TempDir()
	writeLeaf(t, directory)
	held := newLeaf(directory)
	for range 5 {
		if err := held.reload(); err != nil {
			t.Fatal(err)
		}
	}
	if held.reloaded() != 1 {
		t.Errorf("the files were read %d times, want 1", held.reloaded())
	}
}

func TestASecretTheAPIMintedAgainIsPickedUp(t *testing.T) {
	directory := t.TempDir()
	writeLeaf(t, directory)
	held := newLeaf(directory)
	if err := held.reload(); err != nil {
		t.Fatal(err)
	}
	first, _ := held.certificate(nil)

	// The kubelet refreshes the volume, so the files change under a
	// running container. The stamp reads the certificate's own bytes,
	// so the change is seen whatever the filesystem's timestamp
	// granularity is.
	writeLeaf(t, directory)
	if err := held.reload(); err != nil {
		t.Fatal(err)
	}
	if held.reloaded() != 2 {
		t.Errorf("the changed Secret was read %d times", held.reloaded())
	}
	second, _ := held.certificate(nil)
	if string(first.Certificate[0]) == string(second.Certificate[0]) {
		t.Error("the new leaf was not served")
	}
}

func TestASecretThatIsNotAKeyPairIsAFailure(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{tlsCertFile, tlsKeyFile} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("not pem"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	held := newLeaf(directory)
	if err := held.reload(); err == nil {
		t.Fatal("a Secret that is not a key pair was read")
	}
	if held.loaded() {
		t.Error("a Secret that is not a key pair was served")
	}
}

func TestASecretThatLeavesTakesTheCertificateWithIt(t *testing.T) {
	directory := t.TempDir()
	writeLeaf(t, directory)
	held := newLeaf(directory)
	if err := held.reload(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, tlsCertFile)); err != nil {
		t.Fatal(err)
	}
	if err := held.reload(); err != nil {
		t.Fatal(err)
	}
	if held.loaded() {
		t.Error("the certificate outlived the Secret")
	}
}

func TestTheReadinessGaugeFollowsTheLeaf(t *testing.T) {
	directory := t.TempDir()
	readings := newCaptureMetrics("dev")
	held := newLeaf(directory)

	done := make(chan struct{})
	close(done)
	held.watch(done, readings, func(error) {})
	if testutil.ToFloat64(readings.ready) != 0 {
		t.Error("audio_capture_ready is one with no Secret")
	}

	writeLeaf(t, directory)
	held.watch(done, readings, func(error) {})
	if testutil.ToFloat64(readings.ready) != 1 {
		t.Error("audio_capture_ready is zero with a Secret in place")
	}
}
