package main

// Tests for the dispatcher: each command routes to its capability
// with the arguments checked first. The capabilities test themselves
// in their own packages; what's under test here is only the table.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRequiresACommand(t *testing.T) {
	err := run(nil)
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Errorf("no arguments should ask for a command: %v", err)
	}
}

func TestRunRefusesAnUnknownCommand(t *testing.T) {
	err := run([]string{"launder"})
	if err == nil || !strings.Contains(err.Error(), `unknown command "launder"`) {
		t.Errorf("unknown command was not refused: %v", err)
	}
}

func TestRunChecksArgumentCounts(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"mint without a directory", []string{"mint"}},
		{"adopt without directories", []string{"adopt", "only-one"}},
		{"kubeconfig without a directory", []string{"kubeconfig"}},
		{"layer without its inputs", []string{"layer", "manifests", "identity"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := run(c.args)
			if err == nil || !strings.Contains(err.Error(), "usage:") {
				t.Errorf("bad arguments were not refused: %v", err)
			}
		})
	}
}

func TestRunMintsAndComputesAKubeconfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identity")
	if err := run([]string{"mint", dir}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"kubeconfig", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kubeconfig")); err != nil {
		t.Error("no kubeconfig was written")
	}
}

func TestRunPacksADeploymentLayer(t *testing.T) {
	identityDir := filepath.Join(t.TempDir(), "identity")
	if err := run([]string{"mint", identityDir}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "deployment.cpio")
	// A deployment with no manifests declares no modules, so the
	// kernel dist is never consulted and the layer is identity only.
	if err := run([]string{"layer", t.TempDir(), identityDir, "unused", out}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Error("no layer was written")
	}
}

func TestRunReportsTheVersion(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Error(err)
	}
}
