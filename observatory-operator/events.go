package main

// Where observatory-operator posts Events. `kubectl describe` lists
// them, so a person can see what happened to a resource in the last
// hour without reading the log. kubernetes/events writes them. The
// operator posts an Event for each condition transition and for each
// action that changes no condition, and none for a reading or a retry
// of a write.
//
//   - Each condition transition of a device, Telescope, Observatory,
//     Guider, OpticalTrain, or OpticalTube posts one Event after the
//     status writer writes the status (status.go). This includes a SetUp
//     that expires (setup.go) and an AttentionNeeded that a notify
//     action sets (attention.go). A person's own write of SetUp posts
//     nothing.
//   - A Reservation's runner posts one Event with the step's summary
//     when a step ends and when the phase changes, and a Warning when a
//     step fails. Ready and SafeToPowerOff change only at those times,
//     so their transitions post no Event of their own. EditsPending
//     changes when a person edits the spec, so save posts an Event for
//     each of its transitions (reservation.go).
//   - When the runner of a Ready Reservation creates a pod again, it
//     posts one Event on the device, Guider, Telescope, or Observatory
//     that the pod serves (steady.go). The status shows a missing pod
//     only until the pod is back, so the Event is what tells a person
//     that the pod is new and started without the holder's settings.
//   - When a person deletes a device, the runner of a Ready Reservation
//     stops the device's driver on the running INDI server and posts
//     one Event on its Telescope or Observatory (serverdrivers.go). It
//     also deletes the device's pod and posts one Event on the device
//     (deletes.go).
//   - A move that a lock policy refuses posts one Warning on the mount
//     or dome (locks.go).
//   - Each procedure run posts ProcedureStarted when it starts, and
//     ProcedureDone or the Warning ProcedureFailed when it ends
//     (procedure.go). A trigger that cannot run posts ProcedureFailed
//     once for each new reason (triggers.go). The actions of a run post
//     no Events, because status.procedures records them.
//   - The retry annotation posts a Retry Event on its resource
//     (retry.go), and a Reservation that retries its failed step posts
//     another (finish.go).

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

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

// badStatus returns the status that makes a transition of c a Warning,
// for events.Recorder.Transition, or "" when no status of c is a
// Warning. The Warnings are ParentFound False, Ready False with a
// failing reason, Safe False, HorizonFound False, and AttentionNeeded
// True, which a notify action sets to call a person. Ready is also
// False while a resource starts or has no reservation, which is normal.
// The other state conditions, such as Parked or Lit, are False in
// normal use, so they post no Warning.
func badStatus(c observatory.Condition) observatory.ConditionStatus {
	switch {
	case c.Type == observatory.ConditionParentFound:
		return conditions.False
	case c.Type == observatory.ConditionReady && failing(c.Reason):
		return conditions.False
	case c.Type == observatory.ConditionSafe, c.Type == observatory.ConditionHorizonFound:
		return conditions.False
	case c.Type == observatory.ConditionAttentionNeeded:
		return conditions.True
	}
	return ""
}

// failing reports whether a Ready reason means that something failed:
// the Error phase, or a step that failed or timed out.
func failing(reason string) bool {
	switch reason {
	// The Error phase of a device and of a Telescope have the same
	// string, so PhaseError matches both.
	case string(observatory.PhaseError), reasonFailed, reasonTimedOut:
		return true
	}
	return false
}
