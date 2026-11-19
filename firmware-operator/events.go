package main

// The operator posts an Event for each transition of a condition on a
// Firmware or a FirmwareCatalog, and for each action that changes no
// condition, such as an upload, a scan, or the creation of a discovered
// Firmware. A failed upload sets no condition on the Machine, because
// the OS writes the Machine's status and the operator does not.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
)

// component is the operator's name in each Event's source and in the
// managed-by label of the objects it creates.
const component = "firmware-operator"

// The reasons of Events for uploads, scans, and discovered Firmware
// objects, which change no condition. The reasons of conditions, and
// of rehearsal and apply Events, are next to the code that sets them.
const (
	reasonUploadStarted    = "UploadStarted"
	reasonLoaded           = "Loaded"
	reasonUploadFailed     = "UploadFailed"
	reasonConflict         = "Conflict"
	reasonDiscovered       = "Discovered"
	reasonDeleted          = "Deleted"
	reasonUpdateAvailable  = "UpdateAvailable"
	reasonScanStarted      = "ScanStarted"
	reasonScanPodRecreated = "ScanPodRecreated"
	reasonScanned          = "Scanned"
	reasonScanFailed       = "ScanFailed"
	reasonCatalogMoved     = "CatalogMoved"
	reasonManyComponents   = "ManyComponents"
)

type eventReference = events.ObjectReference

// post records the condition's transition, if there was one, and then
// each notice, as Events. After a
// status write, the caller calls it only when the write succeeded, so
// a refused write posts nothing and the next pass computes the same
// change again.
func (o *operator) post(f *firmware, validity condition, notices []notice, transitioned bool) {
	o.postAbout(f.reference(), validity, notices, transitioned)
}

// postCatalog is post for a FirmwareCatalog.
func (o *operator) postCatalog(c *catalog, readiness condition, notices []notice, transitioned bool) {
	o.postAbout(c.reference(), readiness, notices, transitioned)
}

func (o *operator) postAbout(about eventReference, c condition, notices []notice, transitioned bool) {
	if transitioned {
		// Every condition here needs a person when it is False, so a
		// transition to False posts a Warning.
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
