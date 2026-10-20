package main

// The operator posts a Kubernetes Event about a Firmware for each
// change of its Valid condition, and for each upload it starts and each
// result, which change no condition. A Firmware is cluster-scoped, so
// its Events are in the namespace default. A device that did not load
// sets no condition on its Machine, because the Machine's status
// belongs to the OS.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
)

// component names the operator in each Event's source, and in the
// managed-by label of the objects it creates.
const component = "firmware-operator"

// The reasons of the Events that change no condition. Valid's own
// reasons are in firmware.go.
const (
	reasonUploadStarted = "UploadStarted"
	reasonLoaded        = "Loaded"
	reasonUploadFailed  = "UploadFailed"
	reasonConflict      = "Conflict"
)

type eventReference = events.ObjectReference

// post posts the Events of a status write after it landed. A write
// that the API server refused posts nothing, and the next pass finds
// the same change again.
func (o *operator) post(f *firmware, validity condition, notices []notice, transitioned bool) {
	about := f.reference()
	if transitioned {
		o.recorder.Transition(about, validity, conditions.False)
	}
	for _, n := range notices {
		if n.warning {
			o.recorder.Warning(about, n.reason, n.message)
		} else {
			o.recorder.Normal(about, n.reason, n.message)
		}
	}
}
