package main

// A rehearsal or apply pod ends with CatalogMoved when LVFS published a
// new catalog file after the machine's scan, so the jcat lists a file
// other than the one the pod was given. The operator then reads the
// jcat at once instead of at the next interval, and the machine is
// scanned, rehearsed, and applied again with no action by a person.

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// movedFile is the file that LVFS publishes after node-2's scan.
const movedFile = "firmware-09139-stable.xml.zst"

// moveCatalog makes the fake LVFS publish movedFile with one more BIOS
// release than the file of node-2's scan, so the operator scans node-2
// again.
func moveCatalog(w *world) {
	w.lvfs.publish(movedFile, frameworkBIOS("0.0.3.5", "0.0.4.2", "0.0.4.3"), dbx("1", "2"))
}

func envOf(p pod) map[string]string {
	env := map[string]string{}
	for _, e := range p.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	return env
}

// rehearsalMovedWorld returns approvedWorld after LVFS publishes
// movedFile and node-2's rehearsal pod ends with CatalogMoved.
func rehearsalMovedWorld(t *testing.T) *world {
	w := approvedWorld(t)
	moveCatalog(w)
	w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded,
		rehearsalLog(rehearsalResult{Result: resultCatalogMoved, Message: "the jcat now lists " + movedFile}))
	w.settle()
	return w
}

// Rehearsed stays Unknown until the rehearsal against the new file.
func TestARehearsalThatFindsTheCatalogMovedReadsTheJcatAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsalMovedWorld(t)

		if got := w.api.catalog(t, "lvfs").Status.File; got != movedFile {
			t.Errorf("the FirmwareCatalog's status.file is %s, want %s at once", got, movedFile)
		}
		if got := envOf(w.onlyScan(t))[catalogFileVariable]; got != movedFile {
			t.Errorf("node-2's scan reads %s, want %s", got, movedFile)
		}
		r, _ := rehearsalRecord(w.api.firmware(t, biosObjectName).Status.Rehearsals, "node-2")
		if r.Result != resultCatalogMoved || r.Message != "the jcat now lists "+movedFile+", so the operator reads the jcat again and rehearses the machine again against the new file" {
			t.Errorf("the rehearsal record is %+v, want CatalogMoved with the new file", r)
		}
		if c, _ := rehearsedOf(t, w); c.Status != conditions.Unknown || c.Reason != reasonRehearsing || !strings.Contains(c.Message, movedFile) {
			t.Errorf("Rehearsed is %+v, want Unknown with the reason Rehearsing and the new file", c)
		}
		got := w.postedAbout(biosObjectName)
		if !containsPosted(got, posted{"Normal", reasonRehearsalCatalogMoved}) || containsPosted(got, posted{"Warning", reasonRehearsalFailed}) {
			t.Errorf("the operator posted %v, want a Normal RehearsalCatalogMoved and no RehearsalFailed", got)
		}
	})
}

// A rehearsal attempt records the catalog file of the machine's scan,
// so a scan against a new file starts a new rehearsal.
func TestTheScanOfTheMovedCatalogRehearsesAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsalMovedWorld(t)
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
			resultLog(scanned(movedFile, systemFirmware("0.0.3.5", "0.0.4.2", "0.0.4.3"), secureBootDBX("2"))))
		w.settle()

		if got := envOf(w.onlyRehearsal(t))[catalogFileVariable]; got != movedFile {
			t.Errorf("the new rehearsal reads %s, want %s", got, movedFile)
		}
		if r, _ := rehearsalRecord(w.api.firmware(t, biosObjectName).Status.Rehearsals, "node-2"); r.Result != resultRunning || r.File != movedFile {
			t.Errorf("the rehearsal record is %+v, want Running against %s", r, movedFile)
		}
	})
}

// When the new file offers the machine the same releases, the operator
// moves the machine's records and Firmware entries to the new file
// without a scan.
func TestAMovedCatalogWithTheSameReleasesRehearsesWithNoRescan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := approvedWorld(t)
		w.lvfs.publish(movedFile, frameworkBIOS("0.0.3.5", "0.0.4.2"), dbx("1", "2"))
		w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded,
			rehearsalLog(rehearsalResult{Result: resultCatalogMoved, Message: "the jcat now lists " + movedFile}))
		w.settle()

		if got := len(w.api.scans(t)); got != 0 {
			t.Errorf("%d scan pods, want none for a file with the same releases", got)
		}
		for _, e := range w.api.firmware(t, biosObjectName).Status.Machines {
			if e.File != movedFile {
				t.Errorf("the entry of %s has file %s, want %s", e.Device, e.File, movedFile)
			}
		}
		if got := envOf(w.onlyRehearsal(t))[catalogFileVariable]; got != movedFile {
			t.Errorf("the new rehearsal reads %s, want %s", got, movedFile)
		}
	})
}

// An apply pod that ends with CatalogMoved wrote nothing and created no
// Disruption, so its record returns to Waiting instead of Failed.
func TestAnApplyThatFindsTheCatalogMovedStartsAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := preparingWorld(t)
		moveCatalog(w)
		w.api.finishPod(t, w.onlyApply(t).Metadata.Name, applyContainer, podSucceeded,
			applyLog(applyResult{Result: resultCatalogMoved, Message: "the jcat now lists " + movedFile + ", so nothing was written"}))
		w.settle()

		if got := w.api.catalog(t, "lvfs").Status.File; got != movedFile {
			t.Errorf("the FirmwareCatalog's status.file is %s, want %s at once", got, movedFile)
		}
		if got := envOf(w.onlyApply(t))[catalogFileVariable]; got != movedFile {
			t.Errorf("the new apply pod reads %s, want %s", got, movedFile)
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWaiting || r.Disruption != "" {
			t.Errorf("the record is %+v, want Waiting again for the new pod", r)
		}
		if got := w.api.created(disruptionsPath); got != 0 {
			t.Errorf("the operator created %d Disruptions, want none", got)
		}
		got := w.postedAbout(biosObjectName)
		if !containsPosted(got, posted{"Normal", reasonApplyCatalogMoved}) || containsPosted(got, posted{"Warning", reasonApplyFailed}) {
			t.Errorf("the operator posted %v, want a Normal ApplyCatalogMoved and no ApplyFailed", got)
		}
	})
}
