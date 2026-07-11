package releases

// Tests for bundling a release. The fixtures are small stand-ins
// with the same shape as the real thing: artifact files and a
// document generated from their real bytes.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// bundledRelease lays out a tiny release through Bundle itself and
// returns the channel directory and Bundle's report.
func bundledRelease(t *testing.T, version string) (string, string) {
	t.Helper()
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
	var out bytes.Buffer
	err := Bundle(filepath.Join(src, "vmlinuz"), filepath.Join(src, "liken.cpio"),
		filepath.Join(src, "liken"), channel, version, &out)
	if err != nil {
		t.Fatal(err)
	}
	return channel, out.String()
}

func TestBundleProducesAVerifiableRelease(t *testing.T) {
	channel, _ := bundledRelease(t, "0.2.0")

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

func TestBundleReportsTheCatalogEntry(t *testing.T) {
	channel, report := bundledRelease(t, "1.2.3")

	// The catalog entry is what a deployment commits to its Cluster:
	// the release document named by its own digest, computed from the
	// published copy, the root of the trust chain.
	digest, err := fileSHA256(filepath.Join(channel, "1.2.3", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "digest: sha256:"+digest) {
		t.Errorf("report does not carry the document digest:\n%s", report)
	}
	if !strings.Contains(report, "version: 1.2.3") {
		t.Errorf("report does not carry the version:\n%s", report)
	}
}

func TestBundleReplacesAPreviousAttempt(t *testing.T) {
	channel := t.TempDir()
	stale := filepath.Join(channel, "0.2.0", "leftover")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := t.TempDir()
	for _, name := range []string{"vmlinuz", "liken.cpio", "liken"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := Bundle(filepath.Join(src, "vmlinuz"), filepath.Join(src, "liken.cpio"),
		filepath.Join(src, "liken"), channel, "0.2.0", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a rebundle must not leave stale files in the version directory")
	}
}

func TestBundleRefusesAMissingArtifact(t *testing.T) {
	if err := Bundle("no-such-vmlinuz", "no-such-cpio", "no-such-cli",
		t.TempDir(), "0.0.1", io.Discard); err == nil {
		t.Error("bundling artifacts that don't exist must fail")
	}
}
