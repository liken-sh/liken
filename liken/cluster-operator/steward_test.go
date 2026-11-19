package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

func machineRunning(name, version string) machine.Machine {
	m := machine.Machine{}
	m.Metadata.Name = name
	m.Status.Version.Liken = version
	return m
}

func operatorPod(name, node, osVersion string) kubernetes.Pod {
	p := kubernetes.Pod{}
	p.Metadata.Name = name
	p.Metadata.Namespace = "liken-system"
	if osVersion != "" {
		p.Metadata.Annotations = map[string]string{osVersionAnnotation: osVersion}
	}
	p.Spec.NodeName = node
	return p
}

func refreshNames(pods []kubernetes.Pod) []string {
	names := make([]string, 0, len(pods))
	for _, p := range pods {
		names = append(names, p.Metadata.Name)
	}
	return names
}

func TestDecideRefreshEvictsAnOldPodOnAnUpgradedMachine(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "0.1.0")}
	refresh := decideRefresh("0.2.2", machines, pods)
	if got := refreshNames(refresh); len(got) != 1 || got[0] != "op-1" {
		t.Fatalf("refreshed pods = %v, want [op-1]", got)
	}
}

func TestDecideRefreshEvictsAPodWithNoVersionAnnotation(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "")}
	refresh := decideRefresh("0.2.2", machines, pods)
	if len(refresh) != 1 {
		t.Fatalf("refreshed pods = %v, want one", refreshNames(refresh))
	}
}

func TestDecideRefreshLeavesCurrentPodsAlone(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "0.2.2")}
	if refresh := decideRefresh("0.2.2", machines, pods); len(refresh) != 0 {
		t.Fatalf("refreshed pods = %v, want none", refreshNames(refresh))
	}
}

func TestDecideRefreshWaitsForTheMachineToUpgrade(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.1.0")}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "0.1.0")}
	if refresh := decideRefresh("0.2.2", machines, pods); len(refresh) != 0 {
		t.Fatalf("refreshed pods = %v, want none", refreshNames(refresh))
	}
}

func TestDecideRefreshWaitsForTheManifestsOfTheMachinesRelease(t *testing.T) {
	// A worker can upgrade before any leader has applied the new
	// release's manifests.
	machines := []machine.Machine{machineRunning("node-1", "0.2.3")}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "0.2.2")}
	if refresh := decideRefresh("0.2.2", machines, pods); len(refresh) != 0 {
		t.Fatalf("refreshed pods = %v, want none", refreshNames(refresh))
	}
}

func TestDecideRefreshDoesNothingWithoutAVersionedDaemonSet(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "0.1.0")}
	if refresh := decideRefresh("", machines, pods); len(refresh) != 0 {
		t.Fatalf("refreshed pods = %v, want none", refreshNames(refresh))
	}
}

func TestDecideRefreshLeavesATerminatingPodAlone(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pod := operatorPod("op-1", "node-1", "0.1.0")
	pod.Metadata.DeletionTimestamp = "2026-09-29T06:40:41Z"
	if refresh := decideRefresh("0.2.2", machines, []kubernetes.Pod{pod}); len(refresh) != 0 {
		t.Fatalf("refreshed pods = %v, want none", refreshNames(refresh))
	}
}

func TestDecideRefreshIgnoresPodsOnUnknownMachines(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []kubernetes.Pod{operatorPod("op-x", "node-9", "0.1.0")}
	if refresh := decideRefresh("0.2.2", machines, pods); len(refresh) != 0 {
		t.Fatalf("refreshed pods = %v, want none", refreshNames(refresh))
	}
}

// stewardServer serves each DaemonSet in daemonSets with that
// os-version annotation, and 404 for any other. Every pod listing
// returns one pod of release 0.1.0 on node-1, and each eviction is
// appended to evicted.
func stewardServer(t *testing.T, daemonSets map[string]string, evicted *[]string) *apiclient.Client {
	t.Helper()
	return testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/eviction"):
			parts := strings.Split(r.URL.Path, "/")
			*evicted = append(*evicted, parts[len(parts)-2])
			w.WriteHeader(http.StatusCreated)
		case strings.Contains(r.URL.Path, "/daemonsets/"):
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			version, ok := daemonSets[name]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]string{osVersionAnnotation: version},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/pods"):
			app := r.URL.Query().Get("labelSelector")
			pod := operatorPod(strings.TrimPrefix(app, "app=")+"-pod", "node-1", "0.1.0")
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []kubernetes.Pod{pod}})
		default:
			t.Errorf("refreshOSPods requested %s, which the test server does not serve", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestRefreshOSPodsEvictsFromEveryOSDaemonSet(t *testing.T) {
	var evicted []string
	client := stewardServer(t, map[string]string{
		"liken-machine-operator": "0.2.2",
		"machine-logs":           "0.2.2",
	}, &evicted)

	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	refreshOSPods(&fleetReader{client: client}, machines, &overduePodReports{}, passTime)
	if len(evicted) != 2 || evicted[0] != "liken-machine-operator-pod" || evicted[1] != "machine-logs-pod" {
		t.Errorf("evicted pods = %v, want one from each DaemonSet", evicted)
	}
}

func TestRefreshOSPodsSkipsMissingDaemonSets(t *testing.T) {
	var evicted []string
	client := stewardServer(t, map[string]string{
		"liken-machine-operator": "0.2.2",
	}, &evicted)

	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	refreshOSPods(&fleetReader{client: client}, machines, &overduePodReports{}, passTime)
	if len(evicted) != 1 || evicted[0] != "liken-machine-operator-pod" {
		t.Errorf("evicted pods = %v, want only the machine-operator pod, because machine-logs does not exist", evicted)
	}
}

func TestDecideRefreshEvictsOnlyTheOldPodsOfUpgradedMachines(t *testing.T) {
	machines := []machine.Machine{
		machineRunning("node-1", "0.2.2"), // upgraded, old pod: evict
		machineRunning("node-2", "0.1.0"), // not upgraded: keep
		machineRunning("node-3", "0.2.2"), // upgraded, current pod: keep
	}
	pods := []kubernetes.Pod{
		operatorPod("op-1", "node-1", "0.1.0"),
		operatorPod("op-2", "node-2", "0.1.0"),
		operatorPod("op-3", "node-3", "0.2.2"),
	}
	refresh := decideRefresh("0.2.2", machines, pods)
	if got := refreshNames(refresh); len(got) != 1 || got[0] != "op-1" {
		t.Fatalf("refreshed pods = %v, want [op-1]", got)
	}
}
