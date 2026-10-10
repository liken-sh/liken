package main

// The rollout lets a leader go first while the machine-operator
// DaemonSet's template lags the fleet's target, because only a
// leader's boot advances the template. A sweep that cannot read the
// DaemonSet does not know whether the template lags, so it grants no
// turn: a worker granted into the lag boots onto a template older
// than its release.

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

// refusingDaemonSets answers every request for a DaemonSet with a
// server error, so the reader has no copy of the DaemonSets and each
// read of one fails.
type refusingDaemonSets struct{ next http.Handler }

func (h refusingDaemonSets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, daemonSetsPath) {
		http.Error(w, `{"kind":"Status","code":500}`, http.StatusInternalServerError)
		return
	}
	h.next.ServeHTTP(w, r)
}

// targetRelease sets the fleet's target release on the Cluster.
func targetRelease(t *testing.T, client *apiclient.Client, version string) {
	t.Helper()
	doc, err := apiclient.Get[cluster.Cluster](client, clusterPath("lab"))
	if err != nil {
		t.Fatal(err)
	}
	doc.Spec.Version = version
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RequestJSON(http.MethodPut, clusterPath("lab"), body, nil); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
}

func TestASweepThatCannotReadTheTemplateGrantsNoTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := newFleetAPI(time.Now().Add(-time.Hour))
		client, _ := fleetClients(t, fake)
		reads, watcher := fleetClients(t, refusingDaemonSets{next: fake})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := watchFleet(ctx, watcher, reads, watch.Signal(make(chan struct{}, 1)), func(string) {})
		writeLease(t, client, http.MethodPut, leaseName, time.Now())
		synctest.Wait()
		// The applied template carries 2026.09.27-001.
		targetRelease(t, client, "2026.10.01-001")
		awaitTurnOf(t, client, "node-1")
		awaitTurnOf(t, client, "node-2")
		sweepFresh(t, r, client)
		renewFor(t, client, kubernetes.HeartbeatStaleAfter+time.Second, []string{"node-1", "node-2"})

		_, worker, _ := sweepFresh(t, r, client)

		if granted(worker) {
			t.Error("node-2 holds a reboot turn though the sweep could not read whether the template lags")
		}
	})
}
