package main

// The world report reads only: the kernel's uname, the firmware's
// variables, /proc, and the block inventory. Pointing the firmware
// and disk paths at fakes makes the whole report exercisable as an
// ordinary process; what it prints for real is QEMU territory, but
// every reader it calls through is shared with the facts, so running
// it here pins that the report never writes and never panics.

import "testing"

func TestWorldReportReadsTheWholeMachine(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	fakeFirmwareVars(t, map[string][]byte{
		"Boot0000":    encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot A"}),
		"BootCurrent": u16le(0x0000),
		"BootOrder":   u16le(0x0000),
	})
	worldReport()
}
