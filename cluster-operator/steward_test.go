package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/chrisguidry/liken/kubernetes"
	"github.com/chrisguidry/liken/machine"
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

func TestStewardRefreshesStalePodOnUpgradedMachine(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "0.1.0")}
	refresh := decideRefresh("0.2.2", machines, pods)
	if got := refreshNames(refresh); len(got) != 1 || got[0] != "op-1" {
		t.Fatalf("expected [op-1], got %v", got)
	}
}

func TestStewardRefreshesPodWithNoVersionAnnotation(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "")}
	refresh := decideRefresh("0.2.2", machines, pods)
	if len(refresh) != 1 {
		t.Fatalf("expected one refresh, got %v", refreshNames(refresh))
	}
}

func TestStewardLeavesFreshPodsAlone(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "0.2.2")}
	if refresh := decideRefresh("0.2.2", machines, pods); len(refresh) != 0 {
		t.Fatalf("expected no refreshes, got %v", refreshNames(refresh))
	}
}

func TestStewardWaitsForTheMachineToUpgrade(t *testing.T) {
	// The machine still runs 0.1.0: its old pod is the only operator
	// image the machine has, and evicting it would leave the machine
	// with no operator to drive its own upgrade.
	machines := []machine.Machine{machineRunning("node-1", "0.1.0")}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "0.1.0")}
	if refresh := decideRefresh("0.2.2", machines, pods); len(refresh) != 0 {
		t.Fatalf("expected no refreshes, got %v", refreshNames(refresh))
	}
}

func TestStewardWaitsForTheManifestsToCatchUp(t *testing.T) {
	// The machine is ahead of the applied manifests (workers upgrade
	// before any leader has applied the new release's DaemonSet): a
	// refresh now would just recreate another stale pod, thrashing
	// every sweep until the leaders catch up.
	machines := []machine.Machine{machineRunning("node-1", "0.2.3")}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "0.2.2")}
	if refresh := decideRefresh("0.2.2", machines, pods); len(refresh) != 0 {
		t.Fatalf("expected no refreshes, got %v", refreshNames(refresh))
	}
}

func TestStewardDoesNothingWithoutAVersionedDaemonSet(t *testing.T) {
	// A DaemonSet with no os-version annotation predates this design;
	// there is no authority to refresh toward.
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "0.1.0")}
	if refresh := decideRefresh("", machines, pods); len(refresh) != 0 {
		t.Fatalf("expected no refreshes, got %v", refreshNames(refresh))
	}
}

func TestStewardIgnoresPodsOnUnknownMachines(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []kubernetes.Pod{operatorPod("op-x", "node-9", "0.1.0")}
	if refresh := decideRefresh("0.2.2", machines, pods); len(refresh) != 0 {
		t.Fatalf("expected no refreshes, got %v", refreshNames(refresh))
	}
}

// stewardServer fakes the API's steward-facing corner: each named
// DaemonSet exists with the given os-version annotation, every
// DaemonSet's listing returns one stale pod on node-1, and evictions
// are recorded. Lookups of DaemonSets not in the map 404.
func stewardServer(t *testing.T, daemonSets map[string]string, evicted *[]string) *kubernetes.Client {
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
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// The acting half sweeps every OS DaemonSet, the operator's and the
// relays', refreshing each one's stale pods.
func TestStewardSweepsEveryOSDaemonSet(t *testing.T) {
	var evicted []string
	client := stewardServer(t, map[string]string{
		"liken-machine-operator": "0.2.2",
		"machine-logs":           "0.2.2",
	}, &evicted)

	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	stewardOSPods(client, machines)
	if len(evicted) != 2 || evicted[0] != "liken-machine-operator-pod" || evicted[1] != "machine-logs-pod" {
		t.Errorf("expected evictions from both DaemonSets, got %v", evicted)
	}
}

// A DaemonSet that doesn't exist (a partial rollout, a cluster from
// before the relays) is skipped without complaint or eviction.
func TestStewardSkipsMissingDaemonSets(t *testing.T) {
	var evicted []string
	client := stewardServer(t, map[string]string{
		"liken-machine-operator": "0.2.2",
	}, &evicted)

	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	stewardOSPods(client, machines)
	if len(evicted) != 1 || evicted[0] != "liken-machine-operator-pod" {
		t.Errorf("expected only the operator's eviction, got %v", evicted)
	}
}

func TestStewardHandlesTheWholeFleetMidRollout(t *testing.T) {
	machines := []machine.Machine{
		machineRunning("node-1", "0.2.2"), // upgraded, stale pod: refresh
		machineRunning("node-2", "0.1.0"), // not yet upgraded: leave it
		machineRunning("node-3", "0.2.2"), // upgraded, fresh pod: leave it
	}
	pods := []kubernetes.Pod{
		operatorPod("op-1", "node-1", "0.1.0"),
		operatorPod("op-2", "node-2", "0.1.0"),
		operatorPod("op-3", "node-3", "0.2.2"),
	}
	refresh := decideRefresh("0.2.2", machines, pods)
	if got := refreshNames(refresh); len(got) != 1 || got[0] != "op-1" {
		t.Fatalf("expected [op-1], got %v", got)
	}
}
