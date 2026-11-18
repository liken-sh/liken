package capture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestINDIVersion(t *testing.T) {
	versions := "gsc\t1.3+202609080545~ubuntu26.04.1\nindi-bin\t2.2.5+202610040455~ubuntu26.04.1\n"
	got, err := INDIVersion([]byte(versions))
	if err != nil || got != "2.2.5+202610040455~ubuntu26.04.1" {
		t.Errorf("INDIVersion = %q, %v, want 2.2.5+202610040455~ubuntu26.04.1 and no error", got, err)
	}
}

func TestINDIVersionRefusesAListWithNoIndiBin(t *testing.T) {
	if _, err := INDIVersion([]byte("gsc\t1.3\n")); err == nil {
		t.Error("INDIVersion returned no error for a list with no indi-bin line, want an error")
	}
}

func writeToml(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("writing package.toml: %v", err)
	}
	return path
}

// The [package] table also has an indi key. PinnedINDI must ignore it
// and read only [package.upstream].
func TestPinnedINDI(t *testing.T) {
	path := writeToml(t, `[package]
name = "indi"
indi = "not this one"

[package.upstream]
indi-3rdparty = "2.2.0"
indi = "2.2.5"
`)
	got, err := PinnedINDI(path)
	if err != nil || got != "2.2.5" {
		t.Errorf("PinnedINDI = %q, %v, want 2.2.5 and no error", got, err)
	}
}

func TestPinnedINDIFails(t *testing.T) {
	cases := []struct {
		name, path string
	}{
		{"no indi in [package.upstream]", writeToml(t, "[package]\nindi = \"2.2.5\"\n")},
		{"no file", filepath.Join(t.TempDir(), "package.toml")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := PinnedINDI(c.path); err == nil {
				t.Error("PinnedINDI returned no error, want an error")
			}
		})
	}
}
