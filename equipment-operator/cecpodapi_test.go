package main

// The pods of the node workload in the fake API server
// (cecapi_test.go). Until a test declares a pod, the fake serves one
// Ready pod on each machine that a CECBus names, the way a cluster
// whose node workloads all run does. A test of staleness declares the
// pods itself with putPod, and from then on the fake serves only the
// declared pods.

import (
	"net/http"
	"slices"
	"strings"
	"time"
)

// testNamespace is the namespace of both workloads in the tests, as
// liken-system is in deploy/.
const testNamespace = "liken-system"

var fakePodsPath = cecPodsPath(testNamespace)

func init() {
	fakeKinds[fakePodsPath] = [2]string{"v1", "Pod"}
}

// fakePod is one pod of the node workload: the node it runs on,
// whether it is Ready, and when its Ready condition last changed.
type fakePod struct {
	node  string
	ready bool
	since time.Time
}

// podName is the name the fake gives a machine's pod.
func podName(machine string) string {
	return "equipment-operator-cec-" + machine
}

// podObjects answers each pod the fake serves, by name. The caller
// holds the mutex.
func (a *cecAPI) podObjects() map[string]any {
	pods := a.pods
	if !a.podsDeclared {
		pods = map[string]fakePod{}
		for _, bus := range a.buses {
			for _, adapter := range bus.Spec.Adapters {
				pods[podName(adapter.Machine)] = fakePod{node: adapter.Machine, ready: true}
			}
		}
	}
	objects := map[string]any{}
	for name, held := range pods {
		pod := cecPod{Metadata: podMeta{Name: name, Namespace: testNamespace, UID: "uid-" + name}}
		pod.Spec.NodeName = held.node
		status := "False"
		if held.ready {
			status = "True"
		}
		pod.Status.Conditions = []podCondition{{Type: "PodScheduled", Status: "True"}, {Type: "Ready", Status: status, LastTransitionTime: timestamp(held.since)}}
		objects[name] = pod
	}
	return objects
}

// servePodAPI answers the list, the watch, and the read of one pod.
func (a *cecAPI) servePodAPI(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	switch {
	case r.Method != http.MethodGet || !strings.HasPrefix(path, fakePodsPath):
		return false
	case path == fakePodsPath && r.URL.Query().Get("watch") == "true":
		a.serveWatch(w, r)
	case path == fakePodsPath:
		a.mutex.Lock()
		a.serveCollection(w, fakePodsPath, "")
		a.mutex.Unlock()
	default:
		a.mutex.Lock()
		a.serveStored(w, fakePodsPath, strings.TrimPrefix(path, fakePodsPath+"/"))
		a.mutex.Unlock()
	}
	return true
}

// putPod declares the node workload's pod on one machine, Ready or
// not, and tells the pod watches. The Ready condition's transition
// time moves only when the pod is new or its readiness changes, the
// way the kubelet writes it.
func (a *cecAPI) putPod(machine string, ready bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.pods == nil {
		a.pods = map[string]fakePod{}
	}
	a.podsDeclared = true
	held, found := a.pods[podName(machine)]
	if !found || held.ready != ready {
		held.since = time.Now()
	}
	held.node, held.ready = machine, ready
	a.pods[podName(machine)] = held
	a.changedIn(fakePodsPath)
}

// writtenNow stamps an entry with the bubble's clock, as the node
// workload stamps each write.
func writtenNow(entry CECAdapterStatus) CECAdapterStatus {
	entry.ReportedAt = timestamp(time.Now())
	return entry
}

// dropEntry removes one machine's entry from a bus behind the node
// workload's back, as a restore or another writer can, and tells the
// watches.
func (a *cecAPI) dropEntry(bus, machine string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	held := a.buses[bus]
	held.Status.Adapters = slices.DeleteFunc(held.Status.Adapters, func(entry CECAdapterStatus) bool { return entry.Machine == machine })
	a.changed()
}

// alterEntry changes one machine's stored entry behind the node
// workload's back, and tells the watches.
func (a *cecAPI) alterEntry(bus, machine string, change func(*CECAdapterStatus)) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	held := a.buses[bus]
	for index := range held.Status.Adapters {
		if held.Status.Adapters[index].Machine == machine {
			change(&held.Status.Adapters[index])
		}
	}
	a.changed()
}

// removePod deletes the node workload's pod on one machine.
func (a *cecAPI) removePod(machine string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.podsDeclared = true
	delete(a.pods, podName(machine))
	a.changedIn(fakePodsPath)
}

// readyPods is the list of the node workload's pods that a canned API
// server answers: one Ready pod on each machine.
func readyPods(machines ...string) map[string]any {
	items := []cecPod{}
	for _, machine := range machines {
		pod := cecPod{Metadata: podMeta{Name: podName(machine), Namespace: testNamespace}}
		pod.Spec.NodeName = machine
		pod.Status.Conditions = []podCondition{{Type: "Ready", Status: "True"}}
		items = append(items, pod)
	}
	return map[string]any{"items": items}
}
