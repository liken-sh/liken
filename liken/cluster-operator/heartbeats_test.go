package main

// These tests judge a machine's heartbeat through the Leases' watch, in
// a synctest bubble, so the machine's clock and this program's clock
// can differ by as much as a test needs.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
)

const leasesPath = "/apis/coordination.k8s.io/v1/namespaces/liken-system/leases"

// writeLease writes a Lease with the given renewTime, the way a
// machine's operator and this program's election renew theirs.
func writeLease(t *testing.T, client *apiclient.Client, method, name string, renewed time.Time) {
	t.Helper()
	path := leasesPath
	if method == http.MethodPut {
		path += "/" + name
	}
	body, _ := json.Marshal(fakeapi.Object("coordination.k8s.io/v1", "Lease", "liken-system", name,
		map[string]any{"spec": map[string]any{"renewTime": renewed.UTC().Format("2006-01-02T15:04:05.000000Z07:00")}}))
	if err := client.RequestJSON(method, path, body, nil); err != nil {
		t.Fatal(err)
	}
}

// waitLeading lets d pass on the bubble's clock while this program's
// election renews its own Lease every five seconds, so the copies keep
// answering (fleetReader.current).
func waitLeading(t *testing.T, client *apiclient.Client, d time.Duration) {
	t.Helper()
	for ; d > 5*time.Second; d -= 5 * time.Second {
		writeLease(t, client, http.MethodPut, leaseName, time.Now())
		time.Sleep(5 * time.Second)
	}
	writeLease(t, client, http.MethodPut, leaseName, time.Now())
	time.Sleep(d)
	synctest.Wait()
}

// renewFor lets d pass on the bubble's clock while each named machine
// renews its heartbeat Lease on the machine operator's schedule.
func renewFor(t *testing.T, client *apiclient.Client, d time.Duration, renewing []string) {
	t.Helper()
	for ; d > 0; d -= kubernetes.HeartbeatRenewAfter {
		for _, name := range renewing {
			writeLease(t, client, http.MethodPut, name, time.Now())
		}
		waitLeading(t, client, min(d, kubernetes.HeartbeatRenewAfter))
	}
}

// judgeNode1 answers node-1's phase as a sweep judges it now.
func judgeNode1(t *testing.T, r *fleetReader) api.Phase {
	t.Helper()
	heard, err := r.heartbeats(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m := labMachine("node-1", api.PhaseReady)
	return effectivePhase(&m, heard, time.Now())
}

// startWatching opens the fleet's watches on a fleet whose Leases were
// last renewed at renewed, and answers the reader and the client.
func startWatching(t *testing.T, renewed time.Time) (*fleetReader, *apiclient.Client) {
	t.Helper()
	fake := newFleetAPI(renewed)
	client, watcher := fleetClients(t, fake)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	wakes := make(chan struct{}, 1)
	r := watchFleet(ctx, watcher, client, watch.Signal(wakes), func(string) {})
	writeLease(t, client, http.MethodPut, leaseName, time.Now())
	awaitFleetCopies(t, r)
	return r, client
}

// This program measures a heartbeat's age on its own clock, from the
// moment it saw the lease's renewTime change, and never compares the
// machine's clock with its own. A lease it sees for the first time
// counts as seen at that moment.
func TestAHeartbeatAgesOnThisProgramsClock(t *testing.T) {
	cases := []struct {
		name string
		// behind is how far node-1's clock runs behind this program's.
		behind time.Duration
		// renewFor is how long node-1 renews its lease every 8 seconds
		// after the watches start.
		renewFor time.Duration
		// quiet is how long the lease then stays unchanged before the
		// sweep judges it.
		quiet time.Duration
		want  api.Phase
	}{
		{"a clock 45 seconds behind that renews every 8 seconds", 45 * time.Second, 2 * time.Minute, 0, api.PhaseReady},
		{"39 seconds after the last renewal", 0, 30 * time.Second, 39 * time.Second, api.PhaseReady},
		{"41 seconds after the last renewal", 0, 30 * time.Second, 41 * time.Second, api.PhaseLost},
		{"an hour-old lease 39 seconds after startup", time.Hour, 0, 39 * time.Second, api.PhaseReady},
		{"an hour-old lease 41 seconds after startup", time.Hour, 0, 41 * time.Second, api.PhaseLost},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r, client := startWatching(t, time.Now().Add(-c.behind))
				for renewed := time.Duration(0); renewed < c.renewFor; renewed += kubernetes.HeartbeatRenewAfter {
					waitLeading(t, client, kubernetes.HeartbeatRenewAfter)
					writeLease(t, client, http.MethodPut, "node-1", time.Now().Add(-c.behind))
				}
				waitLeading(t, client, c.quiet)

				if got := judgeNode1(t, r); got != c.want {
					t.Errorf("node-1 reads %s, want %s", got, c.want)
				}
			})
		})
	}
}

// A deleted lease leaves no record. A lease created again with the
// same renewTime counts as seen when it arrives, not when the deleted
// one last changed.
func TestADeletedLeaseIsForgotten(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		renewed := time.Now()
		r, client := startWatching(t, renewed)
		waitLeading(t, client, time.Minute)
		if err := client.RequestJSON(http.MethodDelete, leasesPath+"/node-1", nil, nil); err != nil {
			t.Fatal(err)
		}
		writeLease(t, client, http.MethodPost, "node-1", renewed)
		waitLeading(t, client, time.Second)

		if got := judgeNode1(t, r); got != api.PhaseReady {
			t.Errorf("node-1 reads %s, want %s", got, api.PhaseReady)
		}
	})
}
