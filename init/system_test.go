package main

// The boot-time sysctl application: failures are reported and
// skipped, never fatal. The success path writes real kernel
// parameters and belongs to the QEMU harness; what's pinned here is
// that a bad name can't stop a boot.

import "testing"

func TestApplySysctlsSkipsFailuresAndContinues(t *testing.T) {
	applySysctls(map[string]string{
		"liken.test.no.such.parameter": "1", // fails to open; reported, skipped
		"../escape/attempt":            "1", // refused by the traversal guard
	})
}
