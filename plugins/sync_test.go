package plugins

// These tests drive Sync with a fake pull, so the orchestration
// (install path, mode, idempotence, and the PATH hint) is under test
// without a registry. image_test.go covers the real pull.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakePull replaces the registry pull for one test. It records the
// references it is asked for and returns each reference as its own
// bytes, so a test can tell one binary from another.
func fakePull(t *testing.T) *[]string {
	t.Helper()
	var refs []string
	saved := pullBinary
	pullBinary = func(ref, arch string) ([]byte, error) {
		refs = append(refs, ref)
		return []byte(ref), nil
	}
	t.Cleanup(func() { pullBinary = saved })
	return &refs
}

func TestSyncInstallsOneCLIPerOperator(t *testing.T) {
	refs := fakePull(t)
	binDir := filepath.Join(t.TempDir(), "bin")
	operators := []Operator{
		{Domain: "audio", Image: "ghcr.io/liken-sh/audio-operator:2026.09.03-007"},
		{Domain: "library", Image: "ghcr.io/liken-sh/library-operator:2026.09.03-007"},
	}

	var out bytes.Buffer
	if err := Sync(operators, "amd64", binDir, "/usr/bin", &out); err != nil {
		t.Fatal(err)
	}

	for _, domain := range []string{"audio", "library"} {
		path := filepath.Join(binDir, Name(domain))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("no CLI for %s: %v", domain, err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Errorf("%s mode: got %v", domain, info.Mode().Perm())
		}
	}
	want := "ghcr.io/liken-sh/audio-operator-cli:2026.09.03-007"
	if len(*refs) != 2 || (*refs)[0] != want {
		t.Fatalf("pulled %v", *refs)
	}
}

func TestSyncReplacesOnReRun(t *testing.T) {
	fakePull(t)
	binDir := filepath.Join(t.TempDir(), "bin")
	operators := []Operator{{Domain: "audio", Image: "ghcr.io/liken-sh/audio-operator:2026.09.03-007"}}

	for i := 0; i < 2; i++ {
		if err := Sync(operators, "amd64", binDir, "/usr/bin", &bytes.Buffer{}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(binDir, Name("audio")))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ghcr.io/liken-sh/audio-operator-cli:2026.09.03-007" {
		t.Fatalf("got %q", data)
	}
}

func TestSyncPrintsThePathHintWhenTheDirIsNotOnPath(t *testing.T) {
	fakePull(t)
	binDir := filepath.Join(t.TempDir(), "bin")
	operators := []Operator{{Domain: "audio", Image: "ghcr.io/liken-sh/audio-operator:2026.09.03-007"}}

	var out bytes.Buffer
	if err := Sync(operators, "amd64", binDir, "/usr/bin", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), binDir) {
		t.Fatalf("the hint must name the plugin directory:\n%s", out.String())
	}
}

func TestSyncSkipsThePathHintWhenTheDirIsOnPath(t *testing.T) {
	fakePull(t)
	binDir := filepath.Join(t.TempDir(), "bin")
	operators := []Operator{{Domain: "audio", Image: "ghcr.io/liken-sh/audio-operator:2026.09.03-007"}}

	var out bytes.Buffer
	pathEnv := "/usr/bin" + string(os.PathListSeparator) + binDir
	if err := Sync(operators, "amd64", binDir, pathEnv, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "PATH") {
		t.Fatalf("no hint when the dir is already on PATH:\n%s", out.String())
	}
}

func TestSyncReportsADigestPinnedOperator(t *testing.T) {
	fakePull(t)
	binDir := filepath.Join(t.TempDir(), "bin")
	operators := []Operator{{Domain: "audio", Image: "ghcr.io/liken-sh/audio-operator@sha256:" + hex64}}
	err := Sync(operators, "amd64", binDir, "/usr/bin", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "audio") {
		t.Fatalf("a digest pin must fail and name the domain: %v", err)
	}
}
