package main

import (
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// The phase is one word that summarizes a machine's conditions for the
// fleet listing, like a Pod's status.phase. The operator computes it
// from the conditions on every pass, so it cannot disagree with them.
//
// This operator never reports Lost, because a machine whose operator
// runs is not lost. The cluster operator writes Lost for a machine
// that stops reporting, and this operator overwrites it when the
// machine reports again.

// phasePrecedence lists the phases from most severe to least severe.
// When conditions indicate different phases, the machine reports the
// first one in this order. Downloading is last because a download
// finishes without help.
var phasePrecedence = []api.Phase{
	api.PhaseUnknown,
	api.PhaseBooting,
	api.PhaseBlocked,
	api.PhaseUpdating,
	api.PhaseUpdatePending,
	api.PhaseDegraded,
	api.PhaseDownloading,
}

// conditionPhase returns the phase that one condition indicates, or ""
// when the condition is True, is Ready itself, or is one that
// reportsOnly accepts. It switches on the reason because a False
// status alone does not tell RebootPending, which a reboot clears,
// from RejectedLastBoot, which stays until someone edits the spec.
func conditionPhase(c api.Condition) api.Phase {
	if c.Type == "Ready" || reportsOnly(c.Type) || c.Status == api.ConditionTrue {
		return ""
	}
	switch c.Reason {
	case "Joining":
		// WirelessJoined is False with this reason while a radio is
		// still associating: init joins it in the background when a
		// wired interface reaches the cluster, and the supplicant
		// joins again after it loses an access point. So it is not a
		// fault, and Degraded on every boot would teach people to
		// ignore Degraded.
		return ""
	case "FactsUnreadable":
		return api.PhaseUnknown
	case "FactsIncomplete":
		// The facts file has no boot record yet while init starts.
		return api.PhaseBooting
	case "RejectedLastBoot", "StagingRejected", "BootMismatch", "MachineStateEphemeral",
		"NoSystemSlots", "NotInstalled", "NoReleaseSource", "VersionNotInCatalog", "DigestMismatch",
		"CredentialsInvalid", "RetractionBlocked":
		// The machine differs from its spec and cannot stage the
		// change, and waiting does not fix it. A person has to change
		// the spec, the catalog, the credentials Secret, or the
		// machine. RetractionBlocked means a feature is being turned
		// off while objects that only its controller can delete still
		// exist. DigestMismatch stays until the catalog names other
		// bytes, because fetching again returns the same bytes.
		return api.PhaseBlocked
	case "Downloading", "DownloadRetrying":
		// A release downloads to the inactive slot while the workloads
		// keep running. The cluster operator counts a Downloading
		// machine as available (cluster-operator/rollout.go), so a
		// download uses none of the disruption budget.
		return api.PhaseDownloading
	case "RebootRequested", "RestartRequested", "DemotionRebooting", "Draining",
		"Proving", "LoadRequested":
		// A change is in progress. Draining happens only before a
		// reboot, and a k3s restart keeps the pods running. Proving
		// lasts from a proving boot until the node's OS containers are
		// Ready. LoadRequested applies the spec without a reboot.
		return api.PhaseUpdating
	case "RebootPending", "RestartPending", "DemotionPending", "AwaitingTurn",
		"StagedForNextBoot", "AwaitingPodRefresh":
		// A change is staged and waits for a Manual reboot or for the
		// cluster operator to grant this machine its turn.
		// StagedForNextBoot needs no reboot of its own, because any
		// later boot applies it, but the machine does not run the new
		// document yet. AwaitingPodRefresh means the machine runs the
		// new release and this operator's pod still has the old
		// template, until a leader boots the release.
		return api.PhaseUpdatePending
	}
	// An unlisted reason is Degraded, so a new reason that someone
	// forgets to add here shows as a fault in the listing and does not
	// pass as Ready.
	return api.PhaseDegraded
}

// readyCondition returns the Ready condition: True when every other
// condition is True, and otherwise False with the phase as its reason
// and the last condition that is not True in its message. So a machine
// that waits for its reboot turn reads Ready=False with reason
// UpdatePending, not Degraded.
//
// It skips the previous Ready, RebootApproved, which grants a reboot
// turn and reports nothing about health, and the conditions that
// reportsOnly accepts.
func readyCondition(conditions []api.Condition) api.Condition {
	ready := api.Condition{Type: "Ready", Status: api.ConditionTrue, Reason: "Reconciled"}
	for _, condition := range conditions {
		if condition.Type == "Ready" || condition.Type == machine.RebootApprovedCondition ||
			reportsOnly(condition.Type) {
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

// decidePhase returns the most severe phase that any condition
// indicates, or Ready when none indicates a phase.
func decidePhase(conditions []api.Condition) api.Phase {
	indicated := map[api.Phase]bool{}
	for _, c := range conditions {
		if phase := conditionPhase(c); phase != "" {
			indicated[phase] = true
		}
	}
	for _, phase := range phasePrecedence {
		if indicated[phase] {
			return phase
		}
	}
	return api.PhaseReady
}

// reportsOnly reports whether the phase and Ready ignore a condition
// type. A machine with an unplugged or refused serio adapter keeps
// working. An outage of the upstream time servers makes every leader
// report ClockSynchronized False at once, and a fleet that turns
// Degraded together gets no reboot turns.
func reportsOnly(conditionType string) bool {
	return conditionType == serioAttachedCondition || conditionType == clockSynchronizedCondition
}
