package main

// Tests for the boot-time sysctl application: failures are reported
// and skipped, never fatal. The QEMU harness tests the success path,
// which writes real kernel parameters. What this test confirms is
// that a bad parameter name cannot stop a boot.

import "testing"

func TestApplySysctlsSkipsFailuresAndContinues(t *testing.T) {
	applySysctls(map[string]string{
		"liken.test.no.such.parameter": "1", // fails to open; init reports and skips it
		"../escape/attempt":            "1", // the traversal guard refuses this path
	})
}
