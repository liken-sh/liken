package main

// These tests follow the apply pod from its start to the signal to
// write. The pod prepares before the drain, the operator creates the
// Disruption once the pod is Ready, and it sends the signal once
// machine-operator sets Held on the Disruption.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// preparedLine is the progress line that `scan apply` prints when its
// prepare step succeeds, before it waits for the signal.
const preparedLine = `{"phase":"prepared","message":"the rehearsal passed with the recorded append to dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f, and the apply waits for the operator's signal to write","variable":"dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f","sha256":"cf901743"}`

// volumeNamed returns a pod's volume and the first container's mount
// of it.
func volumeNamed(t *testing.T, spec podSpec, name string) (volume, volumeMount) {
	t.Helper()
	v := slices.IndexFunc(spec.Volumes, func(v volume) bool { return v.Name == name })
	m := slices.IndexFunc(spec.Containers[0].VolumeMounts, func(m volumeMount) bool { return m.Name == name })
	if v < 0 || m < 0 {
		t.Fatalf("the pod has no volume %s mounted: %+v", name, spec)
	}
	return spec.Volumes[v], spec.Containers[0].VolumeMounts[m]
}

// runPod acts as the kubelet: it marks the apply pod Running and sets
// its Ready condition to the result of the readiness probe.
func (a *fakeAPI) runPod(t *testing.T, name string, ready bool) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[operatorPodsPath][name])
	if next == nil {
		t.Fatalf("there is no pod %s", name)
	}
	readiness := map[bool]string{true: "True", false: "False"}[ready]
	next["status"] = map[string]any{"phase": "Running", "conditions": []any{
		map[string]any{"type": "PodScheduled", "status": "True"},
		map[string]any{"type": "Ready", "status": readiness},
	}}
	a.store(operatorPodsPath, next, "MODIFIED")
}

// readyPod acts as the kubelet after the apply pod's readiness probe
// succeeds.
func (a *fakeAPI) readyPod(t *testing.T, name string) {
	t.Helper()
	a.runPod(t, name, true)
}

// The apply pod starts when the rehearsal passes, so its prepare step
// is done before the drain evicts DNS. The liken drain does not evict a
// pod that tolerates the cordon taint by its key. The pod reads the
// signal from its own annotations, so it needs no token.
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
		if probe == nil || probe.Exec == nil || !slices.Equal(probe.Exec.Command, []string{"/scan", "prepared"}) || probe.PeriodSeconds != 2 {
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
			t.Errorf("the pod's annotations are %v, want no signal before machine-operator holds the Disruption", p.Metadata.Annotations)
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWaiting || !strings.Contains(r.Message, p.Metadata.Name) {
			t.Errorf("the record is %+v, want Waiting with the pod's name", r)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Normal", reasonApplyPreparing}) {
			t.Errorf("the operator posted %v, want a Normal ApplyPreparing", got)
		}
	})
}

// A drain that starts before the prepare step ends can evict the DNS
// that the step needs, so the operator creates no Disruption while the
// pod prepares.
func TestNoRequestBeforeThePodIsReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := preparingWorld(t)
		time.Sleep(time.Hour)
		synctest.Wait()

		if got := w.api.created(disruptionsPath); got != 0 {
			t.Errorf("the operator created %d Disruptions, want none before the pod is Ready", got)
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWaiting || r.Disruption != "" {
			t.Errorf("the record is %+v, want Waiting with no Disruption", r)
		}
	})
}

// The pod's Ready condition is the only change when the prepare step
// ends, so the watch must start a pass for it, and that pass creates
// the Disruption.
func TestAReadyPodCreatesTheDisruption(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := preparingWorld(t)
		w.api.readyPod(t, w.onlyApply(t).Metadata.Name)
		w.settle()

		if !w.api.disruptionExists(turnName) {
			t.Fatal("no Disruption exists, want one that requests the machine's turn")
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWaiting || r.Disruption != turnName {
			t.Errorf("the record is %+v, want Waiting for the turn of %s", r, turnName)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Normal", reasonApplyRequested}) {
			t.Errorf("the operator posted %v, want a Normal ApplyRequested", got)
		}
	})
}

// A pod that ends or disappears before it is Ready has written
// nothing, so the machine needs no turn.
func TestAPodThatEndsBeforeItIsReadyCreatesNoDisruption(t *testing.T) {
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
		}, "the apply pod ended before the signal to write, so nothing was written: the apply pod wrote no result, and its last log line is: panic: runtime error"},
		{"a deleted pod", func(t *testing.T, w *world, name string) {
			w.api.deleteObject(operatorPodsPath, name)
		}, "the apply pod no longer exists and did not finish preparing, so nothing was written and the operator created no Disruption"},
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
					t.Errorf("the operator created %d Disruptions and left %d apply pods, want neither", got, len(w.api.applies(t)))
				}
				if ac, _ := appliedOf(t, w); ac.Status != conditions.False || ac.Reason != resultFailed {
					t.Errorf("Applied is %+v, want False with the reason Failed", ac)
				}
			})
		})
	}
}

func TestNoSignalBeforeTheDisruptionIsHeld(t *testing.T) {
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

func TestAHeldDisruptionSignalsThePod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsedWorld(t)
		name := w.onlyApply(t).Metadata.Name
		before := time.Now()
		w.api.holdTurn(t, turnName)
		w.settle()

		p := w.onlyApply(t)
		sent := parseTime(p.Metadata.Annotations[writeAtKey])
		if p.Metadata.Name != name || p.Metadata.Annotations[writeKey] != writeGo || sent.Before(before.Truncate(time.Second)) || sent.After(time.Now()) {
			t.Errorf("the pod %s has the annotations %v, want the signal on %s, sent after the hold", p.Metadata.Name, p.Metadata.Annotations, name)
		}
		if r := w.onlyApplyRecord(t); r.Result != resultWriting {
			t.Errorf("the record is %+v, want Writing", r)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Normal", reasonApplyStarted}) {
			t.Errorf("the operator posted %v, want a Normal ApplyStarted", got)
		}
		if got := w.api.created(operatorPodsPath); got != 3 {
			t.Errorf("the operator created %d pods, want the scan, the rehearsal, and one apply", got)
		}
	})
}

// A pod that ends before the signal cannot have written, so the
// operator records Failed and deletes the Disruption, and the machine
// ends its turn without a reboot.
func TestAPodThatEndsBeforeTheSignalWritesNothing(t *testing.T) {
	cases := []struct {
		name    string
		phase   string
		log     string
		message string
	}{
		{"a crash", podFailed, "fwupdtool: loading\npanic: runtime error\n",
			"the apply pod ended before the signal to write, so nothing was written: the apply pod wrote no result, and its last log line is: panic: runtime error"},
		{"a prepare that failed", podSucceeded, applyLog(applyResult{Result: resultFailed,
			Message: "the rehearsal before the write did not pass, so nothing was written: downloading the cabinet: timeout"}),
			"the rehearsal before the write did not pass, so nothing was written: downloading the cabinet: timeout"},
		{"a stop after the prepare", podFailed, preparedLine + "\n",
			"the apply pod ended before the signal to write, so nothing was written: the apply pod wrote no result, and its last log line is: " + preparedLine},
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
					t.Error("a Disruption or an apply pod remains, want neither")
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

// A pod that ended before the signal has written nothing, whatever its
// log says. So when the operator cannot read the log, it still records
// Failed and deletes the Disruption, and the machine does not stay
// drained.
func TestAnUnreadableLogBeforeTheSignalStillFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsedWorld(t)
		w.api.logsForbidden = true
		w.api.finishPod(t, w.onlyApply(t).Metadata.Name, applyContainer, podFailed, "panic: runtime error\n")
		w.settle()

		want := "the apply pod ended before the signal to write, so nothing was written: the operator cannot read the log of the apply pod: "
		if r := w.onlyApplyRecord(t); r.Result != resultFailed || !strings.HasPrefix(r.Message, want) {
			t.Errorf("the record is %+v, want Failed: %s...", r, want)
		}
		if w.api.disruptionExists(turnName) {
			t.Error("the Disruption remains, want it deleted")
		}
	})
}

// A drain that does not skip the apply pod, such as `kubectl drain`,
// evicts it. The operator does not start the pod again while the drain
// runs. When machine-operator sets Held, the operator starts a new pod
// that already has the signal, so the pod prepares and writes at once.
func TestAnEvictedPodIsReplacedWithTheSignal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsedWorld(t)
		w.api.deleteObject(operatorPodsPath, w.onlyApply(t).Metadata.Name)
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
		if got := w.api.created(operatorPodsPath); got != 4 {
			t.Errorf("the operator created %d pods, want the scan, the rehearsal, and two applies", got)
		}
	})
}

// A pod can wait for its turn much longer than a write takes, so the
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

// terminatePod acts as the API server after a delete of a pod whose
// containers are still stopping: the pod remains, with its deletion
// timestamp set.
func (a *fakeAPI) terminatePod(t *testing.T, name string) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[operatorPodsPath][name])
	if next == nil {
		t.Fatalf("there is no pod %s", name)
	}
	next["metadata"].(map[string]any)["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	a.store(operatorPodsPath, next, "MODIFIED")
}

// An apply attempt always uses the same pod name, so creating a new pod
// while the old one is still stopping returns 409. The operator waits
// for the old pod to be gone before it starts the new one.
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

		w.api.deleteObject(operatorPodsPath, old)
		w.settle()

		if r := w.onlyApplyRecord(t); r.Result != resultWaiting {
			t.Errorf("the record is %+v, want Waiting for the new pod", r)
		}
		if p := w.onlyApply(t); p.Metadata.DeletionTimestamp != "" {
			t.Errorf("the apply pod is %+v, want a new one", p.Metadata)
		}
	})
}
