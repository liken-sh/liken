package main

// The Events the machine operator posts about its own Machine.
// `kubectl describe machine` lists them under the status, so a person
// reads what happened to a machine in the last hour with no log to
// open. kubernetes/events writes them, and root plan 78 gives the rule
// for a condition, an Event, and a log line.
//
// A Machine is cluster-scoped, so its Events are in the namespace
// default. `kubectl describe` finds them there, and `kubectl events
// --for machine/<name>` finds them only with -n default or -A.
//
//   - Each condition transition posts one Event, with the condition's
//     reason, after the status write lands (postStatusEvents). The
//     transitions of Ready are the transitions of the phase, because
//     Ready's reason is the phase word when it is False. A spec that
//     the operator refuses to stage posts StagingRejected, and a
//     release or a spec that fell back at boot posts RejectedLastBoot.
//     A reboot requested to apply a document posts RebootRequested.
//     RebootApproved posts nothing here: the cluster operator writes
//     it and posts its own Events.
//   - A new kernel crash record and a new refused boot each post one
//     Event, because both arrive as status fields and not as
//     conditions.
//   - The operator's own actions on the Node post one Event each:
//     the cordon before a drain and the uncordon after the reboot.
//   - The creation of the Machine from the boot manifest posts one
//     Event.
//
// Nothing that repeats on each pass posts an Event: a refused
// eviction, a sysctl written back, or a heartbeat renewal.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// The reasons of the Events that are not condition transitions. A
// transition's Event takes the condition's own reason.
const (
	reasonMachineJoined = "MachineJoined"
	reasonKernelCrashed = "KernelCrashed"
	reasonBootRefused   = "BootRefused"
	reasonCordoned      = "Cordoned"
	reasonUncordoned    = "Uncordoned"
)

// machineReference answers the object reference of a Machine, for an
// Event about it.
func machineReference(m *machine.Machine) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: api.APIVersion, Kind: machineKind,
		Name: m.Metadata.Name, UID: m.Metadata.UID,
	}
}

// machineEvents posts the Events about one Machine. The zero value
// posts nothing, because a nil recorder posts nothing, so a test of a
// decision that posts no Event passes machineEvents{}.
type machineEvents struct {
	recorder *events.Recorder
	machine  events.ObjectReference
}

func (e machineEvents) normal(reason, message string) {
	e.recorder.Normal(e.machine, reason, message)
}

// postStatusEvents posts the Events of one status write that landed:
// one for each condition that transitioned from stored, and one for
// each new crash or refused boot that the facts carried in.
func postStatusEvents(e machineEvents, stored, written *machine.MachineStatus) {
	for _, c := range api.Transitions(stored.Conditions, written.Conditions) {
		if c.Type == machine.RebootApprovedCondition {
			continue
		}
		e.recorder.Transition(e.machine, transitionEvent(c), badStatus(c))
	}
	if crash := written.LastCrash; crash != nil && crash.Time != nil && !sameCrash(stored.LastCrash, crash) {
		e.recorder.Warning(e.machine, reasonKernelCrashed, fmt.Sprintf(
			"the kernel recorded a %s at %s: %s; status.lastCrash names the records",
			crash.Reason, crash.Time.UTC().Format(time.RFC3339), crash.Message))
	}
	if stop := written.LastFailStop; stop != nil && (stored.LastFailStop == nil || !stored.LastFailStop.Time.Equal(stop.Time)) {
		e.recorder.Warning(e.machine, reasonBootRefused, fmt.Sprintf(
			"init refused the boot at %s and powered the machine off: %s",
			stop.Time.UTC().Format(time.RFC3339), stop.Reason))
	}
}

// sameCrash reports whether the stored status already names the crash
// at the same time.
func sameCrash(stored, crash *machine.CrashStatus) bool {
	return stored != nil && stored.Time != nil && stored.Time.Equal(*crash.Time)
}

// transitionEvent answers the condition an Event reports. Five
// convergence conditions share one set of reasons, and Converged
// alone says nothing about which document converged, so the message
// starts with the condition's type and status.
func transitionEvent(c api.Condition) api.Condition {
	prefix := c.Type + " is " + string(c.Status)
	if c.Message == "" {
		c.Message = prefix
	} else {
		c.Message = prefix + ": " + c.Message
	}
	return c
}

// badStatus answers the status of a condition that needs a person,
// for events.Recorder.Transition. A condition needs a person when the
// phase it argues for is Blocked, Degraded, or Unknown. A False that
// argues for Downloading, Updating, UpdatePending, or Booting is a
// change in progress, which needs no one. Ready's reason is the phase
// word.
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
