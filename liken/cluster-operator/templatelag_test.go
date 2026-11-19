package main

// While the machine-operator DaemonSet's template is older than the
// fleet's target release, the rollout grants turns to leaders first,
// because only a leader's boot updates the template. When a pass
// cannot read the DaemonSet, it cannot tell whether the template is
// older, so it grants no turn. A worker granted a turn then would boot
// onto a template older than its release.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
)

// refusingDaemonSets fails every DaemonSet request with a 500, so the
// DaemonSet watch never fills and every read of a DaemonSet fails.
type refusingDaemonSets struct{ next http.Handler }

func (h refusingDaemonSets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, daemonSetsPath) {
		http.Error(w, `{"kind":"Status","code":500}`, http.StatusInternalServerError)
		return
	}
	h.next.ServeHTTP(w, r)
}

// targetRelease sets spec.version, the target release, on the Cluster.
func targetRelease(t *testing.T, client *apiclient.Client, version string) {
	t.Helper()
	doc, err := apiclient.Get[cluster.Cluster](client, clusterPath("lab"))
	if err != nil {
		t.Fatalf("reading the Cluster: %v", err)
	}
	doc.Spec.Version = version
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encoding the Cluster: %v", err)
	}
	if err := client.RequestJSON(http.MethodPut, clusterPath("lab"), body, nil); err != nil {
		t.Fatalf("writing the Cluster: %v", err)
	}
	synctest.Wait()
}

func TestAPassThatCannotReadTheTemplateGrantsNoTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFleetAPI(time.Now().Add(-time.Hour))
		client, _ := fleetClients(t, fake)
		reads, watcher := fleetClients(t, refusingDaemonSets{next: fake})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := watchFleet(ctx, watcher, reads, watch.Signal(make(chan struct{}, 1)), func(string) {})
		writeLease(t, client, http.MethodPut, leaseName, time.Now())
		synctest.Wait()
		// The DaemonSet's applied template is for 2026.09.27-001.
		targetRelease(t, client, "2026.10.01-001")
		awaitTurnOf(t, client, "node-1")
		awaitTurnOf(t, client, "node-2")
		reconcileAndRead(t, r, client)
		renewFor(t, client, kubernetes.HeartbeatStaleAfter+time.Second, []string{"node-1", "node-2"})

		_, worker, _ := reconcileAndRead(t, r, client)

		if granted(worker) {
			t.Error("node-2 holds a reboot turn, want none because the pass could not read whether the template lags")
		}
	})
}
