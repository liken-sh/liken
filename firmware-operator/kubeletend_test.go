package main

// The kubelet can end a pod for a reason that is not the pod's own
// program: an eviction, a rejection at admission, a graceful node
// shutdown, or a preemption. A scan and a rehearsal write nothing to a
// device, so when the kubelet ends one for any of these reasons except
// a shutdown, the operator runs the same attempt again in a new pod.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/endedpods"
)

// endByKubelet acts as the kubelet that rejects or evicts a pod: the
// pod ends Failed with the pod-level reason and message, and its
// container never ran, so its log is empty.
func (a *fakeAPI) endByKubelet(t *testing.T, name, reason, message string) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[operatorPodsPath][name])
	if next == nil {
		t.Fatalf("there is no pod %s", name)
	}
	a.logs[name] = ""
	next["status"] = map[string]any{"phase": podFailed, "reason": reason, "message": message}
	a.store(operatorPodsPath, next, "MODIFIED")
}

// A scan pod that the kubelet rejected waits 10 seconds from its
// creation, then the operator scans the machine again in a new pod, and
// the record stays Running.
func TestARejectedScanRunsAgainAfterTenSeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		first := w.onlyScan(t)
		w.api.endByKubelet(t, first.Metadata.Name, "OutOfcpu", "Node didn't have enough resource: cpu, requested: 10, used: 3990, capacity: 4000")
		w.settle()

		r := w.record(t, "lvfs", "node-2")
		if r.Result != resultRunning || !strings.Contains(r.Message, "OutOfcpu") {
			t.Errorf("while the scan waits the record is %+v, want Running with the kubelet's reason", r)
		}
		if got := w.api.created(operatorPodsPath); got != 1 {
			t.Errorf("before 10 seconds the operator created %d scan pods, want 1", got)
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()

		second := w.onlyScan(t)
		if second.Metadata.Name == first.Metadata.Name || second.Metadata.Annotations[triesKey] != "1" || scanOf(second) != scanOf(first) {
			t.Errorf("after 10 seconds the scan pod is %+v, want a new pod of the same attempt with tries 1", second.Metadata)
		}
		w.api.finishScan(t, second.Metadata.Name, podSucceeded, resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"))))
		w.settle()

		if r := w.record(t, "lvfs", "node-2"); r.Result != resultScanned {
			t.Errorf("after the second pod the record is %+v, want Scanned", r)
		}
		want := []posted{{"Normal", reasonFetched}, {"Normal", reasonScanStarted}, {"Normal", reasonScanPodRecreated}, {"Normal", reasonScanned}}
		if got := w.postedAboutCatalog("lvfs"); !slices.Equal(got, want) {
			t.Errorf("the Events are %v, want %v", got, want)
		}
	})
}

// Each pod that the kubelet ends doubles the wait before the next one,
// and only the first new pod posts an Event.
func TestEachRejectionDoublesTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		w.api.endByKubelet(t, w.onlyScan(t).Metadata.Name, "OutOfmemory", "")
		time.Sleep(10 * time.Second)
		synctest.Wait()
		w.api.endByKubelet(t, w.onlyScan(t).Metadata.Name, "OutOfmemory", "")
		time.Sleep(18 * time.Second)
		synctest.Wait()

		if got := w.api.created(operatorPodsPath); got != 2 {
			t.Errorf("18 seconds after the second pod the operator created %d scan pods, want 2", got)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()

		if got := w.onlyScan(t).Metadata.Annotations[triesKey]; got != "2" {
			t.Errorf("20 seconds after the second pod the scan pod has tries %q, want 2", got)
		}
		recreated := 0
		for _, p := range w.postedAboutCatalog("lvfs") {
			if p.reason == reasonScanPodRecreated {
				recreated++
			}
		}
		if recreated != 1 {
			t.Errorf("the operator posted %d %s Events, want 1", recreated, reasonScanPodRecreated)
		}
	})
}

// The kubelet's message often ends with a period, and the operator
// joins it into a sentence, so the description drops that period.
func TestTheKubeletsEndDropsTheMessagesLastPeriod(t *testing.T) {
	p := pod{Status: podStatus{Phase: podFailed, Reason: "Terminated", Message: "Pod was terminated in response to imminent node shutdown."}}
	want := "Terminated: Pod was terminated in response to imminent node shutdown"
	if got := p.kubeletEnd(); got != want {
		t.Errorf("kubeletEnd = %q, want %q", got, want)
	}
}

// A pod that the kubelet ended for a node shutdown belongs to a boot
// that is ending, so it is not run again.
func TestAShutdownIsToldFromOtherEnds(t *testing.T) {
	cases := []struct {
		name   string
		status podStatus
		want   bool
	}{
		{"a graceful node shutdown", podStatus{Phase: podFailed, Reason: "Terminated", Message: "Pod was terminated in response to imminent node shutdown."}, true},
		{"a shutdown reason", podStatus{Phase: podFailed, Reason: "Shutdown"}, true},
		{"a rejection while the node shuts down", podStatus{Phase: podFailed, Reason: "NodeShutdown", Message: "Pod was rejected as the node is shutting down."}, true},
		{"a preemption by the kubelet for a critical pod", podStatus{Phase: podFailed, Reason: "Preempting", Conditions: []podCondition{{Type: "DisruptionTarget", Status: "True", Reason: "TerminationByKubelet"}}}, false},
		{"a disruption by the kubelet with no reason", podStatus{Phase: podFailed, Conditions: []podCondition{{Type: "DisruptionTarget", Status: "True", Reason: "TerminationByKubelet"}}}, true},
		{"an eviction", podStatus{Phase: podFailed, Reason: "Evicted", Conditions: []podCondition{{Type: "DisruptionTarget", Status: "True", Reason: "TerminationByKubelet"}}}, false},
		{"a rejection for CPU", podStatus{Phase: podFailed, Reason: "OutOfcpu"}, false},
		{"a preemption", podStatus{Phase: podFailed, Conditions: []podCondition{{Type: "DisruptionTarget", Status: "True", Reason: "PreemptionByScheduler"}}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (pod{Status: c.status}).endedAtShutdown(); got != c.want {
				t.Errorf("endedAtShutdown of %+v = %v, want %v", c.status, got, c.want)
			}
		})
	}
}

// At a node shutdown the kubelet ends the scan pod before the Machine
// reports the new boot. The scan is not run again for the boot that
// ended: the record is Failed with the kubelet's reason, and the new
// boot starts a scan of its own.
func TestAScanEndedAtAShutdownIsNotRunAgain(t *testing.T) {
	cases := []struct {
		name, reason, message string
	}{
		{"a graceful node shutdown", "Terminated", "Pod was terminated in response to imminent node shutdown."},
		{"a rejection while the node shuts down", "NodeShutdown", "Pod was rejected as the node is shutting down."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := discoveryWorld(t)
				w.api.endByKubelet(t, w.onlyScan(t).Metadata.Name, c.reason, c.message)
				w.settle()
				time.Sleep(time.Minute)
				synctest.Wait()

				if got := w.api.created(operatorPodsPath); got != 1 {
					t.Errorf("before the new boot the operator created %d scan pods, want 1", got)
				}
				if r := w.record(t, "lvfs", "node-2"); r.Result != resultFailed || !strings.Contains(r.Message, c.reason) {
					t.Errorf("before the new boot the record is %+v, want Failed with the kubelet's reason", r)
				}
				w.api.putFleetMachine("node-2", machineReady, secondBoot)
				w.settle()

				if scan := w.onlyScan(t); scanOf(scan).boot != secondBoot || scan.Metadata.Annotations[triesKey] != "" {
					t.Errorf("after the new boot the scan pod is %+v, want the first pod of a scan of the new boot", scan.Metadata)
				}
			})
		})
	}
}

// An eviction can be the pod's own fault, so after the kubelet ended
// maxTaskPods scan pods of one attempt the record is Failed, with the
// kubelet's reason and the count.
func TestAScanStopsAfterTheKubeletEndedFivePods(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		for range maxTaskPods {
			w.api.endByKubelet(t, w.onlyScan(t).Metadata.Name, "Evicted", "Pod ephemeral local storage usage exceeds the total limit of containers 16Mi.")
			time.Sleep(endedpods.MaxWait)
			synctest.Wait()
		}

		r := w.record(t, "lvfs", "node-2")
		if r.Result != resultFailed || !strings.Contains(r.Message, "ephemeral local storage") || !strings.Contains(r.Message, fmt.Sprintf("%d scan pods", maxTaskPods)) {
			t.Errorf("the record is %+v, want Failed with the kubelet's reason and the count of pods", r)
		}
		if got := w.api.created(operatorPodsPath); got != maxTaskPods {
			t.Errorf("the operator created %d scan pods, want %d", got, maxTaskPods)
		}
		if !slices.Contains(w.postedAboutCatalog("lvfs"), posted{"Warning", reasonScanFailed}) {
			t.Errorf("the Events are %v, want a Warning ScanFailed", w.postedAboutCatalog("lvfs"))
		}
	})
}

// While a scan waits for its next pod, a pass that another change
// starts writes the record again only when its message changed.
func TestAWaitingScanDoesNotRewriteItsRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		w.api.endByKubelet(t, w.onlyScan(t).Metadata.Name, "OutOfcpu", "")
		w.settle()
		before := w.record(t, "lvfs", "node-2")
		w.api.putFirmware(qhy5lii())
		w.settle()

		if after := w.record(t, "lvfs", "node-2"); after.Time != before.Time || after.Message != before.Message {
			t.Errorf("after another pass the record is %+v, want it unchanged from %+v", after, before)
		}
	})
}

// putPod stores a pod that an earlier operator process created.
func (a *fakeAPI) putPod(p pod) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object := toMap(p)
	metadata := object["metadata"].(map[string]any)
	metadata["uid"] = "uid-earlier"
	metadata["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	object["status"] = map[string]any{"phase": podPending}
	a.store(operatorPodsPath, object, "ADDED")
}

// An earlier operator that created the next pod and stopped before it
// deleted the ended pod already posted the Event. The operator that
// finds both posts none.
func TestANextPodThatExistsPostsNoEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		ended := w.onlyScan(t)
		w.api.endByKubelet(t, ended.Metadata.Name, "OutOfcpu", "")
		w.api.putPod(nextTry(ended))
		time.Sleep(10 * time.Second)
		synctest.Wait()

		if got := w.postedAboutCatalog("lvfs"); slices.Contains(got, posted{"Normal", reasonScanPodRecreated}) {
			t.Errorf("the Events are %v, want no %s", got, reasonScanPodRecreated)
		}
		if got := len(w.api.scans(t)); got != 1 {
			t.Errorf("%d scan pods remain, want the next pod alone", got)
		}
	})
}
