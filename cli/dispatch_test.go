package main

// These tests cover the base binary's prefix walk: finding a domain's
// CLI and running it. main_test.go proves an unknown first argument
// with no plugin still reports an unknown command.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

// installFakePlugin writes an executable file for one domain into a
// directory, the way sync installs a real CLI.
func installFakePlugin(t *testing.T, dir, domain string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kubectl-liken-"+domain), []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestFindPluginReadsTheBinDir(t *testing.T) {
	binDir := t.TempDir()
	installFakePlugin(t, binDir, "audio")
	if got := findPlugin(binDir, "audio"); got != filepath.Join(binDir, "kubectl-liken-audio") {
		t.Fatalf("got %q", got)
	}
	if got := findPlugin(binDir, "library"); got != "" {
		t.Fatalf("a domain with no CLI must find nothing: %q", got)
	}
}

func TestFindPluginPrefersPath(t *testing.T) {
	pathDir := t.TempDir()
	installFakePlugin(t, pathDir, "audio")
	t.Setenv("PATH", pathDir)
	if got := findPlugin(t.TempDir(), "audio"); got != filepath.Join(pathDir, "kubectl-liken-audio") {
		t.Fatalf("a CLI on PATH must win: %q", got)
	}
}

func TestDispatchPluginExecsTheCLI(t *testing.T) {
	binDir := t.TempDir()
	installFakePlugin(t, binDir, "audio")
	var gotArgv []string
	execTool = func(path string, argv []string, env []string) error {
		gotArgv = argv
		return nil
	}
	defer func() { execTool = unix.Exec }()

	dispatched, err := dispatchPlugin(binDir, "audio", []string{"capture", "room"})
	if err != nil || !dispatched {
		t.Fatalf("dispatched=%v err=%v", dispatched, err)
	}
	if !slices.Equal(gotArgv, []string{"kubectl-liken-audio", "capture", "room"}) {
		t.Fatalf("argv: %v", gotArgv)
	}
}

func TestDispatchPluginReportsNoPlugin(t *testing.T) {
	dispatched, err := dispatchPlugin(t.TempDir(), "audio", nil)
	if dispatched || err != nil {
		t.Fatalf("no CLI must not dispatch: dispatched=%v err=%v", dispatched, err)
	}
}

func TestRunWalksAnUnknownCommandToItsPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// The domain must not resolve on PATH, so the walk falls to the
	// plugin directory. An empty PATH guarantees that.
	t.Setenv("PATH", "")
	installFakePlugin(t, filepath.Join(home, ".liken", "plugins", "bin"), "audio")

	var gotArgv []string
	execTool = func(path string, argv []string, env []string) error {
		gotArgv = argv
		return nil
	}
	defer func() { execTool = unix.Exec }()

	if err := run([]string{"audio", "capture", "room"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(gotArgv, []string{"kubectl-liken-audio", "capture", "room"}) {
		t.Fatalf("argv: %v", gotArgv)
	}
}
