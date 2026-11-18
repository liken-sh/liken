package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Dockerfile copies each package directory by name, so a new
// package builds on a workstation and fails in the image build. A
// directory with its own go.mod, such as docs/, is another module and
// is not part of the image.
func TestTheDockerfileCopiesEveryGoPackage(t *testing.T) {
	dockerfile, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("reading the Dockerfile: %v", err)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("listing the package directory: %v", err)
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
			t.Errorf("the Dockerfile has no COPY line for the Go package %s/", entry.Name())
		}
	}
}
