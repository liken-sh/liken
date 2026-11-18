package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

// The scan command runs Trivy on one published image of the
// repository, for the root workflow's trivy job. The command fails for
// an image that is not in the repository, and when no version is given.
func TestScanRunsTrivyOnOnePublishedImage(t *testing.T) {
	root := planFixture(t).git.Dir
	sarif := t.TempDir()
	cases := []struct {
		name     string
		args     []string
		fails    bool
		commands []string
	}{
		{"an image of the repository", []string{"-image", "operator", "-version", "2026.10.02-001", "-sarif", sarif}, false, []string{
			"trivy image --scanners vuln --ignore-unfixed --format sarif --output " + filepath.Join(sarif, "operator.sarif") + " ghcr.io/liken-sh/operator:2026.10.02-001",
		}},
		{"an image of no component", []string{"-image", "nope", "-version", "2026.10.02-001", "-sarif", sarif}, true, nil},
		{"no version", []string{"-image", "operator", "-sarif", sarif}, true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newFakeEnv(t, &fakeRegistry{})
			err := w.run(append([]string{"scan", "-root", root}, c.args...))
			if (err != nil) != c.fails || !reflect.DeepEqual(w.recorded.commands, c.commands) {
				t.Errorf("the scan command returned %v and ran %v, want failure %v and the commands %v", err, w.recorded.commands, c.fails, c.commands)
			}
		})
	}
}

// A scan only reports, so when it fails, for example because Trivy
// cannot download its database, the scan command prints a warning and
// succeeds, and so does the job that runs it.
func TestAScanThatFailsDoesNotFailTheScanCommand(t *testing.T) {
	root := planFixture(t).git.Dir
	w := newFakeEnv(t, &fakeRegistry{})
	runner := &failingTrivy{}
	w.Run = runner.run
	if err := w.run([]string{"scan", "-root", root, "-image", "base", "-version", "2026.10.02-001", "-sarif", t.TempDir()}); err != nil {
		t.Fatalf("running the scan command: %v", err)
	}
	if len(runner.commands) != 1 {
		t.Errorf("the scan command ran %v, want one trivy run", runner.commands)
	}
}
