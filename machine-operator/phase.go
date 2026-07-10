package main

// The machine's phase: the whole set of conditions summarized in one
// word.
//
// Conditions are the machine's full account of itself, typed,
// reasoned, and timestamped, and they're what programs consume
// (kubectl wait, controllers, the Ready roll-up). But a human
// scanning a fleet listing doesn't want five columns of True and
// False; they want one word per machine that says whether it needs
// attention. Pods solved this with status.phase (Pending, Running,
// Succeeded, Failed), and the Machine borrows the idea exactly: the
// phase is derived from the conditions on every pass and never
// remembered, so it can never disagree with them.
//
// One phase is deliberately missing from this table: Lost. A machine
// cannot derive its own death from its own conditions, because if
// this code is running, the machine isn't lost. Lost is written by
// the cluster operator on a silent machine's behalf, and overwritten
// by the machine's own operator the moment it reports again.

import (
	"github.com/chrisguidry/liken/machine"
)

// phasePrecedence orders the phases most-severe-first: when several
// conditions point at different phases, the machine reports the
// gravest one. A machine that is both waiting on a Manual reboot and
// failing a sysctl is UpdatePending *and* Degraded; the listing
// should show the one that needs a human soonest.
var phasePrecedence = []machine.Phase{
	machine.PhaseUnknown,
	machine.PhaseBooting,
	machine.PhaseBlocked,
	machine.PhaseUpdating,
	machine.PhaseUpdatePending,
	machine.PhaseDegraded,
}

// conditionPhase maps one condition to the phase it argues for, ""
// when it argues for nothing (it's True, or it's the Ready roll-up,
// which is a summary of the others and would double-count). The
// mapping is by reason, because the reasons already distinguish what
// the boolean status can't: RebootPending and RejectedLastBoot are
// both "not converged", but one resolves with a reboot and the other
// never will without a different edit.
func conditionPhase(c machine.Condition) machine.Phase {
	if c.Type == "Ready" || c.Status == machine.ConditionTrue {
		return ""
	}
	switch c.Reason {
	case "FactsUnreadable":
		// The operator is running but cannot read the facts, so it
		// knows nothing about the machine it stands on.
		return machine.PhaseUnknown
	case "FactsIncomplete":
		// Facts exist but carry no boot record yet: init is still
		// working its way up.
		return machine.PhaseBooting
	case "RejectedLastBoot", "StagingRejected", "BootMismatch", "MachineStateEphemeral",
		"NoSystemSlots", "NotInstalled", "NoReleaseSource", "VersionNotInCatalog", "DigestMismatch",
		"CredentialsInvalid":
		// Drift exists but liken refuses or is unable to stage it.
		// Time won't fix these; a different edit will. The version
		// target can be stuck several ways: no slots to hold a
		// release, a boot that didn't come from a slot (so no boot
		// entry could ever run the download), a catalog with no
		// source or without the target, and a download whose bytes
		// don't match the catalog's digest. That last one is corrupt
		// at the source, where refetching can't change what the
		// server publishes. A malformed credentials Secret is the
		// same shape: only a corrected Secret fixes it.
		return machine.PhaseBlocked
	case "RebootRequested", "RestartRequested", "DemotionRebooting", "Draining", "Downloading":
		// A disruption is in flight; the machine is mid-change.
		// Draining is the first step of a reboot: the node is
		// cordoned and its workloads are being evicted before the
		// machine goes down (a k3s restart skips it; pods survive).
		// Downloading is the version target's equivalent. The change
		// is arriving over the network instead of waiting on a
		// reboot, but the machine is just as much mid-change.
		return machine.PhaseUpdating
	case "RebootPending", "RestartPending", "DemotionPending", "AwaitingTurn":
		// A change is staged and waiting, either on a Manual reboot
		// or on the cluster granting this machine its turn. A
		// verified release waiting for its proving reboot reads the
		// same way, because it is waiting on exactly the same things.
		return machine.PhaseUpdatePending
	}
	return machine.PhaseDegraded
}

// decidePhase reduces the conditions to the single gravest phase,
// Ready when nothing argues otherwise.
func decidePhase(conditions []machine.Condition) machine.Phase {
	argued := map[machine.Phase]bool{}
	for _, c := range conditions {
		if phase := conditionPhase(c); phase != "" {
			argued[phase] = true
		}
	}
	for _, phase := range phasePrecedence {
		if argued[phase] {
			return phase
		}
	}
	return machine.PhaseReady
}
