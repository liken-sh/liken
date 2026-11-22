package main

// The Events that the machine operator posts about its own Machine,
// which `kubectl describe machine` lists.
//
// A Machine is cluster-scoped, so its Events are in the namespace
// default. `kubectl events --for machine/<name>` finds them only with
// -n default or -A.
//
// The operator posts an Event for:
//
//   - Each condition transition, after the status write succeeds. The
//     Event's reason is the condition's reason, such as
//     StagingRejected, RejectedLastBoot, or RebootRequested. When Ready
//     is not True its reason is the phase, so Ready's Events record
//     the phase changes. RebootApproved posts nothing here, because
//     the cluster operator sets it and posts its own Event.
//   - A new kernel crash record and a new refused boot, which are
//     status fields and not conditions.
//   - The cordon before a drain, and the uncordon after the reboot.
//   - The creation of the Machine from the boot manifest.
//   - A radio's new association with an access point.
//   - A prepared claim's device that was unplugged.
//   - A write by a backstop pass.
//   - A proven cluster document replaced with the running one.
//
// An action that repeats on each pass posts nothing: a refused
// eviction, a sysctl written again, or a heartbeat renewal.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// The reasons of the Events that are not condition transitions.
const (
	reasonMachineJoined = "MachineJoined"
	reasonKernelCrashed = "KernelCrashed"
	reasonBootRefused   = "BootRefused"
	reasonCordoned      = "Cordoned"
	reasonUncordoned    = "Uncordoned"
	reasonRoamed        = "Roamed"
	reasonAssociated    = "Associated"

	// reasonDeviceUnplugged is for a device that was unplugged while
	// a prepared claim used it (unplugged.go).
	reasonDeviceUnplugged = "DeviceUnplugged"

	// reasonClusterProvenRepaired is for a proven.yaml that the pass
	// replaced with the running cluster document (cluster.go).
	reasonClusterProvenRepaired = "ClusterProvenRepaired"

	// reasonBackstopRepaired is for a write by a backstop pass, which
	// means that a code path in this operator changed state without
	// waking the loop (backstop.go).
	reasonBackstopRepaired = "BackstopRepaired"
)

func machineReference(m *machine.Machine) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: api.APIVersion, Kind: machineKind,
		Name: m.Metadata.Name, UID: m.Metadata.UID,
	}
}

// machineEvents posts the Events about one Machine. The zero value
// posts nothing, because a nil recorder posts nothing.
type machineEvents struct {
	recorder *events.Recorder
	machine  events.ObjectReference
}

func (e machineEvents) normal(reason, message string) {
	e.recorder.Normal(e.machine, reason, message)
}

func (e machineEvents) warning(reason, message string) {
	e.recorder.Warning(e.machine, reason, message)
}

// postStatusEvents posts the Events for a status write that
// succeeded, by comparing the written status with the stored one.
func postStatusEvents(e machineEvents, stored, written *machine.MachineStatus) {
	for _, c := range api.Transitions(stored.Conditions, written.Conditions) {
		if c.Type == machine.RebootApprovedCondition {
			continue
		}
		e.recorder.Transition(e.machine, transitionEvent(c), badStatus(c))
	}
	if crash := written.LastCrash; crash != nil && crash.Time != nil && !sameCrash(stored.LastCrash, crash) {
		e.recorder.Warning(e.machine, reasonKernelCrashed, fmt.Sprintf(
			"The kernel recorded a %s at %s: %s. status.lastCrash has the records",
			crash.Reason, crash.Time.UTC().Format(time.RFC3339), crash.Message))
	}
	if stop := written.LastFailStop; stop != nil && (stored.LastFailStop == nil || !stored.LastFailStop.Time.Equal(stop.Time)) {
		e.recorder.Warning(e.machine, reasonBootRefused, fmt.Sprintf(
			"init refused the boot at %s and powered the machine off: %s",
			stop.Time.UTC().Format(time.RFC3339), stop.Reason))
	}
	postAssociationEvents(e, stored, written)
}

// postAssociationEvents posts an Event for each radio whose
// associatedAt changed, which init sets only on a new association.
// The reason is Roamed when the stored status had another BSSID, and
// Associated otherwise. A Roamed Event is the only record of the
// access point that the radio left, because status has only the
// current one.
func postAssociationEvents(e machineEvents, stored, written *machine.MachineStatus) {
	before := map[string]*machine.WirelessStatus{}
	for _, iface := range stored.Network.Interfaces {
		before[iface.Name] = iface.Wireless
	}
	for _, iface := range written.Network.Interfaces {
		now := iface.Wireless
		if now == nil || now.AssociatedAt == nil {
			continue
		}
		was := before[iface.Name]
		if was != nil && was.AssociatedAt != nil && was.AssociatedAt.Equal(*now.AssociatedAt) {
			continue
		}
		to := accessPoint(now)
		if was != nil && was.BSSID != "" && was.BSSID != now.BSSID {
			e.normal(reasonRoamed, fmt.Sprintf("%s moved from %s to %s", iface.Name, accessPoint(was), to))
			continue
		}
		message := fmt.Sprintf("%s associated with %s", iface.Name, to)
		if now.LastDisconnectReason != 0 {
			message += fmt.Sprintf(" after it lost its access point (%s)",
				machine.DisconnectReasonText(now.LastDisconnectReason))
		}
		e.normal(reasonAssociated, message)
	}
}

func accessPoint(w *machine.WirelessStatus) string {
	return fmt.Sprintf("%s on %d MHz (%s)", w.BSSID, w.Frequency, w.Band)
}

func sameCrash(stored, crash *machine.CrashStatus) bool {
	return stored != nil && stored.Time != nil && stored.Time.Equal(*crash.Time)
}

// transitionEvent prefixes the condition's message with its type and
// status, because several conditions use the same reasons, such as
// Converged, and the reason alone does not say which one changed.
func transitionEvent(c api.Condition) api.Condition {
	prefix := c.Type + " is " + string(c.Status)
	if c.Message == "" {
		c.Message = prefix
	} else {
		c.Message = prefix + ": " + c.Message
	}
	return c
}

// badStatus returns the status that events.Recorder.Transition posts
// as a Warning: c.Status when the condition's phase is Blocked,
// Degraded, or Unknown, and empty otherwise. A False condition in
// another phase, such as Updating, is a change in progress, so its
// Event is Normal.
func badStatus(c api.Condition) conditions.Status {
	phase := conditionPhase(c)
	if c.Type == "Ready" && c.Status != api.ConditionTrue {
		phase = api.Phase(c.Reason)
	}
	switch phase {
	case api.PhaseBlocked, api.PhaseDegraded, api.PhaseUnknown:
		return c.Status
	}
	return ""
}
