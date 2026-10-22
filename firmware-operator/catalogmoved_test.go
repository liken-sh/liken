package main

// These tests follow a rehearsal or an apply pod that finds its
// catalog moved: LVFS published a new file after the machine's scan,
// so the pod's jcat names another file than the one it was given. The
// operator reads the jcat again at once instead of at the next
// interval, and the machine scans, rehearses, and applies again on its
// own, with no person's approval.

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// movedFile is the file that LVFS publishes after node-2's scan.
const movedFile = "firmware-09139-stable.xml.zst"

// moveCatalog plays LVFS publishing movedFile. It offers the BIOS one
// more release than the file the scan read, so node-2's releases
// change, and the operator scans node-2 again after it reads the jcat.
func moveCatalog(w *world) {
	w.lvfs.publish(movedFile, frameworkBIOS("0.0.3.5", "0.0.4.2", "0.0.4.3"), dbx("1", "2"))
}

// envOf answers a pod's environment.
func envOf(p pod) map[string]string {
	env := map[string]string{}
	for _, e := range p.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	return env
}

// rehearsalMovedWorld is approvedWorld after LVFS published movedFile
// and node-2's rehearsal found its jcat naming it.
func rehearsalMovedWorld(t *testing.T) *world {
	w := approvedWorld(t)
	moveCatalog(w)
	w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded,
		rehearsalLog(rehearsalResult{Result: resultCatalogMoved, Message: "the jcat now names " + movedFile}))
	w.settle()
	return w
}

// A rehearsal that found the catalog moved is evidence that the
// FirmwareCatalog's file is old, so the operator reads the jcat at once,
// with no wait for the interval, and scans the machine against the new
// file. The approval still waits for a rehearsal, and nothing needs a
// person.
func TestARehearsalThatFindsTheCatalogMovedReadsTheJcatAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsalMovedWorld(t)

		if got := w.api.catalog(t, "lvfs").Status.File; got != movedFile {
			t.Errorf("the FirmwareCatalog names %s, want %s at once", got, movedFile)
		}
		if got := envOf(w.onlyScan(t))[catalogFileVariable]; got != movedFile {
			t.Errorf("node-2's scan reads %s, want %s", got, movedFile)
		}
		r, _ := rehearsalRecord(w.api.firmware(t, biosObjectName).Status.Rehearsals, "node-2")
		if r.Result != resultCatalogMoved || r.Message != "the jcat now names "+movedFile+"; the operator reads the jcat again, and the machine rehearses again against the new file" {
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

// The rehearsal's attempt names the catalog file of the machine's scan,
// so the scan of the new file starts a new rehearsal on its own.
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

// A new file that offers the machine the same releases moves the
// machine's scan record to it with no scan. The machine's entries in
// the Firmware objects move with the record, so they name the file the
// operator concluded the machine runs against, and the rehearsal starts
// against the new file with no rescan.
func TestAMovedCatalogWithTheSameReleasesRehearsesWithNoRescan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := approvedWorld(t)
		w.lvfs.publish(movedFile, frameworkBIOS("0.0.3.5", "0.0.4.2"), dbx("1", "2"))
		w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded,
			rehearsalLog(rehearsalResult{Result: resultCatalogMoved, Message: "the jcat now names " + movedFile}))
		w.settle()

		if got := len(w.api.scans(t)); got != 0 {
			t.Errorf("%d scan pods, want none for a file with the same releases", got)
		}
		for _, e := range w.api.firmware(t, biosObjectName).Status.Machines {
			if e.File != movedFile {
				t.Errorf("the entry of %s names %s, want %s", e.Device, e.File, movedFile)
			}
		}
		if got := envOf(w.onlyRehearsal(t))[catalogFileVariable]; got != movedFile {
			t.Errorf("the new rehearsal reads %s, want %s", got, movedFile)
		}
	})
}

// An apply pod that found the catalog moved wrote nothing and asked for
// no turn. Its record leaves, so it is not Failed, and the apply starts
// again on its own against the file the operator read at once.
func TestAnApplyThatFindsTheCatalogMovedStartsAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := preparingWorld(t)
		moveCatalog(w)
		w.api.finishPod(t, w.onlyApply(t).Metadata.Name, applyContainer, podSucceeded,
			applyLog(applyResult{Result: resultCatalogMoved, Message: "the jcat now names " + movedFile + ", so nothing was written"}))
		w.settle()

		if got := w.api.catalog(t, "lvfs").Status.File; got != movedFile {
			t.Errorf("the FirmwareCatalog names %s, want %s at once", got, movedFile)
		}
		if got := envOf(w.onlyApply(t))[catalogFileVariable]; got != movedFile {
			t.Errorf("the new apply pod reads %s, want %s", got, movedFile)
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWaiting || r.Disruption != "" {
			t.Errorf("the record is %+v, want Waiting again for the new pod", r)
		}
		if got := w.api.created(disruptionsPath); got != 0 {
			t.Errorf("the operator filed %d requests, want none", got)
		}
		got := w.postedAbout(biosObjectName)
		if !containsPosted(got, posted{"Normal", reasonApplyCatalogMoved}) || containsPosted(got, posted{"Warning", reasonApplyFailed}) {
			t.Errorf("the operator posted %v, want a Normal ApplyCatalogMoved and no ApplyFailed", got)
		}
	})
}
