package main

import (
	"os"
	"path/filepath"
	"testing"
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
