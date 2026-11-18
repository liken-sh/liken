package main

// The node workload's pods in the fake API server (cecapi_test.go).
// Until a test calls putPod or removePod, the fake serves one Ready pod
// for each machine that a CECBus lists. After the first call, it serves
// only the pods that the test put.

import (
	"net/http"
	"slices"
	"strings"
	"time"
)

// testNamespace is the namespace that deploy/ uses.
const testNamespace = "liken-system"

var fakePodsPath = cecPodsPath(testNamespace)

func init() {
	fakeKinds[fakePodsPath] = [2]string{"v1", "Pod"}
}

// fakePod is one pod of the node workload. since is when its Ready
// condition last changed.
type fakePod struct {
	node  string
	ready bool
	since time.Time
}

func podName(machine string) string {
	return "equipment-operator-cec-" + machine
}

// podObjects returns the pods the fake serves, by name. The caller
// holds a.mutex.
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
	for name, declared := range pods {
		pod := cecPod{Metadata: podMeta{Name: name, Namespace: testNamespace, UID: "uid-" + name}}
		pod.Spec.NodeName = declared.node
		status := "False"
		if declared.ready {
			status = "True"
		}
		pod.Status.Conditions = []podCondition{{Type: "PodScheduled", Status: "True"}, {Type: "Ready", Status: status, LastTransitionTime: timestamp(declared.since)}}
		objects[name] = pod
	}
	return objects
}

// servePodAPI serves the list, the watch, and the get of one pod. It
// returns false for any other request.
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

// putPod creates or updates the node workload's pod on one machine and
// notifies the pod watches. Like the kubelet, it changes the Ready
// condition's lastTransitionTime only when the pod is new or its
// readiness changes.
func (a *cecAPI) putPod(machine string, ready bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.pods == nil {
		a.pods = map[string]fakePod{}
	}
	a.podsDeclared = true
	pod, found := a.pods[podName(machine)]
	if !found || pod.ready != ready {
		pod.since = time.Now()
	}
	pod.node, pod.ready = machine, ready
	a.pods[podName(machine)] = pod
	a.changedIn(fakePodsPath)
}

// writtenNow sets the entry's ReportedAt to the bubble's clock time,
// as the node workload does on each write.
func writtenNow(entry CECAdapterStatus) CECAdapterStatus {
	entry.ReportedAt = timestamp(time.Now())
	return entry
}

// dropEntry deletes a machine's entry from a bus, as a writer other
// than the node workload could, and notifies the watches.
func (a *cecAPI) dropEntry(bus, machine string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	stored := a.buses[bus]
	stored.Status.Adapters = slices.DeleteFunc(stored.Status.Adapters, func(entry CECAdapterStatus) bool { return entry.Machine == machine })
	a.changed()
}

// alterEntry applies change to a machine's entry, as a writer other
// than the node workload could, and notifies the watches.
func (a *cecAPI) alterEntry(bus, machine string, change func(*CECAdapterStatus)) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	stored := a.buses[bus]
	for index := range stored.Status.Adapters {
		if stored.Status.Adapters[index].Machine == machine {
			change(&stored.Status.Adapters[index])
		}
	}
	a.changed()
}

func (a *cecAPI) removePod(machine string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.podsDeclared = true
	delete(a.pods, podName(machine))
	a.changedIn(fakePodsPath)
}

// readyPods returns a list of one Ready node workload pod for each
// machine, for tests that serve a canned API response.
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
