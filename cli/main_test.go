package main

// These tests cover the dispatcher. Each command routes to its
// capability, after this package checks its arguments first. The
// capabilities test themselves in their own packages. What is under
// test here is only the routing table.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRequiresACommand(t *testing.T) {
	err := run(nil)
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Errorf("no arguments should ask for a command: %v", err)
	}
}

func TestRunRefusesAnUnknownCommand(t *testing.T) {
	err := run([]string{"launder"})
	if err == nil || !strings.Contains(err.Error(), `unknown command "launder"`) {
		t.Errorf("unknown command was not refused: %v", err)
	}
}

func TestRunChecksArgumentCounts(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"new without a directory", []string{"new"}},
		{"mint without a directory", []string{"mint"}},
		{"adopt without directories", []string{"adopt", "only-one"}},
		{"kubeconfig without a directory", []string{"kubeconfig"}},
		{"layer without its inputs", []string{"layer", "manifests"}},
		{"fetch without its inputs", []string{"fetch", "https://example.com"}},
		{"media without its inputs", []string{"media", "release-dir"}},
		{"stick without its inputs", []string{"stick", "-console", "ttyS0", "release-dir"}},
		{"bundle without its artifacts", []string{"bundle", "vmlinuz"}},
		{"serve with too many arguments", []string{"serve", "channel", "addr", "extra"}},
		{"index without a source", []string{"index", "out"}},
		{"index without an output directory", []string{"index", "-source", "https://example.com"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := run(c.args)
			if err == nil || !strings.Contains(err.Error(), "usage:") {
				t.Errorf("bad arguments were not refused: %v", err)
			}
		})
	}
}

func TestRunMintsAndComputesAKubeconfig(t *testing.T) {
	deploymentDir := t.TempDir()
	identityDir := filepath.Join(deploymentDir, "identity")
	if err := run([]string{"mint", identityDir}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"kubeconfig", "-server", "https://127.0.0.1:16443", deploymentDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(identityDir, "kubeconfig")); err != nil {
		t.Error("no kubeconfig was written")
	}
}

func TestRunPacksADeploymentLayer(t *testing.T) {
	identityDir := filepath.Join(t.TempDir(), "identity")
	if err := run([]string{"mint", identityDir}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "deployment.cpio")
	// A deployment with no manifests still has an identity, so the
	// layer holds only identity.
	if err := run([]string{"layer", t.TempDir(), identityDir, out}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Error("no layer was written")
	}
}

// bundledChannel sends one small release through the bundle command
// and returns the channel directory. The artifacts only need to
// exist, so their contents are their own names.
func bundledChannel(t *testing.T, version string, components ...string) string {
	t.Helper()
	src := t.TempDir()
	for _, name := range []string{"vmlinuz", "liken.sqfs", "boot.cpio", "microcode.cpio", "liken", "systemd-bootx64.efi", "grub-boot.img", "grub-core.img", "LICENSES.md"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(name+" bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	channel := t.TempDir()
	args := []string{"bundle", filepath.Join(src, "vmlinuz"), filepath.Join(src, "liken.sqfs"),
		filepath.Join(src, "boot.cpio"), filepath.Join(src, "microcode.cpio"),
		filepath.Join(src, "liken"), filepath.Join(src, "systemd-bootx64.efi"),
		filepath.Join(src, "grub-boot.img"), filepath.Join(src, "grub-core.img"),
		filepath.Join(src, "LICENSES.md"), channel, version}
	if err := run(append(args, components...)); err != nil {
		t.Fatal(err)
	}
	return channel
}

// withStdin points standard input at the given text for one test. The
// index command reads its key list from there.
func withStdin(t *testing.T, text string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = saved
		f.Close()
	})
}

func TestRunAssemblesInstallMedia(t *testing.T) {
	// This is a small release round-trip: bundle a release, pack a
	// layer for an empty deployment, and turn the two into install
	// media.
	channel := bundledChannel(t, "2026.07.11-001")

	identityDir := filepath.Join(t.TempDir(), "identity")
	if err := run([]string{"mint", identityDir}); err != nil {
		t.Fatal(err)
	}
	layer := filepath.Join(t.TempDir(), "deployment.cpio")
	if err := run([]string{"layer", t.TempDir(), identityDir, layer}); err != nil {
		t.Fatal(err)
	}

	media := filepath.Join(t.TempDir(), "install.cpio")
	if err := run([]string{"media", filepath.Join(channel, "2026.07.11-001"), layer, media}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(media); err != nil {
		t.Error("no install media was written")
	}
}

func TestRunBundlesARelease(t *testing.T) {
	channel := bundledChannel(t, "2026.07.11-001", "kernel=7.1.2", "k3s=v1.36.2+k3s1")

	if _, err := os.Stat(filepath.Join(channel, "2026.07.11-001", "release.yaml")); err != nil {
		t.Error("no release document was written")
	}
}

func TestRunIndexesAChannel(t *testing.T) {
	// The index command reads the channel over HTTP, the way it reads
	// the public one, so the test serves a bundled channel and points
	// the command at it.
	channel := bundledChannel(t, "2026.07.11-001", "kernel=7.1.2")
	server := httptest.NewServer(http.FileServer(http.Dir(channel)))
	t.Cleanup(server.Close)
	withStdin(t, "2026.07.11-001/release.yaml\n2026.07.11-001/vmlinuz\n\n")

	pages := t.TempDir()
	if err := run([]string{"index", "-source", server.URL, pages}); err != nil {
		t.Fatal(err)
	}

	for _, page := range [][]string{{"index.html"}, {"2026.07.11-001", "index.html"}} {
		if _, err := os.Stat(filepath.Join(append([]string{pages}, page...)...)); err != nil {
			t.Errorf("no page at %v", page)
		}
	}
}

func TestRunRefusesAMalformedComponent(t *testing.T) {
	err := run([]string{"bundle", "vmlinuz", "liken.sqfs", "boot.cpio", "microcode.cpio", "liken", "menu.efi",
		"grub-boot.img", "grub-core.img", "LICENSES.md",
		t.TempDir(), "2026.07.11-001", "kernel"})
	if err == nil || !strings.Contains(err.Error(), "name=version") {
		t.Errorf("a component without name=version must be refused: %v", err)
	}
}

func TestRunReportsTheVersion(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Error(err)
	}
}
