package releases

// Tests for the channel operations: publishing a deployment's built
// image, bundling a public release, and the corruption drill. The
// fixtures are small stand-ins with the same shapes: a built image
// directory carrying an install payload, and artifact files big
// enough to corrupt.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// builtImage stands in for a deployment's build/<v>/image directory:
// the install payload install.sh would have packed, plus the install
// image beside it.
func builtImage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	payload := filepath.Join(dir, "install-root", "usr", "share", "liken", "release")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(payload, "vmlinuz"):      "kernel bytes",
		filepath.Join(payload, "liken.cpio"):   "composed image bytes",
		filepath.Join(payload, "release.yaml"): "kind: Release\nmetadata: {name: 1.2.3}\n",
		filepath.Join(dir, "install.cpio"):     "install image bytes",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPublishLaysOutTheChannel(t *testing.T) {
	channel := t.TempDir()
	var out bytes.Buffer
	if err := Publish(builtImage(t), channel, "1.2.3", &out); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"vmlinuz":      "kernel bytes",
		"liken.cpio":   "composed image bytes",
		"release.yaml": "kind: Release\nmetadata: {name: 1.2.3}\n",
		"install.cpio": "install image bytes",
	} {
		got, err := os.ReadFile(filepath.Join(channel, "1.2.3", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s: published bytes differ from the build's", name)
		}
	}
}

func TestPublishReportsTheCatalogEntry(t *testing.T) {
	channel := t.TempDir()
	var out bytes.Buffer
	if err := Publish(builtImage(t), channel, "1.2.3", &out); err != nil {
		t.Fatal(err)
	}

	// The catalog names the release document by its own digest: the
	// root of the trust chain, computed from the published copy.
	digest, err := fileSHA256(filepath.Join(channel, "1.2.3", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "digest: sha256:"+digest) {
		t.Errorf("report does not carry the document digest:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "version: 1.2.3") {
		t.Errorf("report does not carry the version:\n%s", out.String())
	}
}

func TestPublishReplacesAPreviousAttempt(t *testing.T) {
	channel := t.TempDir()
	stale := filepath.Join(channel, "1.2.3", "leftover")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Publish(builtImage(t), channel, "1.2.3", io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a republish must not leave stale files in the version directory")
	}
}

func TestBundleProducesAVerifiableRelease(t *testing.T) {
	src := t.TempDir()
	for name, content := range map[string]string{
		"vmlinuz":    "kernel bytes",
		"liken.cpio": "generic image bytes",
		"liken":      "toolkit bytes",
	} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	channel := t.TempDir()

	err := Bundle(filepath.Join(src, "vmlinuz"), filepath.Join(src, "liken.cpio"),
		filepath.Join(src, "liken"), channel, "0.2.0", io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	// The document must parse as the same Release kind machines
	// verify, and every artifact must verify against it: the same
	// check the fetch path performs.
	raw, err := os.ReadFile(filepath.Join(channel, "0.2.0", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		t.Fatal(err)
	}
	if release.Metadata.Name != "0.2.0" {
		t.Errorf("release name: %q", release.Metadata.Name)
	}
	if len(release.Artifacts) != 3 {
		t.Fatalf("artifacts: %d", len(release.Artifacts))
	}
	for _, a := range release.Artifacts {
		f, err := os.Open(filepath.Join(channel, "0.2.0", a.Name))
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Verify(f); err != nil {
			t.Errorf("%s does not verify: %v", a.Name, err)
		}
		f.Close()
	}
}

func TestCorruptBreaksTheDigestAndOnlyTheDigest(t *testing.T) {
	channel := t.TempDir()
	version := filepath.Join(channel, "1.2.3")
	if err := os.MkdirAll(version, 0o755); err != nil {
		t.Fatal(err)
	}
	original := bytes.Repeat([]byte("liken"), 512*1024)
	if err := os.WriteFile(filepath.Join(version, "liken.cpio"), original, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Corrupt(channel, "1.2.3", &out); err != nil {
		t.Fatal(err)
	}

	damaged, err := os.ReadFile(filepath.Join(version, "liken.cpio"))
	if err != nil {
		t.Fatal(err)
	}
	if len(damaged) != len(original) {
		t.Fatalf("corruption changed the size: %d -> %d", len(original), len(damaged))
	}
	diffs := 0
	for i := range original {
		if original[i] != damaged[i] {
			diffs++
			if i != corruptionOffset {
				t.Errorf("byte %d changed; only %d should", i, corruptionOffset)
			}
		}
	}
	if diffs != 1 {
		t.Errorf("%d bytes changed, want exactly 1", diffs)
	}
	if !strings.Contains(out.String(), "refuse") {
		t.Errorf("report does not say what the drill proves:\n%s", out.String())
	}
}

func TestCorruptRefusesAnUnpublishedRelease(t *testing.T) {
	if err := Corrupt(t.TempDir(), "9.9.9", io.Discard); err == nil {
		t.Error("corrupting a release that isn't there must fail loudly")
	}
}

func TestPublishRefusesAnImageWithoutAPayload(t *testing.T) {
	// A directory that never went through install.sh has no payload;
	// publishing it must fail before the channel is touched.
	channel := t.TempDir()
	if err := Publish(t.TempDir(), channel, "1.2.3", io.Discard); err == nil {
		t.Error("publishing an unbuilt image must fail")
	}
}

func TestBundleRefusesAMissingArtifact(t *testing.T) {
	if err := Bundle("no-such-vmlinuz", "no-such-cpio", "no-such-cli",
		t.TempDir(), "0.0.1", io.Discard); err == nil {
		t.Error("bundling artifacts that don't exist must fail")
	}
}
