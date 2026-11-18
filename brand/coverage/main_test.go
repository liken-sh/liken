package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The site and the release publish only the report file, so the
// command must write nothing else.
func TestRunWritesOneFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "coverage.html")
	args := []string{
		"-title", "widget", "-out", out, "-root", tree,
		"testdata/go.out", "testdata/cobertura.xml",
	}
	if err := run(args); err != nil {
		t.Fatalf("running coverage: %v", err)
	}
	written, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing the output directory: %v", err)
	}
	if len(written) != 1 {
		t.Errorf("the run wrote %d files, want 1", len(written))
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the report: %v", err)
	}
	for _, want := range []string{"<h1>widget</h1>", "60.0%", "75.0%"} {
		if !strings.Contains(string(page), want) {
			t.Errorf("the page is missing %q", want)
		}
	}
}

func TestRunLabelsTheInputsInOrder(t *testing.T) {
	out := filepath.Join(t.TempDir(), "coverage.html")
	args := []string{
		"-out", out, "-root", tree, "-label", "Go", "-label", "Rust",
		"testdata/go.out", "testdata/cobertura.xml",
	}
	if err := run(args); err != nil {
		t.Fatalf("running coverage: %v", err)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the report: %v", err)
	}
	for _, want := range []string{">Rust</a>", ">Rust</h2>", "<h1>Test coverage</h1>"} {
		if !strings.Contains(string(page), want) {
			t.Errorf("the page is missing %q", want)
		}
	}
}

func TestRunRefusesAnIncompleteCommandLine(t *testing.T) {
	out := filepath.Join(t.TempDir(), "coverage.html")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no input", []string{"-out", out}},
		{"no output file", []string{"testdata/go.out"}},
		{"more labels than inputs", []string{"-out", out, "-label", "Go", "-label", "Rust", "testdata/go.out"}},
		{"a missing input", []string{"-out", out, "-root", tree, "testdata/gone.out"}},
		{"an unwritable output file", []string{"-out", filepath.Join(out, "deeper.html"), "-root", tree, "testdata/go.out"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := run(tc.args); err == nil {
				t.Errorf("run(%q) returned no error, want one", tc.args)
			}
		})
	}
}

func TestRunPrintsTheUsageForHelp(t *testing.T) {
	if err := run([]string{"-h"}); err != nil {
		t.Errorf("-h is not a failure: %v", err)
	}
}
