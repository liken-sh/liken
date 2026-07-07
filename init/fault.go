package main

// Fault injection, for drilling the fallback machinery.
//
// A blue-green upgrade system has to handle its worst releases, and
// the only way to trust the fallbacks is to ship releases built to
// fail and confirm the machine survives them. The releases domain
// stamps this variable at link time (`make release VERSION=x
// FAULT=...`), exactly the way the version is stamped: the fault is
// part of the binary, so the broken release is broken everywhere it
// boots, reproducibly.
//
// Two faults cover the two fallback paths:
//
//	panic      init panics immediately at startup. PID 1 dying
//	           panics the kernel, the baked panic=10 argument
//	           reboots it ten seconds later, and the firmware falls
//	           back to BootOrder and the proven slot, because its
//	           one-shot BootNext was already consumed. No liken code
//	           participates in the recovery, which is exactly what
//	           this fault exists to demonstrate.
//
//	wedge-k3s  the machine boots fine but k3s never starts, so the
//	           node never joins and the operator that would promote
//	           the release never runs. BootNext can't detect this
//	           failure, because the kernel is healthy and nothing
//	           panics. The proving watchdog exists for exactly this
//	           case: it waits ten minutes, then deliberately reboots
//	           onto the proven slot.
//
// An empty value, which every real release has, injects nothing.
var fault = ""
