package main

// Fault injection, for testing the fallback machinery in drills.
//
// A blue-green upgrade system has to handle its worst releases, and
// the only way to trust the fallbacks is to ship releases built to
// fail and confirm that the machine survives them. The releases
// domain stamps this variable at link time (`make release VERSION=x
// FAULT=...`), the same way it stamps the version. The fault is part
// of the binary, so the broken release fails the same way,
// reproducibly, everywhere it boots.
//
// Two faults cover the two fallback paths:
//
//	panic      init panics immediately at startup. PID 1 dying
//	           panics the kernel. The baked panic=10 argument
//	           reboots the kernel ten seconds later, and the
//	           firmware falls back to BootOrder and the proven
//	           slot, because the firmware already consumed its
//	           one-shot BootNext. No liken code takes part in the
//	           recovery, which is exactly what this fault exists to
//	           show.
//
//	wedge-k3s  the machine boots fine but k3s never starts, so the
//	           node never joins and the operator that would promote
//	           the release never runs. BootNext cannot detect this
//	           failure, because the kernel is healthy and nothing
//	           panics. The proving watchdog exists for exactly this
//	           case: it waits ten minutes, then deliberately reboots
//	           onto the proven slot.
//
// An empty value, which every real release has, injects no fault.
var fault = ""
