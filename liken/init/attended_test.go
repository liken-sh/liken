package main

// Tests for the attended-boot contract: who a terminal message reaches,
// and when the machine waits for an answer. The real console is a
// device the kernel opened for PID 1; these tests point consoleDevice
// at ordinary files, which answer a read at once, so a test can prove
// the hold's decision without ever risking a hang of its own.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pointConsoleAt puts the hold's console device at a path of the test's
// choosing, and restores the real one afterward.
func pointConsoleAt(t *testing.T, path string) {
	t.Helper()
	old := consoleDevice
	consoleDevice = path
	t.Cleanup(func() { consoleDevice = old })
}

// fakeConsole points the hold at a file that stands in for the console
// device, and returns a reader for what landed on it.
func fakeConsole(t *testing.T) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "console")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	pointConsoleAt(t, path)
	return func() string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
}

// fakeLogStream points init's log stream at a file, the same
// reassignment redirectToKmsg makes at boot, and returns a reader for
// it. On a booted machine this stream reaches every console= the
// command line named.
func fakeLogStream(t *testing.T) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stderr")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
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
			t.Fatal(err)
		}
		return string(raw)
	}
}

func TestHoldInstallerConsoleReachesBothReadersWhenAttended(t *testing.T) {
	// The standard server console pair: the log copy reaches both
	// devices through kmsg, and the direct copy reaches the one that
	// /dev/console opens. A person watching either device sees the
	// prompt.
	fakeCmdline(t, "console=ttyS0 console=tty0 rdinit=/liken liken.install liken.attended\n")
	console := fakeConsole(t)
	logs := fakeLogStream(t)

	holdInstallerConsole("liken: press Enter to power off", false)

	if !strings.Contains(logs(), "press Enter to power off") {
		t.Errorf("the ordered copy must go out with the boot's other lines: %q", logs())
	}
	if !strings.Contains(console(), "press Enter to power off") {
		t.Errorf("the direct copy must reach the device that answers: %q", console())
	}
}

func TestHoldInstallerConsoleDoesNotWaitOnAnUnattendedInstall(t *testing.T) {
	// A hand-written or scripted install: an image build, a lab guest,
	// a PXE server. Nobody can press a key, so the message is reported
	// and the boot goes on to its power-off.
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.install\n")
	console := fakeConsole(t)
	logs := fakeLogStream(t)

	holdInstallerConsole("liken: installed to slot A; press Enter to power off", false)

	if !strings.Contains(logs(), "installed to slot A") {
		t.Errorf("an unattended install still reports its ending: %q", logs())
	}
	if console() != "" {
		t.Errorf("no prompt where nobody can answer it: %q", console())
	}
}

func TestHoldInstallerConsoleGivesUpWhenTheConsoleWillNotOpen(t *testing.T) {
	fakeCmdline(t, "rdinit=/liken liken.install liken.attended\n")
	pointConsoleAt(t, filepath.Join(t.TempDir(), "no-console-here"))
	logs := fakeLogStream(t)

	holdInstallerConsole("liken: press Enter to power off", false)

	out := logs()
	if !strings.Contains(out, "press Enter to power off") || !strings.Contains(out, "opening the console") {
		t.Errorf("a console that will not open is reported, and the boot goes on: %q", out)
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
			t.Errorf("%q: attended is %v", c.cmdline, got)
		}
	}
}
