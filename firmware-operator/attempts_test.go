package main

// Each appearance of a device gets one upload attempt for each
// generation of the rule. Repeated writes to a device can disturb it, so
// the operator does not retry a failed upload until the device appears
// again or the rule changes. A restarted operator reads the status
// entries and the upload pods so that it does not start an upload twice.

import (
	"testing"
	"testing/synctest"
	"time"
)

// failedUpload runs an upload whose loader fails, and leaves the bare
// chip in the ResourceSlice.
func failedUpload(t *testing.T) *world {
	w := startedUpload(t)
	w.api.finish(t, w.onlyUpload(t).Metadata.Name, podFailed, 1, "fxload: Protocol error")
	w.settle()
	return w
}

// A failed upload does not run again while the same appearance of the
// device stays in the ResourceSlice.
func TestAFailedUploadDoesNotRunAgainForTheSameAppearance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := failedUpload(t)
		// A ResourceSlice update that does not change the device still
		// starts a pass.
		w.api.publish("liken-1", bareChip("1-4.1.2", 12), camera("1-3", 4))
		time.Sleep(time.Hour)
		synctest.Wait()

		if got := w.api.created(operatorPodsPath); got != 1 {
			t.Errorf("the operator created %d upload pods, want 1", got)
		}
		if e := w.entry(t, "qhy5l-ii"); e.Result != resultFailed {
			t.Errorf("the entry is %+v, want it to stay Failed", e)
		}
	})
}

// A replug of the device or a new generation of the rule starts a new
// attempt, which replaces the port's entry.
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

				if got := w.api.created(operatorPodsPath); got != 2 {
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

// An operator that restarts during an upload creates no second pod, and
// writes the first pod's result.
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

		if got := w.api.created(operatorPodsPath); got != 1 {
			t.Errorf("the operators created %d upload pods, want 1", got)
		}
		if e := w.entry(t, "qhy5l-ii"); e.Result != resultLoaded {
			t.Errorf("the entry is %+v, want Loaded", e)
		}
	})
}

// When an operator stops after it creates the pod and before it writes
// the entry, the next operator writes that pod's result and creates no
// second pod.
func TestAPodWithNoEntryGetsItsResultWritten(t *testing.T) {
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

		if got := w.api.created(operatorPodsPath); got != 0 {
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

// putUpload stores an upload pod that an earlier operator process
// created, without its claim.
func (a *fakeAPI) putUpload(upload pod) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object := toMap(upload)
	metadata := object["metadata"].(map[string]any)
	metadata["uid"] = "uid-earlier"
	metadata["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	object["status"] = map[string]any{"phase": podPending}
	a.store(operatorPodsPath, object, "ADDED")
}

// The next pass creates the missing claim of a Pending upload pod, owned
// by the pod.
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
