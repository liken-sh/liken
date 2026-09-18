package plugins

// These tests cover the list half: reading which CLIs are installed,
// merging them with the operators the cluster runs, and the drift and
// cold-start marks the render draws.

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestInstalledReadsTheDomainsInTheDirectory(t *testing.T) {
	binDir := t.TempDir()
	for _, name := range []string{Name("audio"), Name("library"), "kubectl", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Installed(binDir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"audio", "library"}) {
		t.Fatalf("got %v", got)
	}
}

func TestInstalledReadsAMissingDirectoryAsEmpty(t *testing.T) {
	got, err := Installed(filepath.Join(t.TempDir(), "absent"))
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestMergeUnionsBothSides(t *testing.T) {
	installed := map[string]string{"audio": "2026.09.03-007", "display": "2026.09.02-001"}
	operators := map[string]string{"audio": "2026.09.03-007", "library": "2026.09.03-007"}
	got := Merge(installed, operators)

	want := []Entry{
		{Domain: "audio", CLIVersion: "2026.09.03-007", OperatorVersion: "2026.09.03-007"},
		{Domain: "display", CLIVersion: "2026.09.02-001", OperatorVersion: ""},
		{Domain: "library", CLIVersion: "", OperatorVersion: "2026.09.03-007"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestEntryDriftAndMissing(t *testing.T) {
	cases := []struct {
		name    string
		entry   Entry
		drift   bool
		missing bool
	}{
		{"matched", Entry{"audio", "v1", "v1"}, false, false},
		{"drifted", Entry{"audio", "v1", "v2"}, true, false},
		{"missing cli", Entry{"library", "", "v2"}, false, true},
		{"orphan cli", Entry{"display", "v1", ""}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.entry.Drift() != c.drift {
				t.Errorf("drift: got %v", c.entry.Drift())
			}
			if c.entry.MissingCLI() != c.missing {
				t.Errorf("missing: got %v", c.entry.MissingCLI())
			}
		})
	}
}

func TestRenderMarksDriftAndNamesMissingCLIs(t *testing.T) {
	entries := []Entry{
		{Domain: "audio", CLIVersion: "2026.09.03-007", OperatorVersion: "2026.09.03-008"},
		{Domain: "library", CLIVersion: "", OperatorVersion: "2026.09.03-007"},
	}
	var out bytes.Buffer
	Render(entries, &out)
	text := out.String()
	if !strings.Contains(text, "audio") || !strings.Contains(text, "drift") {
		t.Fatalf("drift must be marked:\n%s", text)
	}
	if !strings.Contains(text, "library") || !strings.Contains(text, "sync") {
		t.Fatalf("a missing CLI must be named with the sync hint:\n%s", text)
	}
}
