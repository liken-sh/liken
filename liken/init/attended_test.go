package main

// The tests point consoleDevice at ordinary files, which return from a
// read at once, so a test of the wait for a keypress cannot hang.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pointConsoleAt(t *testing.T, path string) {
	t.Helper()
	old := consoleDevice
	consoleDevice = path
	t.Cleanup(func() { consoleDevice = old })
}

func fakeConsole(t *testing.T) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "console")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("creating the fake console: %v", err)
	}
	pointConsoleAt(t, path)
	return func() string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the fake console: %v", err)
		}
		return string(raw)
	}
}

// fakeLogStream replaces os.Stderr with a file, as redirectToKmsg does
// at boot, and returns a function that reads it. On a booted machine
// this stream reaches every console= device.
func fakeLogStream(t *testing.T) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stderr")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the fake log stream: %v", err)
	}
	old := os.Stderr
	os.Stderr = f
	t.Cleanup(func() {
		os.Stderr = old
		f.Close()
	})
	return func() string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the fake log stream: %v", err)
		}
		return string(raw)
	}
}

func TestHoldInstallerConsoleReachesBothReadersWhenAttended(t *testing.T) {
	// A server with two console= devices: the log copy reaches both
	// through kmsg, and the direct copy reaches the one that
	// /dev/console opens, so a person at either device sees the prompt.
	fakeCmdline(t, "console=ttyS0 console=tty0 rdinit=/liken liken.install liken.attended\n")
	console := fakeConsole(t)
	logs := fakeLogStream(t)

	holdInstallerConsole("liken: press Enter to power off", false)

	if !strings.Contains(logs(), "press Enter to power off") {
		t.Errorf("the log stream is %q, want the message among the boot's other lines", logs())
	}
	if !strings.Contains(console(), "press Enter to power off") {
		t.Errorf("the console is %q, want the message written to it directly", console())
	}
}

func TestHoldInstallerConsoleDoesNotWaitOnAnUnattendedInstall(t *testing.T) {
	// A scripted install, such as an image build or a PXE boot, has
	// nobody to press a key, so the message is printed with no wait.
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.install\n")
	console := fakeConsole(t)
	logs := fakeLogStream(t)

	holdInstallerConsole("liken: installed to slot A. Press Enter to power off.", false)

	if !strings.Contains(logs(), "installed to slot A") {
		t.Errorf("the log stream is %q, want the ending message of the install", logs())
	}
	if console() != "" {
		t.Errorf("the console is %q, want nothing written to it", console())
	}
}

func TestHoldInstallerConsoleGivesUpWhenTheConsoleWillNotOpen(t *testing.T) {
	fakeCmdline(t, "rdinit=/liken liken.install liken.attended\n")
	pointConsoleAt(t, filepath.Join(t.TempDir(), "no-console-here"))
	logs := fakeLogStream(t)

	holdInstallerConsole("liken: press Enter to power off", false)

	out := logs()
	if !strings.Contains(out, "press Enter to power off") || !strings.Contains(out, "opening the console") {
		t.Errorf("the log stream is %q, want the message and a line about opening the console", out)
	}
}

func TestAttendedReadsTheMenusWord(t *testing.T) {
	cases := []struct {
		cmdline string
		want    bool
	}{
		{"rdinit=/liken liken.machine=node-1 liken.install liken.attended\n", true},
		{"rdinit=/liken liken.report liken.attended\n", true},
		{"rdinit=/liken liken.machine=node-1 liken.install\n", false},
		{"rdinit=/liken liken.slot=A\n", false},
	}
	for _, c := range cases {
		fakeCmdline(t, c.cmdline)
		if got := attended(); got != c.want {
			t.Errorf("attended() is %v for %q, want %v", got, c.cmdline, c.want)
		}
	}
}
