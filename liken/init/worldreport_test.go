package main

// The world report reads uname, the firmware variables, /proc, and the
// block devices. This test points the firmware, command line, mount
// table, and disk paths at fakes, so the report runs in an ordinary
// process and prints none of the workstation's mounts.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorldReportReadsTheWholeMachine(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	fakeFirmwareVars(t, map[string][]byte{
		"Boot0000":    encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot A"}),
		"BootCurrent": u16le(0x0000),
		"BootOrder":   u16le(0x0000),
	})
	fakeCmdline(t, "console=ttyS0 rdinit=/liken\n")
	mounts := filepath.Join(t.TempDir(), "mounts")
	if err := os.WriteFile(mounts, []byte("proc /proc proc rw 0 0\n"), 0o644); err != nil {
		t.Fatalf("writing the mount table: %v", err)
	}
	old := selfMountsPath
	selfMountsPath = mounts
	t.Cleanup(func() { selfMountsPath = old })

	got := capturedStdout(t, worldReport)

	for _, want := range []string{
		"liken: firmware: booted via Boot0000 (liken slot A)\n",
		"liken: cmdline: console=ttyS0 rdinit=/liken\n",
		"liken: mounts:\nliken:   proc /proc proc rw 0 0\n",
		"liken: disk " + filepath.Join(dev, "vda") + ": 2.0 GiB\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the world report has no line %q. It printed\n%s", want, got)
		}
	}
}
