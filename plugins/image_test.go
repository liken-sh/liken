package plugins

// These tests cover the image half of the package: deriving the CLI
// image from the operator image, and pulling the binary out of it.
// The pull runs against a real in-memory registry (the
// go-containerregistry registry package) rather than a mock, so the
// same code path a workstation runs against ghcr is under test.

import (
	"archive/tar"
	"bytes"
	"net/http/httptest"
	"net/url"
	"runtime"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func TestCLIImageRefAppendsTheSuffixAndKeepsTheTag(t *testing.T) {
	cases := []struct {
		name     string
		operator string
		want     string
	}{
		{"release", "ghcr.io/liken-sh/audio-operator:2026.09.03-007", "ghcr.io/liken-sh/audio-operator-cli:2026.09.03-007"},
		{"dev build", "ghcr.io/liken-sh/media-operator:2026.09.03-007-dev-003-abcdef01", "ghcr.io/liken-sh/media-operator-cli:2026.09.03-007-dev-003-abcdef01"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := cliImageRef(c.operator)
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestCLIImageRefRefusesADigest(t *testing.T) {
	if _, err := cliImageRef("ghcr.io/liken-sh/audio-operator@sha256:" + hex64); err == nil {
		t.Fatal("a digest names no version tag to reuse")
	}
}

func TestImageVersionReadsTheTag(t *testing.T) {
	got, err := ImageVersion("ghcr.io/liken-sh/audio-operator:2026.09.03-007")
	if err != nil || got != "2026.09.03-007" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ImageVersion("ghcr.io/liken-sh/audio-operator@sha256:" + hex64); err == nil {
		t.Fatal("a digest names no version tag")
	}
}

func TestEntrypointNamesTheBinary(t *testing.T) {
	cfg := &v1.ConfigFile{}
	cfg.Config.Entrypoint = []string{"/kubectl-liken-audio"}
	got, err := entrypoint(cfg)
	if err != nil || got != "/kubectl-liken-audio" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := entrypoint(&v1.ConfigFile{}); err == nil {
		t.Fatal("an image with no entrypoint names no binary")
	}
}

// tarWith builds a one-file tar, the shape mutate.Extract yields.
func tarWith(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBinaryFromTarFindsTheEntrypointPath(t *testing.T) {
	body := tarWith(t, "./kubectl-liken-audio", []byte("ELF..."))
	got, err := binaryFromTar(bytes.NewReader(body), "/kubectl-liken-audio")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ELF..." {
		t.Fatalf("got %q", got)
	}
}

func TestBinaryFromTarReportsAMissingBinary(t *testing.T) {
	body := tarWith(t, "etc/passwd", []byte("x"))
	if _, err := binaryFromTar(bytes.NewReader(body), "/kubectl-liken-audio"); err == nil {
		t.Fatal("a missing binary must be an error")
	}
}

// cliImage builds a runnable CLI image: the binary as a file, and the
// entrypoint that names it.
func cliImage(t *testing.T, binary []byte) v1.Image {
	t.Helper()
	image, err := crane.Image(map[string][]byte{"kubectl-liken-audio": binary})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := image.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	config := cfg.Config.DeepCopy()
	config.Entrypoint = []string{"/kubectl-liken-audio"}
	image, err = mutate.Config(image, *config)
	if err != nil {
		t.Fatal(err)
	}
	return image
}

func TestRemotePullExtractsTheBinary(t *testing.T) {
	server := httptest.NewServer(registry.New())
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	// A localhost: reference resolves over http, so the pull runs
	// against the test registry the same way it runs against ghcr.
	ref := "localhost:" + u.Port() + "/liken-sh/audio-operator-cli:2026.09.03-007"

	tag, err := name.NewTag(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(tag, cliImage(t, []byte("the audio CLI"))); err != nil {
		t.Fatal(err)
	}

	got, err := remotePull(ref, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the audio CLI" {
		t.Fatalf("got %q", got)
	}
}

func TestRemotePullReportsAMissingImage(t *testing.T) {
	server := httptest.NewServer(registry.New())
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ref := "localhost:" + u.Port() + "/liken-sh/absent-cli:2026.09.03-007"
	if _, err := remotePull(ref, runtime.GOARCH); err == nil {
		t.Fatal("pulling an image that was never pushed must be an error")
	}
}

const hex64 = "0000000000000000000000000000000000000000000000000000000000000000"
