package main

// The Events of observatory-operator's resources. `kubectl describe`
// lists them under the status, so a person reads what happened to a
// resource in the last hour with no log to open. kubernetes/events
// writes them, and root plan 78 gives the rule for a condition, an
// Event, and a log line.
//
//   - Each condition transition of a device, a Telescope, an
//     Observatory, a Guider, an OpticalTrain, and an OpticalTube posts
//     one Event, after the status writer's write lands (status.go).
//   - A Reservation's runner posts one Event for each step's end and
//     each phase change, with the step's own summary, so the
//     transitions of the Reservation's Ready and SafeToPowerOff post
//     nothing more. Each of them changes when a step starts or ends,
//     or when the phase changes, and the runner's Event says more.
//     SessionCurrent changes on a spec edit, when no step changes, so
//     each of its transitions posts one Event (sessioncurrent.go).
//   - A pod that a Ready reservation's runner creates again is one
//     Event on the device, the Guider, or the Telescope or
//     Observatory whose INDI server the pod runs, because the status
//     shows only the gap while the pod is gone (steady.go).
//   - A driver that a Ready reservation's runner stops on a running
//     server, because a person deleted its device, is one Event on its
//     Telescope or Observatory (serverdrivers.go), and the device's pod
//     that the runner deletes is one Event on the device (deletes.go).
//   - A move that a mount or a dome refuses under a lock policy is one
//     Warning on the device (locks.go).
//   - Each run of a procedure posts one Event on its resource when it
//     starts, and one when it ends: ProcedureStarted, ProcedureDone,
//     or the Warning ProcedureFailed (procedure.go). Its actions post
//     none: the run's record in status.procedures holds them.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// reference answers the object reference of a resource of the group.
func reference(kind observatory.Kind, meta observatory.ObjectMeta) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: observatory.APIVersion, Kind: kind.Name,
		Namespace: meta.Namespace, Name: meta.Name, UID: meta.UID,
	}
}

// record posts a Normal Event about a reservation.
func (o *operator) record(r *observatory.Reservation, reason, message string) {
	o.recorder.Normal(reference(observatory.ReservationKind, r.Metadata), reason, message)
}

// badStatus answers the status of a condition that needs a person,
// for events.Recorder.Transition. A missing parent needs one. Ready is
// False while a resource starts or is not reserved, which is expected,
// so only its failing reasons need one. Weather that is not Safe needs
// one too, and so does a missing Horizon, because a scheduler would
// plan the telescope's night with no horizon. The other state
// conditions of a device, such as Parked or Lit, are False in normal
// use, so none of them is a Warning.
func badStatus(c observatory.Condition) observatory.ConditionStatus {
	switch {
	case c.Type == observatory.ConditionParentFound:
		return conditions.False
	case c.Type == observatory.ConditionReady && failing(c.Reason):
		return conditions.False
	case c.Type == observatory.ConditionSafe, c.Type == observatory.ConditionHorizonFound:
		return conditions.False
	}
	return ""
}

// failing reports whether a Ready reason says that something failed:
// a phase of Error, or a failed step.
func failing(reason string) bool {
	switch reason {
	// A device's phase and a Telescope's phase both name it Error.
	case string(observatory.PhaseError), reasonFailed, reasonTimedOut:
		return true
	}
	return false
}
