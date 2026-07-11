package image

// Tests for install-media assembly: a public release directory plus a
// deployment layer become one bootable install image. The release
// fixture is a channel-shaped directory whose document is generated
// the same way the publisher generates it, from the artifact bytes.

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// releaseFixtureWith lays out a release directory: the given
// artifacts and a release.yaml naming them by digest and size.
func releaseFixtureWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	document := "apiVersion: liken.sh/v1alpha1\nkind: Release\nmetadata:\n  name: 0.9.9\nartifacts:\n"
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		document += fmt.Sprintf("  - name: %s\n    sha256: %x\n    size: %d\n",
			name, sha256.Sum256([]byte(content)), len(content))
	}
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// releaseFixture is the media tests' four-artifact release.
func releaseFixture(t *testing.T) string {
	t.Helper()
	return releaseFixtureWith(t, map[string]string{
		"vmlinuz":    "kernel bytes",
		"liken.sqfs": "system image bytes",
		"boot.cpio":  "boot archive bytes",
		"liken":      "toolkit bytes",
	})
}

// layerFixture writes a stand-in deployment layer beside the test.
func layerFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), machine.LayerName)
	if err := os.WriteFile(path, []byte("deployment layer bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// buildMedia runs Media over the fixtures and returns the image's
// bytes.
func buildMedia(t *testing.T, releaseDir, layerPath string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "install.cpio")
	if err := Media(releaseDir, layerPath, out, io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMediaLeadsWithTheComposedSystem(t *testing.T) {
	releaseDir := releaseFixture(t)
	layerPath := layerFixture(t)
	raw := buildMedia(t, releaseDir, layerPath)

	// The install boot runs from the boot archive and the layer, in
	// override order, ahead of the payload wrapper; the system image
	// travels only inside the payload.
	composed := append([]byte("boot archive bytes"), []byte("deployment layer bytes")...)
	if !bytes.HasPrefix(raw, composed) {
		t.Error("the image must begin with the boot archive followed by the layer")
	}
}

func TestMediaCarriesTheReleasePayload(t *testing.T) {
	releaseDir := releaseFixture(t)
	layerPath := layerFixture(t)
	raw := buildMedia(t, releaseDir, layerPath)

	wrapper := raw[len("boot archive bytes")+len("deployment layer bytes"):]
	files := map[string][]byte{}
	for _, e := range readArchive(t, wrapper) {
		if e.mode&0o170000 == 0o100000 {
			files[e.name] = e.data
		}
	}

	document, err := os.ReadFile(filepath.Join(releaseDir, "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	payload := "usr/share/liken/release/"
	for name, want := range map[string]string{
		"vmlinuz":                "kernel bytes",
		"liken.sqfs":             "system image bytes",
		"boot.cpio":              "boot archive bytes",
		"liken":                  "toolkit bytes",
		"release.yaml":           string(document),
		machine.LayerName:        "deployment layer bytes",
		machine.LayerSidecarName: "",
	} {
		got, ok := files[payload+name]
		if !ok {
			t.Errorf("the payload is missing %s", name)
			continue
		}
		if want != "" && string(got) != want {
			t.Errorf("%s: payload bytes differ from the release's", name)
		}
	}
}

func TestMediaSidecarVouchesForTheLayer(t *testing.T) {
	releaseDir := releaseFixture(t)
	layerPath := layerFixture(t)
	raw := buildMedia(t, releaseDir, layerPath)

	wrapper := raw[len("boot archive bytes")+len("deployment layer bytes"):]
	var sidecar []byte
	for _, e := range readArchive(t, wrapper) {
		if strings.HasSuffix(e.name, machine.LayerSidecarName) {
			sidecar = e.data
		}
	}
	digest, err := machine.ParseLayerSidecar(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.VerifyLayer(digest, strings.NewReader("deployment layer bytes")); err != nil {
		t.Errorf("the sidecar does not vouch for the layer: %v", err)
	}
}

func TestMediaRefusesATamperedArtifact(t *testing.T) {
	releaseDir := releaseFixture(t)
	if err := os.WriteFile(filepath.Join(releaseDir, "vmlinuz"), []byte("not the kernel"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Media(releaseDir, layerFixture(t), filepath.Join(t.TempDir(), "out"), io.Discard)
	if err == nil {
		t.Error("an artifact that fails its document must not be packed")
	}
}

func TestMediaRefusesADamagedReleaseDirectory(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, releaseDir string)
	}{
		{"missing document", func(t *testing.T, releaseDir string) {
			if err := os.Remove(filepath.Join(releaseDir, "release.yaml")); err != nil {
				t.Fatal(err)
			}
		}},
		{"garbage document", func(t *testing.T, releaseDir string) {
			if err := os.WriteFile(filepath.Join(releaseDir, "release.yaml"), []byte("{not yaml"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			releaseDir := releaseFixture(t)
			c.prepare(t, releaseDir)
			err := Media(releaseDir, layerFixture(t), filepath.Join(t.TempDir(), "out"), io.Discard)
			if err == nil {
				t.Error("a release directory without a sound document must be refused")
			}
		})
	}
}

func TestMediaRefusesAMissingLayer(t *testing.T) {
	missing := filepath.Join(t.TempDir(), machine.LayerName)
	err := Media(releaseFixture(t), missing, filepath.Join(t.TempDir(), "out"), io.Discard)
	if err == nil {
		t.Error("media cannot be assembled without the deployment layer")
	}
}

func TestMediaReportsAnUnwritableOutput(t *testing.T) {
	out := filepath.Join(t.TempDir(), "no-such-dir", "install.cpio")
	err := Media(releaseFixture(t), layerFixture(t), out, io.Discard)
	if err == nil {
		t.Error("an unwritable output path is an error to surface")
	}
}

func TestMediaRefusesAMissingArtifact(t *testing.T) {
	releaseDir := releaseFixture(t)
	if err := os.Remove(filepath.Join(releaseDir, "liken")); err != nil {
		t.Fatal(err)
	}
	err := Media(releaseDir, layerFixture(t), filepath.Join(t.TempDir(), "out"), io.Discard)
	if err == nil {
		t.Error("a release missing a listed artifact must not be packed")
	}
}
