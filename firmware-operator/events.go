package main

// The operator posts a Kubernetes Event about a Firmware for each
// change of its Valid condition, and for each upload it starts and each
// result, which change no condition. It posts one about a discovered
// Firmware when it creates it and when a machine's newest offered
// release changes, and one about a FirmwareCatalog for each change of its Ready
// condition and for each scan it starts and each result. A Firmware and
// a FirmwareCatalog are cluster-scoped, so their Events are in the namespace
// default. A device that did not load
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
// reasons are in firmware.go, and Ready's in catalog.go.
const (
	reasonUploadStarted   = "UploadStarted"
	reasonLoaded          = "Loaded"
	reasonUploadFailed    = "UploadFailed"
	reasonConflict        = "Conflict"
	reasonDiscovered      = "Discovered"
	reasonUpdateAvailable = "UpdateAvailable"
	reasonScanStarted     = "ScanStarted"
	reasonScanned         = "Scanned"
	reasonScanFailed      = "ScanFailed"
	reasonCatalogMoved    = "CatalogMoved"
)

type eventReference = events.ObjectReference

// post posts the Events of a Firmware's status write after it landed.
// A write that the API server refused posts nothing, and the next pass
// finds the same change again.
func (o *operator) post(f *firmware, validity condition, notices []notice, transitioned bool) {
	o.postAbout(f.reference(), validity, notices, transitioned)
}

// postCatalog posts the Events of a FirmwareCatalog's status write after it
// landed.
func (o *operator) postCatalog(c *catalog, readiness condition, notices []notice, transitioned bool) {
	o.postAbout(c.reference(), readiness, notices, transitioned)
}

// postAbout posts the transition of an object's condition, when it
// transitioned, and then each notice.
func (o *operator) postAbout(about eventReference, c condition, notices []notice, transitioned bool) {
	if transitioned {
		o.recorder.Transition(about, c, conditions.False)
	}
	for _, n := range notices {
		if n.warning {
			o.recorder.Warning(about, n.reason, n.message)
		} else {
			o.recorder.Normal(about, n.reason, n.message)
		}
	}
}
