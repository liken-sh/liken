package main

import (
	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

// The machine's phase: the whole set of conditions summarized in
// one word.
//
// Conditions are the machine's full account of itself, typed,
// reasoned, and timestamped, and programs consume them (kubectl
// wait, controllers, the Ready roll-up). But a person scanning a
// fleet listing does not want five columns of True and False. They
// want one word per machine that says whether it needs attention.
// Pods solved this with status.phase (Pending, Running, Succeeded,
// Failed), and the Machine borrows this idea exactly. The operator
// derives the phase from the conditions on every pass, and never
// stores it, so it can never disagree with them.
//
// One phase is deliberately missing from this table: Lost. A
// machine cannot derive its own death from its own conditions,
// because if this code is running, the machine is not lost. The
// cluster operator writes Lost on a silent machine's behalf, and
// the machine's own operator overwrites it the moment the machine
// reports again.

// phasePrecedence orders the phases from most severe to least
// severe. When several conditions point at different phases, the
// machine reports the most severe one. A machine that is both
// waiting on a Manual reboot and failing a sysctl is UpdatePending
// and Degraded at the same time. The listing should show the one
// that needs a person soonest.
var phasePrecedence = []api.Phase{
	api.PhaseUnknown,
	api.PhaseBooting,
	api.PhaseBlocked,
	api.PhaseUpdating,
	api.PhaseUpdatePending,
	api.PhaseDegraded,
}

// conditionPhase maps one condition to the phase it indicates. It
// returns "" when the condition indicates nothing: when it is True,
// or when it is the Ready roll-up, which summarizes the others and
// would double-count. The mapping works by reason, because the
// reasons already distinguish what the boolean status cannot.
// RebootPending and RejectedLastBoot are both "not converged," but
// one resolves with a reboot and the other never will without a
// different edit.
func conditionPhase(c api.Condition) api.Phase {
	if c.Type == "Ready" || c.Status == api.ConditionTrue {
		return ""
	}
	switch c.Reason {
	case "Joining":
		// A radio the boot handed to the background is still
		// associating. The condition stays False, because it reports
		// what the boot actuated and the join has not finished, but
		// the machine is not degraded: the boot went on without the
		// radio on purpose, and a join takes seconds. The fleet
		// listing must not show a fault for that window: a Degraded
		// that appears on every boot and clears itself teaches
		// people to ignore Degraded.
		return ""
	case "FactsUnreadable":
		// The operator is running but cannot read the facts, so it
		// has no information about the machine it runs on.
		return api.PhaseUnknown
	case "FactsIncomplete":
		// Facts exist but carry no boot record yet. init is still
		// starting up.
		return api.PhaseBooting
	case "RejectedLastBoot", "StagingRejected", "BootMismatch", "MachineStateEphemeral",
		"NoSystemSlots", "NotInstalled", "NoReleaseSource", "VersionNotInCatalog", "DigestMismatch",
		"CredentialsInvalid", "RetractionBlocked":
		// Drift exists, but liken refuses to stage it, or cannot.
		// Time will not fix these; a different edit will. A blocked
		// retraction takes an edit to the cluster's objects rather
		// than to a document: a feature stays enabled while objects
		// only its controller can remove still exist. The
		// version target can get stuck in several ways: no slots to
		// hold a release, a boot that did not come from a slot (so
		// no boot entry could ever run the download), a catalog with
		// no source or without the target, or a download whose bytes
		// do not match the catalog's digest. That last case is
		// corrupt at the source, where refetching cannot change what
		// the server publishes. A malformed credentials Secret has
		// the same shape: only a corrected Secret fixes it.
		return api.PhaseBlocked
	case "RebootRequested", "RestartRequested", "DemotionRebooting", "Draining", "Downloading",
		"Proving", "LoadRequested":
		// A disruption is in progress; the machine is in the middle
		// of a change. Draining is the first step of a reboot: the
		// node is cordoned and its workloads are being evicted
		// before the machine goes down (a k3s restart skips this
		// step, and pods survive). Downloading is the version
		// target's equivalent. The change is arriving over the
		// network instead of waiting on a reboot, but the machine is
		// just as much in the middle of a change. So is Proving: a
		// boot's imports stay on trial until the OS pods prove them,
		// which ordinarily takes seconds. LoadRequested is the live
		// load's moment in flight, usually under a second, and a
		// machine applying its spec in place is updating, not
		// degraded.
		return api.PhaseUpdating
	case "RebootPending", "RestartPending", "DemotionPending", "AwaitingTurn",
		"StagedForNextBoot", "AwaitingPodRefresh":
		// A change is staged and waiting, either on a Manual reboot
		// or on the cluster granting this machine its turn. A
		// verified release waiting for its proving reboot reads the
		// same way, because it is waiting on exactly the same things.
		// A change staged for the next boot needs no disruption of its
		// own, so nothing is scheduled to apply it and any later boot
		// does. The listing still shows the machine as pending,
		// because it does not yet run the document the deployment
		// wrote. AwaitingPodRefresh waits the same way: the machine
		// already runs the new release, and only the pod steward's
		// refresh, after a leader boots that release, is left.
		return api.PhaseUpdatePending
	}
	// Anything unrecognized reads as Degraded, deliberately. A
	// reason missing from this table fails visibly in the fleet
	// listing, instead of passing silently as Ready.
	return api.PhaseDegraded
}

// readyCondition is the Ready roll-up: True exactly when every other
// condition is True. The scan skips any prior Ready, so the previous
// pass's value cannot affect this one. It also skips the conductor's
// grant, because the grant is a permission, not an observation about
// this machine's health.
//
// When something is False, the reason is the phase word the
// conditions argue for (decidePhase), so the roll-up and the phase
// can never disagree about how bad things are. A machine waiting on
// its reboot turn reads Ready=False with reason UpdatePending, and
// Degraded appears on the roll-up exactly when the phase says
// Degraded. The message still names a failing condition, which is
// where the detail lives.
func readyCondition(conditions []api.Condition) api.Condition {
	ready := api.Condition{Type: "Ready", Status: api.ConditionTrue, Reason: "Reconciled"}
	for _, condition := range conditions {
		if condition.Type == "Ready" || condition.Type == machine.RebootApprovedCondition {
			continue
		}
		if condition.Status != api.ConditionTrue {
			ready = api.Condition{
				Type: "Ready", Status: api.ConditionFalse,
				Reason:  string(decidePhase(conditions)),
				Message: condition.Type + " is " + string(condition.Status),
			}
		}
	}
	return ready
}

// decidePhase reduces the conditions to the single most severe
// phase, or Ready when nothing indicates otherwise.
func decidePhase(conditions []api.Condition) api.Phase {
	argued := map[api.Phase]bool{}
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
	return api.PhaseReady
}
