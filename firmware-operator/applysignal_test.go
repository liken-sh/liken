package main

// These tests follow the apply pod from its start to the signal to
// write: the pod that prepares while the cluster is whole, the request
// for the turn that waits until the pod is Ready, the signal that the
// operator sends once machine-operator holds the turn, a pod that ends
// or is evicted before the signal, and the deadline that counts from
// the signal.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// preparedLine is the progress line that `scan apply` prints when its
// prepare passed, before it waits for the signal.
const preparedLine = `{"phase":"prepared","message":"the rehearsal passed with the recorded append to dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f; the apply waits for the operator's signal to write","variable":"dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f","sha256":"cf901743"}`

// volumeNamed answers a pod's volume and the container's mount of it.
func volumeNamed(t *testing.T, spec podSpec, name string) (volume, volumeMount) {
	t.Helper()
	v := slices.IndexFunc(spec.Volumes, func(v volume) bool { return v.Name == name })
	m := slices.IndexFunc(spec.Containers[0].VolumeMounts, func(m volumeMount) bool { return m.Name == name })
	if v < 0 || m < 0 {
		t.Fatalf("the pod has no volume %s mounted: %+v", name, spec)
	}
	return spec.Volumes[v], spec.Containers[0].VolumeMounts[m]
}

// runPod plays the kubelet, which starts the apply pod's container and
// sets the pod's Ready condition from its readiness probe.
func (a *fakeAPI) runPod(t *testing.T, name string, ready bool) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[uploadPodsPath][name])
	if next == nil {
		t.Fatalf("there is no pod %s", name)
	}
	readiness := map[bool]string{true: "True", false: "False"}[ready]
	next["status"] = map[string]any{"phase": "Running", "conditions": []any{
		map[string]any{"type": "PodScheduled", "status": "True"},
		map[string]any{"type": "Ready", "status": readiness},
	}}
	a.store(uploadPodsPath, next, "MODIFIED")
}

// readyPod plays the kubelet once the apply pod's readiness probe
// passed.
func (a *fakeAPI) readyPod(t *testing.T, name string) {
	t.Helper()
	a.runPod(t, name, true)
}

// The apply pod starts when the rehearsal passes, while the cluster is
// whole, so the drain can evict DNS without stopping the apply. Its
// readiness probe turns it Ready when the prepare is done. It stays on
// the machine through the drain, which skips a pod that tolerates the
// cordon by its key, and it reads the signal from its own annotations
// with no credentials.
func TestTheApplyPodStartsWhenTheRehearsalPasses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := preparingWorld(t)

		p := w.onlyApply(t)
		spec := p.Spec
		if spec.NodeName != "node-2" || !slices.Equal(spec.Containers[0].Args, []string{"apply"}) {
			t.Errorf("the pod runs %v on %q, want apply on node-2", spec.Containers[0].Args, spec.NodeName)
		}
		want := []toleration{{Operator: "Exists"}, {Key: "node.kubernetes.io/unschedulable", Operator: "Exists", Effect: "NoSchedule"}}
		if !slices.Equal(spec.Tolerations, want) {
			t.Errorf("the pod tolerates %+v, want every taint and the cordon by its key", spec.Tolerations)
		}
		if spec.ActiveDeadlineSeconds != nil || spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
			t.Errorf("the pod has the deadline %v and the token %v, want neither", spec.ActiveDeadlineSeconds, spec.AutomountServiceAccountToken)
		}
		probe := spec.Containers[0].ReadinessProbe
		if probe == nil || probe.Exec == nil || !slices.Equal(probe.Exec.Command, []string{"/usr/local/bin/scan", "prepared"}) || probe.PeriodSeconds != 2 {
			t.Errorf("the readiness probe is %+v, want scan prepared every 2 seconds", probe)
		}
		marker, markerMount := volumeNamed(t, spec, "prepared")
		if marker.EmptyDir == nil || marker.EmptyDir.Medium != "Memory" || markerMount.MountPath != "/run/apply" {
			t.Errorf("the marker's volume is %+v at %s, want a directory in memory at /run/apply", marker, markerMount.MountPath)
		}
		signal, signalMount := volumeNamed(t, spec, "signal")
		if signal.DownwardAPI == nil || len(signal.DownwardAPI.Items) != 1 ||
			signal.DownwardAPI.Items[0] != (downwardAPIFile{Path: "annotations", FieldRef: fieldRef{FieldPath: "metadata.annotations"}}) ||
			signalMount.MountPath != "/run/signal" {
			t.Errorf("the signal volume is %+v at %s, want the pod's annotations at /run/signal", signal, signalMount.MountPath)
		}
		host, hostMount := volumeNamed(t, spec, "host-firmware")
		if hostMount.MountPath != hostFirmwarePath || hostMount.ReadOnly || hostMount.RecursiveReadOnly != "" || host.HostPath.Path != "/sys/firmware" {
			t.Errorf("the firmware mount is %+v, want the machine's /sys/firmware read-write", hostMount)
		}
		security := spec.Containers[0].SecurityContext
		if security.Privileged != nil || !slices.Equal(security.Capabilities.Drop, []string{"ALL"}) ||
			!slices.Equal(security.Capabilities.Add, []string{"DAC_READ_SEARCH", "LINUX_IMMUTABLE"}) {
			t.Errorf("the security context is %+v, want no privilege and DAC_READ_SEARCH and LINUX_IMMUTABLE alone", security)
		}
		env := map[string]string{}
		for _, e := range spec.Containers[0].Env {
			env[e.Name] = e.Value
		}
		if env[appendSHA256Variable] != passed.SHA256 || env[deviceIDVariable] != "3fcf" || env[cabinetSHA256Variable] != biosCabinet {
			t.Errorf("the environment is %v, want the rehearsed append, the device, and the pinned cabinet", env)
		}
		if _, sent := p.Metadata.Annotations[writeKey]; sent {
			t.Errorf("the pod's annotations are %v, want no signal before the turn holds", p.Metadata.Annotations)
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWaiting || !strings.Contains(r.Message, p.Metadata.Name) {
			t.Errorf("the record is %+v, want Waiting with the pod's name", r)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Normal", reasonApplyPreparing}) {
			t.Errorf("the operator posted %v, want a Normal ApplyPreparing", got)
		}
	})
}

// A drain that starts before the prepare is done can evict the DNS that
// the prepare needs, so the operator asks for no turn while the pod
// prepares.
func TestNoRequestBeforeThePodIsReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := preparingWorld(t)
		time.Sleep(time.Hour)
		synctest.Wait()

		if got := w.api.created(disruptionsPath); got != 0 {
			t.Errorf("the operator filed %d requests, want none before the pod is Ready", got)
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWaiting || r.Disruption != "" {
			t.Errorf("the record is %+v, want Waiting with no Disruption", r)
		}
	})
}

// The pod's Ready condition is the only change when the prepare ends,
// so the watch wakes the pass for it, and the pass files the request.
func TestAReadyPodFilesTheRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := preparingWorld(t)
		w.api.readyPod(t, w.onlyApply(t).Metadata.Name)
		w.settle()

		if !w.api.disruptionExists(turnName) {
			t.Fatal("no request, want the machine's turn asked for")
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWaiting || r.Disruption != turnName {
			t.Errorf("the record is %+v, want Waiting for the turn of %s", r, turnName)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Normal", reasonApplyRequested}) {
			t.Errorf("the operator posted %v, want a Normal ApplyRequested", got)
		}
	})
}

// A pod that ends or is gone before it is Ready wrote nothing, and the
// machine needs no turn.
func TestAPodThatEndsBeforeItIsReadyAsksForNoTurn(t *testing.T) {
	cases := []struct {
		name    string
		end     func(t *testing.T, w *world, name string)
		message string
	}{
		{"a failed prepare", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podSucceeded, applyLog(applyResult{Result: resultFailed,
				Message: "the rehearsal before the write did not pass, so nothing was written: downloading the cabinet: lookup cdn.fwupd.org: no such host"}))
		}, "the rehearsal before the write did not pass, so nothing was written: downloading the cabinet: lookup cdn.fwupd.org: no such host"},
		{"a crash", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podFailed, "panic: runtime error\n")
		}, "the apply pod ended before the signal to write, so nothing was written: the apply wrote no result; its last line was: panic: runtime error"},
		{"a deleted pod", func(t *testing.T, w *world, name string) {
			w.api.deleteObject(uploadPodsPath, name)
		}, "the apply pod is gone before it prepared, so nothing was written, and the operator asked for no turn"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := preparingWorld(t)
				c.end(t, w, w.onlyApply(t).Metadata.Name)
				w.settle()
				time.Sleep(time.Hour)
				synctest.Wait()

				if r := w.onlyApplyRecord(t); r.Result != resultFailed || r.Message != c.message {
					t.Errorf("the record is %+v, want Failed: %s", r, c.message)
				}
				if got := w.api.created(disruptionsPath); got != 0 || len(w.api.applies(t)) != 0 {
					t.Errorf("the operator filed %d requests and left %d apply pods, want neither", got, len(w.api.applies(t)))
				}
				if ac, _ := appliedOf(t, w); ac.Status != conditions.False || ac.Reason != resultFailed {
					t.Errorf("Applied is %+v, want False with the reason Failed", ac)
				}
			})
		})
	}
}

func TestNoSignalBeforeTheTurnHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsedWorld(t)
		time.Sleep(time.Hour)
		synctest.Wait()

		if p := w.onlyApply(t); p.Metadata.Annotations[writeKey] != "" {
			t.Errorf("the pod's annotations are %v, want no signal", p.Metadata.Annotations)
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWaiting {
			t.Errorf("the record is %+v, want Waiting", r)
		}
	})
}

func TestAHeldTurnSignalsThePod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsedWorld(t)
		name := w.onlyApply(t).Metadata.Name
		held := time.Now()
		w.api.holdTurn(t, turnName)
		w.settle()

		p := w.onlyApply(t)
		sent := parseTime(p.Metadata.Annotations[writeAtKey])
		if p.Metadata.Name != name || p.Metadata.Annotations[writeKey] != writeGo || sent.Before(held.Truncate(time.Second)) || sent.After(time.Now()) {
			t.Errorf("the pod %s has the annotations %v, want the signal on %s, sent after the hold", p.Metadata.Name, p.Metadata.Annotations, name)
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWriting {
			t.Errorf("the record is %+v, want Writing", r)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Normal", reasonApplyStarted}) {
			t.Errorf("the operator posted %v, want a Normal ApplyStarted", got)
		}
		if got := w.api.created(uploadPodsPath); got != 3 {
			t.Errorf("the operator created %d pods, want the scan, the rehearsal, and one apply", got)
		}
	})
}

// A pod that ends before the signal cannot have written, so the record
// is Failed, and the machine leaves its turn with no reboot.
func TestAPodThatEndsBeforeTheSignalWritesNothing(t *testing.T) {
	cases := []struct {
		name    string
		phase   string
		log     string
		message string
	}{
		{"a crash", podFailed, "fwupdtool: loading\npanic: runtime error\n",
			"the apply pod ended before the signal to write, so nothing was written: the apply wrote no result; its last line was: panic: runtime error"},
		{"a prepare that failed", podSucceeded, applyLog(applyResult{Result: resultFailed,
			Message: "the rehearsal before the write did not pass, so nothing was written: downloading the cabinet: timeout"}),
			"the rehearsal before the write did not pass, so nothing was written: downloading the cabinet: timeout"},
		{"a stop after the prepare", podFailed, preparedLine + "\n",
			"the apply pod ended before the signal to write, so nothing was written: the apply wrote no result; its last line was: " + preparedLine},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := rehearsedWorld(t)
				w.api.finishPod(t, w.onlyApply(t).Metadata.Name, applyContainer, c.phase, c.log)
				w.settle()

				if r := w.onlyApplyRecord(t); r.Result != resultFailed || r.Message != c.message {
					t.Errorf("the record is %+v, want Failed: %s", r, c.message)
				}
				if w.api.disruptionExists(turnName) || len(w.api.applies(t)) != 0 {
					t.Error("a request or an apply pod remains, want neither")
				}
				if ac, _ := appliedOf(t, w); ac.Status != conditions.False || ac.Reason != resultFailed {
					t.Errorf("Applied is %+v, want False with the reason Failed", ac)
				}
				if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Warning", reasonApplyFailed}) {
					t.Errorf("the operator posted %v, want a Warning ApplyFailed", got)
				}
			})
		})
	}
}

// A pod with no signal wrote nothing whatever its log says, so a log
// the operator may not read ends the record too, instead of leaving the
// machine in its turn.
func TestAnUnreadableLogBeforeTheSignalStillFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsedWorld(t)
		w.api.logsForbidden = true
		w.api.finishPod(t, w.onlyApply(t).Metadata.Name, applyContainer, podFailed, "panic: runtime error\n")
		w.settle()

		want := "the apply pod ended before the signal to write, so nothing was written: the operator could not read the apply's log: "
		if r := w.onlyApplyRecord(t); r.Result != resultFailed || !strings.HasPrefix(r.Message, want) {
			t.Errorf("the record is %+v, want Failed: %s...", r, want)
		}
		if w.api.disruptionExists(turnName) {
			t.Error("the request remains, want it withdrawn")
		}
	})
}

// A drain that does not skip the apply pod evicts it. The operator does
// not start it again while the drain runs, and starts a new pod that
// already carries the signal once the turn holds, which prepares and
// writes at once.
func TestAnEvictedPodIsReplacedWithTheSignal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsedWorld(t)
		w.api.deleteObject(uploadPodsPath, w.onlyApply(t).Metadata.Name)
		w.settle()
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := len(w.api.applies(t)); got != 0 {
			t.Fatalf("%d apply pods before the hold, want none", got)
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWaiting {
			t.Fatalf("before the hold the record is %+v, want Waiting", r)
		}

		w.api.holdTurn(t, turnName)
		w.settle()

		if p := w.onlyApply(t); p.Metadata.Annotations[writeKey] != writeGo || p.Metadata.Annotations[writeAtKey] == "" {
			t.Errorf("the new pod's annotations are %v, want the signal", p.Metadata.Annotations)
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWriting {
			t.Errorf("the record is %+v, want Writing", r)
		}
		if got := w.api.created(uploadPodsPath); got != 4 {
			t.Errorf("the operator created %d pods, want the scan, the rehearsal, and two applies", got)
		}
	})
}

// A pod can wait for its turn far longer than a write takes, so the
// deadline counts from the signal, not from the pod's creation.
func TestTheDeadlineCountsFromTheSignal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsedWorld(t)
		time.Sleep(2 * scanDeadline)
		w.api.holdTurn(t, turnName)
		w.settle()
		time.Sleep(scanDeadline / 2)
		synctest.Wait()
		if r := w.onlyApplyRecord(t); r.Result != resultWriting {
			t.Fatalf("halfway to the deadline the record is %+v, want Writing", r)
		}

		time.Sleep(scanDeadline)
		synctest.Wait()

		if r := w.onlyApplyRecord(t); r.Result != resultUncertain || r.Message != "the apply pod did not finish within 10m0s of the signal to write" {
			t.Errorf("the record is %+v, want Uncertain past the deadline", r)
		}
	})
}

// terminatePod plays the API server after a delete of a pod whose
// containers still stop: the pod remains, with its deletion started.
func (a *fakeAPI) terminatePod(t *testing.T, name string) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[uploadPodsPath][name])
	if next == nil {
		t.Fatalf("there is no pod %s", name)
	}
	next["metadata"].(map[string]any)["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	a.store(uploadPodsPath, next, "MODIFIED")
}

// An apply attempt keeps its pod's name, so a new apply of the same
// attempt can meet the old pod while it stops. A create then answers
// 409 for a pod that is about to go. The operator waits for the old
// pod to go before it starts the new one.
func TestANewApplyWaitsForTheOldPodToGo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := preparingWorld(t)
		old := w.onlyApply(t).Metadata.Name
		w.api.terminatePod(t, old)
		f := w.api.firmware(t, biosObjectName)
		f.Spec.Update.Version, f.Spec.Update.Cabinet = "", nil
		w.api.putFirmware(f)
		w.settle()
		approve(t, w, "0.0.4.2", biosCabinet, nil)
		w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded, rehearsalLog(passed))
		w.settle()
		if records := w.api.firmware(t, biosObjectName).Status.Applies; len(records) != 0 {
			t.Fatalf("status.applies = %+v while the old pod stops, want none", records)
		}

		w.api.deleteObject(uploadPodsPath, old)
		w.settle()

		if r := w.onlyApplyRecord(t); r.Result != resultWaiting {
			t.Errorf("the record is %+v, want Waiting for the new pod", r)
		}
		if p := w.onlyApply(t); p.Metadata.DeletionTimestamp != "" {
			t.Errorf("the apply pod is %+v, want a new one", p.Metadata)
		}
	})
}
