package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

// The scan command runs Trivy on one published image of the
// repository, for the root workflow's trivy job. A request that names
// no image of the repository, or no version, fails.
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
			w := newWorld(t, &fakeRegistry{})
			err := w.run(append([]string{"scan", "-root", root}, c.args...))
			if (err != nil) != c.fails || !reflect.DeepEqual(w.recorded.commands, c.commands) {
				t.Errorf("error %v, commands %v", err, w.recorded.commands)
			}
		})
	}
}

// A scan reports and does not gate, so a scan that fails, as when
// Trivy cannot download its database, warns and does not fail the
// command or the job that runs it.
func TestAScanThatFailsDoesNotFailTheScanCommand(t *testing.T) {
	root := planFixture(t).git.Dir
	w := newWorld(t, &fakeRegistry{})
	runner := &failingTrivy{}
	w.Run = runner.run
	if err := w.run([]string{"scan", "-root", root, "-image", "base", "-version", "2026.10.02-001", "-sarif", t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Errorf("commands %v", runner.commands)
	}
}
