package main

// The world report reads uname, the firmware variables, /proc, and the
// block devices. This test points the firmware and disk paths at fakes
// so that the report runs in an ordinary process, and it checks only
// that the report does not panic.

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
