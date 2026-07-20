package main

// The world report only reads: the kernel's uname, the firmware's
// variables, /proc, and the block device inventory. This test points
// the firmware and disk paths at fakes, which makes the whole report
// runnable as an ordinary process. What the report prints on real
// hardware is tested by the QEMU harness, but every reader function
// it calls is shared with the facts code. So running the report here
// confirms that it never writes and never panics.

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
