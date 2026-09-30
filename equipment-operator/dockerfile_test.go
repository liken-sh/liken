package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Dockerfile copies the top-level Go files with one glob and each
// package directory by name, so a new package builds and tests on a
// workstation and fails only in the image build. This test reads the
// tree for every directory that holds Go source of this module and
// fails when the Dockerfile does not copy it. A directory with its own
// go.mod, such as docs/, is another module and never reaches the image.
func TestTheDockerfileCopiesEveryGoPackage(t *testing.T) {
	dockerfile, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "testdata" {
			continue
		}
		sources, _ := filepath.Glob(filepath.Join(entry.Name(), "*.go"))
		if _, err := os.Stat(filepath.Join(entry.Name(), "go.mod")); err == nil || len(sources) == 0 {
			continue
		}
		copied := "COPY " + entry.Name() + " ./" + entry.Name() + "\n"
		if !strings.Contains(string(dockerfile), copied) {
			t.Errorf("the Dockerfile does not copy the Go package %s/", entry.Name())
		}
	}
}
