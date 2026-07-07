package main

// Fault injection, for drilling the fallback machinery.
//
// A blue-green upgrade system is only as good as its worst release,
// and the only way to trust the fallbacks is to ship releases built
// to fail and watch the machine survive them. The releases domain
// stamps this variable at link time (`make release VERSION=x
// FAULT=...`), exactly the way the version is stamped: the fault is
// part of the binary, so the broken release is broken everywhere it
// boots, reproducibly.
//
// Two faults cover the two fallback paths:
//
//	panic      init panics at first breath. PID 1 dying panics the
//	           kernel, the baked panic=10 argument reboots it ten
//	           seconds later, and the firmware — its one-shot
//	           BootNext already consumed — falls back to BootOrder
//	           and the proven slot. No liken code participates in
//	           the recovery; that's the point.
//
//	wedge-k3s  the machine boots fine but k3s never starts, so the
//	           node never joins and the operator that would promote
//	           the release never runs. This is the failure BootNext
//	           can't see — the kernel is healthy, nothing panics —
//	           and it's what the proving watchdog exists for: ten
//	           minutes of patience, then a deliberate reboot onto
//	           the proven slot.
//
// An empty value — every real release — injects nothing.
var fault = ""
