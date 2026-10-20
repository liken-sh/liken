package main

// These tests play the kubelet and machine-operator after an upload
// starts, and read the result the operator writes. The operator writes
// the result first and then deletes the pod, and the claim goes with
// the pod.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// An upload loads when the loader exits 0 and the bare device leaves
// the slice, and the device that comes back in its place matches no
// rule.
func TestTheDeviceLeavingAfterSuccessIsLoaded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startedUpload(t)
		w.api.finish(t, w.onlyUpload(t).Metadata.Name, podSucceeded, 0, "")
		time.Sleep(14 * time.Second)
		w.api.publish("liken-1", camera("1-4.1.2", 13))
		w.settle()

		e := w.entry(t, "qhy5l-ii")
		if e.Result != resultLoaded || e.DeviceNumber != 12 {
			t.Errorf("the entry is %+v, want Loaded for device number 12", e)
		}
		if got := uploadCountsOf(t, w.api.firmware(t, "qhy5l-ii")); got != (UploadCounts{Loaded: 1}) {
			t.Errorf("status.counts = %+v, want one loaded", got)
		}
		if got, claims := len(w.api.uploads(t)), len(w.api.claims(t)); got+claims != 0 {
			t.Errorf("%d upload pods and %d claims remain, want none", got, claims)
		}
		want := []posted{{"Normal", reasonCompiled}, {"Normal", reasonUploadStarted}, {"Normal", reasonLoaded}}
		if got := w.postedAbout("qhy5l-ii"); !slices.Equal(got, want) {
			t.Errorf("the Events are %v, want %v", got, want)
		}
	})
}

// A loader that exits 0 while the bare device stays has not loaded the
// chip. The operator waits one minute after the loader ends, then
// writes Failed.
func TestExitZeroWithTheDeviceStayingFailsAfterAMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startedUpload(t)
		name := w.onlyUpload(t).Metadata.Name
		w.api.finish(t, name, podSucceeded, 0, "")
		time.Sleep(59 * time.Second)
		synctest.Wait()

		if e := w.entry(t, "qhy5l-ii"); e.Result != resultRunning {
			t.Errorf("before the minute the entry is %+v, want Running", e)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()

		e := w.entry(t, "qhy5l-ii")
		if e.Result != resultFailed || !strings.Contains(e.Message, "stayed in the slice") {
			t.Errorf("after the minute the entry is %+v, want Failed because the device stayed", e)
		}
		if got := len(w.api.uploads(t)); got != 0 {
			t.Errorf("%d upload pods remain, want none", got)
		}
		want := []posted{{"Normal", reasonCompiled}, {"Normal", reasonUploadStarted}, {"Warning", reasonUploadFailed}}
		if got := w.postedAbout("qhy5l-ii"); !slices.Equal(got, want) {
			t.Errorf("the Events are %v, want %v", got, want)
		}
	})
}

// A loader that fails gives its exit code and the last line of its
// log in the entry's message.
func TestAFailedLoaderGivesItsExitCodeAndLastLine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startedUpload(t)
		log := "fxload: opening /dev/bus/usb/001/012\nfxload: can't modify CPUCS: Protocol error\n"
		w.api.finish(t, w.onlyUpload(t).Metadata.Name, podFailed, 1, log)
		w.settle()

		e := w.entry(t, "qhy5l-ii")
		want := "the loader exited with code 1: fxload: can't modify CPUCS: Protocol error"
		if e.Result != resultFailed || e.Message != want {
			t.Errorf("the entry is %+v, want Failed with %q", e, want)
		}
		if got := len(w.api.uploads(t)); got != 0 {
			t.Errorf("%d upload pods remain, want none", got)
		}
	})
}

// A pod that never finishes, such as one whose claim never allocates,
// fails five minutes after its creation.
func TestAnUploadThatDoesNotFinishFailsAfterFiveMinutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startedUpload(t)
		time.Sleep(5 * time.Minute)
		synctest.Wait()

		e := w.entry(t, "qhy5l-ii")
		if e.Result != resultFailed || !strings.Contains(e.Message, "did not finish within 5m0s") {
			t.Errorf("the entry is %+v, want Failed because the pod did not finish", e)
		}
		if got := len(w.api.uploads(t)); got != 0 {
			t.Errorf("%d upload pods remain, want none", got)
		}
	})
}

// A Running entry whose pod a person deleted fails, and the operator
// does not run the upload again for that appearance.
func TestARunningUploadWhosePodIsGoneFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startedUpload(t)
		w.api.deleteObject(uploadPodsPath, w.onlyUpload(t).Metadata.Name)
		w.settle()

		e := w.entry(t, "qhy5l-ii")
		if e.Result != resultFailed || !strings.Contains(e.Message, "the upload pod is gone") {
			t.Errorf("the entry is %+v, want Failed because the pod is gone", e)
		}
		if got := w.api.created(uploadPodsPath); got != 1 {
			t.Errorf("the operator created %d upload pods, want 1", got)
		}
	})
}

// deleteObject deletes an object the way another writer does.
func (a *fakeAPI) deleteObject(collection, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.store(collection, a.objects[collection][name], "DELETED")
}

// The pod's own failure, such as the deadline of
// activeDeadlineSeconds, gives the pod's reason when the loader never
// ended.
func TestAFailedPodWithNoLoaderGivesThePodsReason(t *testing.T) {
	cases := []struct {
		name   string
		status podStatus
		want   string
	}{
		{"a deadline", podStatus{Phase: podFailed, Reason: "DeadlineExceeded", Message: "Pod was active on the node longer than the specified deadline"},
			"the upload pod failed: DeadlineExceeded: Pod was active on the node longer than the specified deadline"},
		{"no reason", podStatus{Phase: podFailed}, "the upload pod failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := follow(pod{Status: c.status}, true, time.Now())
			if got.result != resultFailed || got.message != c.want {
				t.Errorf("follow = %+v, want Failed with %q", got, c.want)
			}
		})
	}
}

// A loader's exit reason other than the plain Error goes in the
// message, and a long last line is cut.
func TestALoadersFailureNamesItsReason(t *testing.T) {
	long := strings.Repeat("x", 300)
	cases := []struct {
		name   string
		loader terminated
		want   string
	}{
		{"killed for memory", terminated{ExitCode: 137, Reason: "OOMKilled"}, "the loader exited with code 137 (OOMKilled)"},
		{"a long line", terminated{ExitCode: 2, Reason: "Error", Message: long}, "the loader exited with code 2: " + long[:maxLineSize] + "..."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := failure(pod{}, &c.loader); got != c.want {
				t.Errorf("failure = %q, want %q", got, c.want)
			}
		})
	}
}

// earliest answers the earlier time, and the zero time is no time.
func TestEarliest(t *testing.T) {
	early, late := time.Unix(100, 0), time.Unix(200, 0)
	cases := []struct {
		name       string
		a, b, want time.Time
	}{
		{"both", late, early, early},
		{"the first only", early, time.Time{}, early},
		{"the second only", time.Time{}, late, late},
		{"neither", time.Time{}, time.Time{}, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := earliest(c.a, c.b); !got.Equal(c.want) {
				t.Errorf("earliest = %v, want %v", got, c.want)
			}
		})
	}
}

// A status write that the API server refuses keeps the pod, posts no
// Event, and the pass after the retry delay writes the result and
// then deletes the pod.
func TestARefusedStatusWriteKeepsThePodUntilTheRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startedUpload(t)
		w.api.refuseStatusWrites(1)
		w.api.finish(t, w.onlyUpload(t).Metadata.Name, podFailed, 1, "fxload: Protocol error")
		w.settle()

		if e := w.entry(t, "qhy5l-ii"); e.Result != resultRunning {
			t.Errorf("after the refusal the entry is %+v, want Running", e)
		}
		if got := len(w.api.uploads(t)); got != 1 {
			t.Errorf("after the refusal %d upload pods remain, want 1", got)
		}
		time.Sleep(retryDelay)
		synctest.Wait()

		if e := w.entry(t, "qhy5l-ii"); e.Result != resultFailed {
			t.Errorf("after the retry the entry is %+v, want Failed", e)
		}
		if got := len(w.api.uploads(t)); got != 0 {
			t.Errorf("after the retry %d upload pods remain, want none", got)
		}
		want := []posted{{"Normal", reasonCompiled}, {"Normal", reasonUploadStarted}, {"Warning", reasonUploadFailed}}
		if got := w.postedAbout("qhy5l-ii"); !slices.Equal(got, want) {
			t.Errorf("the Events are %v, want %v", got, want)
		}
	})
}
