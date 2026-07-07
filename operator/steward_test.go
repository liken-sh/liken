package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

func machineRunning(name, version string) machine.Machine {
	m := machine.Machine{}
	m.Metadata.Name = name
	m.Status.Version.Liken = version
	return m
}

func operatorPod(name, node, osVersion string) podObject {
	p := podObject{}
	p.Metadata.Name = name
	p.Metadata.Namespace = "liken-system"
	if osVersion != "" {
		p.Metadata.Annotations = map[string]string{osVersionAnnotation: osVersion}
	}
	p.Spec.NodeName = node
	return p
}

func refreshNames(pods []podObject) []string {
	names := make([]string, 0, len(pods))
	for _, p := range pods {
		names = append(names, p.Metadata.Name)
	}
	return names
}

func TestStewardRefreshesStalePodOnUpgradedMachine(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []podObject{operatorPod("op-1", "node-1", "0.1.0")}
	refresh := decideRefresh("0.2.2", machines, pods)
	if got := refreshNames(refresh); len(got) != 1 || got[0] != "op-1" {
		t.Fatalf("expected [op-1], got %v", got)
	}
}

func TestStewardRefreshesPodWithNoVersionAnnotation(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []podObject{operatorPod("op-1", "node-1", "")}
	refresh := decideRefresh("0.2.2", machines, pods)
	if len(refresh) != 1 {
		t.Fatalf("expected one refresh, got %v", refreshNames(refresh))
	}
}

func TestStewardLeavesFreshPodsAlone(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []podObject{operatorPod("op-1", "node-1", "0.2.2")}
	if refresh := decideRefresh("0.2.2", machines, pods); len(refresh) != 0 {
		t.Fatalf("expected no refreshes, got %v", refreshNames(refresh))
	}
}

func TestStewardWaitsForTheMachineToUpgrade(t *testing.T) {
	// The machine still runs 0.1.0: its old pod is the only operator
	// image the machine has, and evicting it would leave the machine
	// with no operator to drive its own upgrade.
	machines := []machine.Machine{machineRunning("node-1", "0.1.0")}
	pods := []podObject{operatorPod("op-1", "node-1", "0.1.0")}
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
	pods := []podObject{operatorPod("op-1", "node-1", "0.2.2")}
	if refresh := decideRefresh("0.2.2", machines, pods); len(refresh) != 0 {
		t.Fatalf("expected no refreshes, got %v", refreshNames(refresh))
	}
}

func TestStewardDoesNothingWithoutAVersionedDaemonSet(t *testing.T) {
	// A DaemonSet with no os-version annotation predates this design;
	// there is no authority to refresh toward.
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []podObject{operatorPod("op-1", "node-1", "0.1.0")}
	if refresh := decideRefresh("", machines, pods); len(refresh) != 0 {
		t.Fatalf("expected no refreshes, got %v", refreshNames(refresh))
	}
}

func TestStewardIgnoresPodsOnUnknownMachines(t *testing.T) {
	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	pods := []podObject{operatorPod("op-x", "node-9", "0.1.0")}
	if refresh := decideRefresh("0.2.2", machines, pods); len(refresh) != 0 {
		t.Fatalf("expected no refreshes, got %v", refreshNames(refresh))
	}
}

// The acting half sweeps every OS DaemonSet, not just the
// operator's, and a DaemonSet that doesn't exist (a partial rollout,
// an old cluster) is skipped without complaint or eviction.
func TestStewardSweepsEveryOSDaemonSet(t *testing.T) {
	var evicted []string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/eviction"):
			parts := strings.Split(r.URL.Path, "/")
			evicted = append(evicted, parts[len(parts)-2])
			w.WriteHeader(http.StatusCreated)
		case strings.Contains(r.URL.Path, "/daemonsets/"):
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			// Only the operator's and kernel-logs' DaemonSets exist;
			// the other lookups 404 and the steward moves on.
			if name != "liken-operator" && name != "kernel-logs" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]string{osVersionAnnotation: "0.2.2"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/pods"):
			app := r.URL.Query().Get("labelSelector")
			pod := operatorPod(strings.TrimPrefix(app, "app=")+"-pod", "node-1", "0.1.0")
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []podObject{pod}})
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	machines := []machine.Machine{machineRunning("node-1", "0.2.2")}
	stewardOSPods(client, machines)
	if len(evicted) != 2 {
		t.Fatalf("expected evictions from both existing DaemonSets, got %v", evicted)
	}
	if evicted[0] != "liken-operator-pod" || evicted[1] != "kernel-logs-pod" {
		t.Errorf("evicted: %v", evicted)
	}
}

func TestStewardHandlesTheWholeFleetMidRollout(t *testing.T) {
	machines := []machine.Machine{
		machineRunning("node-1", "0.2.2"), // upgraded, stale pod: refresh
		machineRunning("node-2", "0.1.0"), // not yet upgraded: leave it
		machineRunning("node-3", "0.2.2"), // upgraded, fresh pod: leave it
	}
	pods := []podObject{
		operatorPod("op-1", "node-1", "0.1.0"),
		operatorPod("op-2", "node-2", "0.1.0"),
		operatorPod("op-3", "node-3", "0.2.2"),
	}
	refresh := decideRefresh("0.2.2", machines, pods)
	if got := refreshNames(refresh); len(got) != 1 || got[0] != "op-1" {
		t.Fatalf("expected [op-1], got %v", got)
	}
}
