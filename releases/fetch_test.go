package releases

// Tests for downloading a release. Each test round-trips through the
// package's own machinery: Bundle lays out a real (tiny) release,
// the serve handler publishes it the way any web server would, and
// Fetch downloads it back — so the fixtures can never drift from
// what the channel actually serves.

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// servedChannel bundles a release and exposes it over HTTP, returning
// the source URL a fetch would be pointed at and the channel directory
// behind it.
func servedChannel(t *testing.T, version string) (string, string) {
	t.Helper()
	channel, _ := bundledRelease(t, version)
	server := httptest.NewServer(handler(channel))
	t.Cleanup(server.Close)
	return server.URL + "/releases", channel
}

// documentDigest names a published release document the way a catalog
// entry would: by the sha256 of its exact bytes.
func documentDigest(t *testing.T, channel, version string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(channel, version, "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
}

func TestFetchDownloadsAVerifiableRelease(t *testing.T) {
	source, _ := servedChannel(t, "2026.07.14-001")
	dest := t.TempDir()

	var out bytes.Buffer
	if err := Fetch(source, "2026.07.14-001", "", dest, &out); err != nil {
		t.Fatal(err)
	}

	// What landed must verify the same way a machine would verify it:
	// parse the document, then check every artifact against it.
	raw, err := os.ReadFile(filepath.Join(dest, "2026.07.14-001", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range release.Artifacts {
		f, err := os.Open(filepath.Join(dest, "2026.07.14-001", a.Name))
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Verify(f); err != nil {
			t.Errorf("%s does not verify: %v", a.Name, err)
		}
		f.Close()
	}
}

func TestFetchVerifiesTheDocumentAgainstAPinnedDigest(t *testing.T) {
	source, channel := servedChannel(t, "2026.07.14-001")
	dest := t.TempDir()

	digest := documentDigest(t, channel, "2026.07.14-001")
	if err := Fetch(source, "2026.07.14-001", digest, dest, nil); err != nil {
		t.Errorf("a matching digest must be accepted: %v", err)
	}
}

func TestFetchRefusesADocumentThatMissesThePin(t *testing.T) {
	source, _ := servedChannel(t, "2026.07.14-001")
	dest := t.TempDir()

	wrong := "sha256:" + strings.Repeat("0", 64)
	err := Fetch(source, "2026.07.14-001", wrong, dest, nil)
	if err == nil {
		t.Fatal("a digest mismatch must refuse the release")
	}
	// Nothing may look complete: release.yaml is the marker of a
	// finished download and must never land for a refused release.
	if _, statErr := os.Stat(filepath.Join(dest, "2026.07.14-001", "release.yaml")); !os.IsNotExist(statErr) {
		t.Error("a refused release must not leave release.yaml behind")
	}
}

func TestFetchRefusesATamperedArtifact(t *testing.T) {
	source, channel := servedChannel(t, "2026.07.14-001")
	dest := t.TempDir()

	// Tamper with a published artifact after the document was written:
	// the served bytes no longer match the digests the document
	// promises.
	tampered := filepath.Join(channel, "2026.07.14-001", "liken.sqfs")
	if err := os.WriteFile(tampered, []byte("not the published bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Fetch(source, "2026.07.14-001", "", dest, nil)
	if err == nil {
		t.Fatal("a tampered artifact must refuse the release")
	}
	if !strings.Contains(err.Error(), "liken.sqfs") {
		t.Errorf("the error must name the artifact: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "2026.07.14-001", "release.yaml")); !os.IsNotExist(statErr) {
		t.Error("a refused release must not leave release.yaml behind")
	}
}

func TestFetchRepairsADamagedLocalCopy(t *testing.T) {
	source, _ := servedChannel(t, "2026.07.14-001")
	dest := t.TempDir()

	if err := Fetch(source, "2026.07.14-001", "", dest, nil); err != nil {
		t.Fatal(err)
	}
	// Damage one local artifact; a refetch must notice by digest and
	// repair only what fails, the same resume-by-reverification the
	// machines use.
	damaged := filepath.Join(dest, "2026.07.14-001", "vmlinuz")
	if err := os.WriteFile(damaged, []byte("torn"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Fetch(source, "2026.07.14-001", "", dest, &out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(damaged)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "kernel bytes" {
		t.Errorf("the damaged artifact was not repaired: %q", raw)
	}
	if !strings.Contains(out.String(), "1 fetched") {
		t.Errorf("only the damaged artifact should be refetched:\n%s", out.String())
	}
}

func TestFetchIsIdempotent(t *testing.T) {
	source, _ := servedChannel(t, "2026.07.14-001")
	dest := t.TempDir()

	if err := Fetch(source, "2026.07.14-001", "", dest, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Fetch(source, "2026.07.14-001", "", dest, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0 fetched") {
		t.Errorf("a complete local copy should fetch nothing:\n%s", out.String())
	}
}

func TestFetchResolvesLatestFromTheChannelDocument(t *testing.T) {
	source, _ := servedChannel(t, "2026.07.14-002")
	dest := t.TempDir()

	var out bytes.Buffer
	if err := Fetch(source, "latest", "", dest, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "2026.07.14-002", "release.yaml")); err != nil {
		t.Errorf("latest must land under its resolved version: %v", err)
	}
	if !strings.Contains(out.String(), "2026.07.14-002") {
		t.Errorf("the resolved version must be reported:\n%s", out.String())
	}
}

func TestFetchRefusesADocumentNamingTheWrongVersion(t *testing.T) {
	source, channel := servedChannel(t, "2026.07.14-001")
	dest := t.TempDir()

	// Publish one release's directory under another version's name:
	// the document inside still names the version it was bundled for.
	if err := os.Rename(filepath.Join(channel, "2026.07.14-001"), filepath.Join(channel, "2026.07.14-009")); err != nil {
		t.Fatal(err)
	}

	if err := Fetch(source, "2026.07.14-009", "", dest, nil); err == nil {
		t.Fatal("a document naming the wrong version must be refused")
	}
}

func TestFetchReportsAMissingRelease(t *testing.T) {
	source, _ := servedChannel(t, "2026.07.14-001")

	err := Fetch(source, "2026.07.14-002", "", t.TempDir(), nil)
	if err == nil {
		t.Fatal("fetching an unpublished version must fail")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("the error should carry the server's answer: %v", err)
	}
}
