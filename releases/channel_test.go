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

var testComponents = []machine.ReleaseComponent{
	{Name: "kernel", Version: "7.1.2"},
	{Name: "k3s", Version: "v1.36.2+k3s1"},
}

// bundledRelease lays out a tiny release through Bundle itself and
// returns the channel directory and Bundle's report.
func bundledRelease(t *testing.T, version string) (string, string) {
	t.Helper()
	src := t.TempDir()
	for name, content := range map[string]string{
		"vmlinuz":             "kernel bytes",
		"liken.sqfs":          "system image bytes",
		"boot.cpio":           "boot archive bytes",
		"liken":               "toolkit bytes",
		"systemd-bootx64.efi": "boot menu bytes",
		"grub-boot.img":       "mbr stage bytes",
		"grub-core.img":       "core image bytes",
	} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	channel := t.TempDir()
	var out bytes.Buffer
	err := Bundle(filepath.Join(src, "vmlinuz"), filepath.Join(src, "liken.sqfs"),
		filepath.Join(src, "boot.cpio"),
		filepath.Join(src, "liken"), filepath.Join(src, "systemd-bootx64.efi"),
		filepath.Join(src, "grub-boot.img"), filepath.Join(src, "grub-core.img"),
		channel, version, testComponents, &out)
	if err != nil {
		t.Fatal(err)
	}
	return channel, out.String()
}

func TestBundleProducesAVerifiableRelease(t *testing.T) {
	channel, _ := bundledRelease(t, "2026.07.11-001")

	// The document must parse as the same Release kind machines
	// verify, and every artifact must verify against it: the same
	// check the fetch path performs.
	raw, err := os.ReadFile(filepath.Join(channel, "2026.07.11-001", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		t.Fatal(err)
	}
	if release.Metadata.Name != "2026.07.11-001" {
		t.Errorf("release name: %q", release.Metadata.Name)
	}
	if len(release.Artifacts) != 7 {
		t.Fatalf("artifacts: %d", len(release.Artifacts))
	}
	for _, a := range release.Artifacts {
		f, err := os.Open(filepath.Join(channel, "2026.07.11-001", a.Name))
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Verify(f); err != nil {
			t.Errorf("%s does not verify: %v", a.Name, err)
		}
		f.Close()
	}
}

func TestBundleRecordsTheComponents(t *testing.T) {
	channel, _ := bundledRelease(t, "2026.07.11-001")

	// The version is a calendar date that says nothing about what
	// shipped; the document's components section is where the what
	// lives, so a bundle must carry it through verbatim.
	raw, err := os.ReadFile(filepath.Join(channel, "2026.07.11-001", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Components) != 2 {
		t.Fatalf("components: %+v", release.Components)
	}
	if release.Components[0].Name != "kernel" || release.Components[0].Version != "7.1.2" {
		t.Errorf("components: %+v", release.Components)
	}
	if release.Components[1].Name != "k3s" || release.Components[1].Version != "v1.36.2+k3s1" {
		t.Errorf("components: %+v", release.Components)
	}
}

func TestBundleReportsTheCatalogEntry(t *testing.T) {
	channel, report := bundledRelease(t, "2026.07.11-002")

	// The catalog entry is what a deployment commits to its Cluster:
	// the release document named by its own digest, computed from the
	// published copy, the root of the trust chain.
	digest, err := fileSHA256(filepath.Join(channel, "2026.07.11-002", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "digest: sha256:"+digest) {
		t.Errorf("report does not carry the document digest:\n%s", report)
	}
	if !strings.Contains(report, "version: 2026.07.11-002") {
		t.Errorf("report does not carry the version:\n%s", report)
	}
}

func TestBundleWritesTheChannelDocument(t *testing.T) {
	channel, report := bundledRelease(t, "2026.07.11-001")

	// One bundle in a fresh channel makes that bundle the latest; the
	// document at the channel's root must say so, in the same Channel
	// kind a polling cluster will read.
	raw, err := os.ReadFile(filepath.Join(channel, "channel.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := machine.ParseChannel(raw)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Latest != "2026.07.11-001" {
		t.Errorf("latest: %q", doc.Latest)
	}
	if !strings.Contains(report, "latest release: 2026.07.11-001") {
		t.Errorf("report does not announce the channel's latest:\n%s", report)
	}
}

func TestChannelDocumentNamesTheNewestRelease(t *testing.T) {
	// Bundling an older release after a newer one must not move the
	// channel backwards: the latest is the newest version present, not
	// the most recently bundled. The zero-padded calendar grammar makes
	// plain string order the version order.
	channel, _ := bundledRelease(t, "2026.07.11-002")
	rebundleInto(t, channel, "2026.07.11-001")

	raw, err := os.ReadFile(filepath.Join(channel, "channel.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := machine.ParseChannel(raw)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Latest != "2026.07.11-002" {
		t.Errorf("latest went backwards: %q", doc.Latest)
	}
}

func TestChannelDocumentIgnoresForeignDirectories(t *testing.T) {
	// A channel directory may hold things that aren't releases (notes,
	// scratch space); only version-shaped directories are candidates
	// for latest.
	channel, _ := bundledRelease(t, "2026.07.11-001")
	if err := os.MkdirAll(filepath.Join(channel, "not-a-version"), 0o755); err != nil {
		t.Fatal(err)
	}
	rebundleInto(t, channel, "2026.07.11-002")

	raw, err := os.ReadFile(filepath.Join(channel, "channel.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := machine.ParseChannel(raw)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Latest != "2026.07.11-002" {
		t.Errorf("latest: %q", doc.Latest)
	}
}

// rebundleInto runs Bundle again against an existing channel
// directory, the way successive releases land in the same channel.
func rebundleInto(t *testing.T, channel, version string) {
	t.Helper()
	src := t.TempDir()
	for _, name := range []string{"vmlinuz", "liken.sqfs", "boot.cpio", "liken", "systemd-bootx64.efi", "grub-boot.img", "grub-core.img"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(name+" bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	err := Bundle(filepath.Join(src, "vmlinuz"), filepath.Join(src, "liken.sqfs"),
		filepath.Join(src, "boot.cpio"),
		filepath.Join(src, "liken"), filepath.Join(src, "systemd-bootx64.efi"),
		filepath.Join(src, "grub-boot.img"), filepath.Join(src, "grub-core.img"),
		channel, version, testComponents, &out)
	if err != nil {
		t.Fatal(err)
	}
}

func TestBundleReplacesAPreviousAttempt(t *testing.T) {
	channel := t.TempDir()
	stale := filepath.Join(channel, "2026.07.11-001", "leftover")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := t.TempDir()
	for _, name := range []string{"vmlinuz", "liken.sqfs", "boot.cpio", "liken", "systemd-bootx64.efi", "grub-boot.img", "grub-core.img"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := Bundle(filepath.Join(src, "vmlinuz"), filepath.Join(src, "liken.sqfs"),
		filepath.Join(src, "boot.cpio"),
		filepath.Join(src, "liken"), filepath.Join(src, "systemd-bootx64.efi"),
		filepath.Join(src, "grub-boot.img"), filepath.Join(src, "grub-core.img"),
		channel, "2026.07.11-001", testComponents, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a rebundle must not leave stale files in the version directory")
	}
}

func TestBundleRefusesAMissingArtifact(t *testing.T) {
	if err := Bundle("no-such-vmlinuz", "no-such-sqfs", "no-such-cpio", "no-such-cli", "no-such-menu",
		"no-such-mbr", "no-such-core",
		t.TempDir(), "2026.07.11-001", testComponents, io.Discard); err == nil {
		t.Error("bundling artifacts that don't exist must fail")
	}
}

func TestBundleRefusesAMalformedVersion(t *testing.T) {
	// The grammar is enforced where versions are authored, so a typo
	// is refused here rather than discovered when a machine fails to
	// fetch. Nothing may land in the channel under the bad name.
	channel := t.TempDir()
	err := Bundle("vmlinuz", "liken.sqfs", "boot.cpio", "liken", "menu.efi",
		"grub-boot.img", "grub-core.img",
		channel, "1.2.3", testComponents, io.Discard)
	if err == nil {
		t.Fatal("a version outside the grammar must be refused")
	}
	if entries, _ := os.ReadDir(channel); len(entries) != 0 {
		t.Errorf("a refused bundle must leave the channel untouched: %v", entries)
	}
}
