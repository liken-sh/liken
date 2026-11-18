package drivers

// generated.go comes from the lists in indi/images/, the tag in
// indi/package.toml, and the tag in weston/package.toml.
// TestTheMapIsCurrent generates it again from those files and fails when
// the result differs, so a bump of the INDI images or a new driver in a
// list cannot leave generated.go stale. `make drivers` (or `go generate
// ./drivers`) runs the test with -update, which writes generated.go.

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "write generated.go from indi/images/, indi/package.toml, and weston/package.toml")

func TestTheMapIsCurrent(t *testing.T) {
	indi := filepath.Join("..", "..", "indi")
	want, err := generate(filepath.Join(indi, "images"), filepath.Join(indi, "package.toml"), filepath.Join("..", "..", "weston", "package.toml"))
	if err != nil {
		t.Fatalf("generating the map from the lists and tags: %v", err)
	}
	if *update {
		if err := os.WriteFile("generated.go", want, 0o644); err != nil {
			t.Fatalf("writing generated.go: %v", err)
		}
		return
	}
	got, err := os.ReadFile("generated.go")
	if err != nil {
		t.Fatalf("reading generated.go: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("generated.go differs from what indi/images/, indi/package.toml, and weston/package.toml generate, want no difference: run make drivers")
	}
}

func TestAListWithADriverTwiceIsRefused(t *testing.T) {
	dir := t.TempDir()
	images := filepath.Join(dir, "images")
	if err := os.Mkdir(images, 0o755); err != nil {
		t.Fatalf("creating the images directory: %v", err)
	}
	for name, body := range map[string]string{
		"zwo": "driver indi_asi_ccd\n",
		"qhy": "driver indi_asi_ccd\n",
	} {
		if err := os.WriteFile(filepath.Join(images, name), []byte(body), 0o644); err != nil {
			t.Fatalf("writing the list %s: %v", name, err)
		}
	}
	toml := filepath.Join(dir, "package.toml")
	if err := os.WriteFile(toml, []byte("version = \"20261005\"\nrevision = 3\n"), 0o644); err != nil {
		t.Fatalf("writing package.toml: %v", err)
	}
	if _, err := generate(images, toml, toml); err == nil {
		t.Error("generate returned no error for a driver in two lists, want an error")
	}
}

func TestAPackageWithNoTagIsRefused(t *testing.T) {
	dir := t.TempDir()
	toml := filepath.Join(dir, "package.toml")
	if err := os.WriteFile(toml, []byte("[package]\nname = \"indi\"\n"), 0o644); err != nil {
		t.Fatalf("writing package.toml: %v", err)
	}
	if _, err := generate(dir, toml, toml); err == nil {
		t.Error("generate returned no error for a package.toml with no version and revision, want an error")
	}
}
