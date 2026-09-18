package main

// This file is the base binary's own prefix walk. kubectl dispatches
// kubectl liken audio capture by finding kubectl-liken-audio on PATH,
// so the two-layer command falls out of kubectl's own rule with no
// code here. The walk below gives the bare liken and kubectl-liken
// names the same behavior: liken audio capture finds the same binary
// and runs it with capture. The walk reads the domain from the
// arguments, never from this program's own name, so it behaves the
// same under either name.

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/liken-sh/liken/plugins"
)

// findPlugin locates a domain's CLI. It reads PATH first, because a
// person who added the plugin directory to PATH expects that copy,
// and then the plugin directory itself, which is not always on PATH.
// It returns the path, or the empty string when neither holds the
// CLI.
func findPlugin(binDir, domain string) string {
	name := plugins.Name(domain)
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	candidate := filepath.Join(binDir, name)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return ""
}

// dispatchPlugin runs a domain's CLI with the remaining arguments, or
// reports that no plugin answers the domain. It replaces this process
// with the plugin (execTool), so the plugin owns the terminal and its
// exit code is the base binary's.
func dispatchPlugin(binDir, domain string, rest []string) (bool, error) {
	path := findPlugin(binDir, domain)
	if path == "" {
		return false, nil
	}
	return true, execTool(path, append([]string{plugins.Name(domain)}, rest...), os.Environ())
}
