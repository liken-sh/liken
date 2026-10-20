package main

// Each appearance of a device gets one attempt for each generation of
// the rule. A loop that writes to a device again and again can disturb
// it, so nothing retries a failed upload until the device appears again
// or the rule changes. The status entry and the pod's annotations are
// the whole record, so a restart of the operator starts nothing twice.

import (
	"testing"
	"testing/synctest"
	"time"
)

// failedUpload runs an upload whose loader fails, with the bare chip
// still in the slice.
func failedUpload(t *testing.T) *world {
	w := startedUpload(t)
	w.api.finish(t, w.onlyUpload(t).Metadata.Name, podFailed, 1, "fxload: Protocol error")
	w.settle()
	return w
}

// A failed upload does not run again while the same appearance stays,
// even long after.
func TestAFailedUploadDoesNotRunAgainForTheSameAppearance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := failedUpload(t)
		// A slice change that leaves the device as it was wakes a pass.
		w.api.publish("liken-1", bareChip("1-4.1.2", 12), camera("1-3", 4))
		time.Sleep(time.Hour)
		synctest.Wait()

		if got := w.api.created(uploadPodsPath); got != 1 {
			t.Errorf("the operator created %d upload pods, want 1", got)
		}
		if e := w.entry(t, "qhy5l-ii"); e.Result != resultFailed {
			t.Errorf("the entry is %+v, want it to stay Failed", e)
		}
	})
}

// A new appearance of the device, after a replug or a power cycle,
// and a new generation of the rule each start a new attempt, which
// replaces the port's entry.
func TestANewAppearanceOrGenerationRunsAgain(t *testing.T) {
	cases := []struct {
		name           string
		change         func(w *world)
		wantNumber     int64
		wantGeneration int64
	}{
		{"a replug", func(w *world) {
			w.api.publish("liken-1")
			w.api.publish("liken-1", bareChip("1-4.1.2", 13))
		}, 13, 1},
		{"a new generation", func(w *world) {
			edited := qhy5lii()
			edited.Spec.Upload.Args = []string{"-v"}
			w.api.putFirmware(edited)
		}, 12, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := failedUpload(t)
				c.change(w)
				w.settle()

				if got := w.api.created(uploadPodsPath); got != 2 {
					t.Errorf("the operator created %d upload pods, want 2", got)
				}
				e := w.entry(t, "qhy5l-ii")
				if e.Result != resultRunning || e.DeviceNumber != c.wantNumber || e.Generation != c.wantGeneration {
					t.Errorf("the entry is %+v, want Running for device number %d at generation %d", e, c.wantNumber, c.wantGeneration)
				}
			})
		})
	}
}

// An operator that restarts during an upload creates no second pod,
// and the new copy follows the first pod to its result.
func TestARestartDuringAnUploadCreatesNoSecondPod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &world{api: startFakeAPI(t)}
		stopFirst := w.run(t)
		w.api.putFirmware(qhy5lii())
		w.api.publish("liken-1", bareChip("1-4.1.2", 12))
		w.settle()
		name := w.onlyUpload(t).Metadata.Name
		stopFirst()
		w.run(t)
		w.api.finish(t, name, podSucceeded, 0, "")
		w.api.publish("liken-1", camera("1-4.1.2", 13))
		w.settle()

		if got := w.api.created(uploadPodsPath); got != 1 {
			t.Errorf("the operators created %d upload pods, want 1", got)
		}
		if e := w.entry(t, "qhy5l-ii"); e.Result != resultLoaded {
			t.Errorf("the entry is %+v, want Loaded", e)
		}
	})
}

// A copy that stopped after it created the pod and before it wrote
// the entry leaves a pod with no entry. The next copy follows that pod
// and writes its result, with no second pod.
func TestAPodWithNoEntryIsFollowed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		api.putFirmware(qhy5lii())
		chip := bareChip("1-4.1.2", 12)
		api.publish("liken-1", chip)
		rule := api.firmware(t, "qhy5l-ii")
		api.putUpload(buildUploadPod(&rule, devicesOf(sliceOf("liken-1", chip))[0], operatorNS))
		w := &world{api: api}
		w.run(t)
		w.api.finish(t, w.onlyUpload(t).Metadata.Name, podFailed, 1, "fxload: Protocol error")
		w.settle()

		if got := w.api.created(uploadPodsPath); got != 0 {
			t.Errorf("the operator created %d upload pods, want none", got)
		}
		if e := w.entry(t, "qhy5l-ii"); e.Result != resultFailed {
			t.Errorf("the entry is %+v, want Failed", e)
		}
		if got := len(w.api.claims(t)); got != 0 {
			t.Errorf("%d claims remain, want none", got)
		}
	})
}

// putUpload stores an upload pod that an earlier copy of the operator
// created, and no claim for it.
func (a *fakeAPI) putUpload(upload pod) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object := toMap(upload)
	metadata := object["metadata"].(map[string]any)
	metadata["uid"] = "uid-earlier"
	metadata["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	object["status"] = map[string]any{"phase": podPending}
	a.store(uploadPodsPath, object, "ADDED")
}

// A pending pod whose claim is missing gets its claim from the next
// pass, with the pod as its owner.
func TestAPendingPodGetsItsMissingClaim(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		api.putFirmware(qhy5lii())
		chip := bareChip("1-4.1.2", 12)
		api.publish("liken-1", chip)
		rule := api.firmware(t, "qhy5l-ii")
		api.putUpload(buildUploadPod(&rule, devicesOf(sliceOf("liken-1", chip))[0], operatorNS))
		w := &world{api: api}
		w.run(t)

		claims := w.api.claims(t)
		if len(claims) != 1 || claims[0].Metadata.OwnerReferences[0].UID != "uid-earlier" {
			t.Errorf("the claims are %+v, want one owned by the earlier pod", claims)
		}
	})
}
