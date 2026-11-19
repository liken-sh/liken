package main

// These tests cover how this program uses kubernetes/election: which
// Lease it competes for, the prefix on the election's log lines, a copy
// that the API server refuses on the Lease, and the pass of a copy
// that leads. The election's own tests are in its package.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/election/electiontest"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
)

func TestACopyTakesTheLeaseUnderItsPodName(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	synctest.Test(t, func(t *testing.T) {
		server := electiontest.New("liken-cluster-operator", "liken-system")
		options := electionOptions("liken-cluster-operator-a", func(line string) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, line)
		})
		options.Exit = func(code int) { t.Errorf("the election exited with status %d, want it to keep running", code) }

		leader, err := election.Start(t.Context(), server.Config(t), options)
		if err != nil {
			t.Fatalf("starting the election: %v", err)
		}
		if server.Holder() != leader.Identity() || !strings.HasPrefix(leader.Identity(), "liken-cluster-operator-a_") {
			t.Errorf("holder = %q and identity = %q, want the Lease held under the pod's name", server.Holder(), leader.Identity())
		}
		leader.StepDown()
	})

	mu.Lock()
	defer mu.Unlock()
	if want := "liken-cluster-operator: holding Lease liken-system/liken-cluster-operator as liken-cluster-operator-a_"; !logged(lines, want) {
		t.Errorf("the election logged %q, want a line that starts with %q", lines, want)
	}
}

// A copy that the API server refuses on the Lease waits, as for any
// other election failure. A refusal does not mean that the release
// lacks the RBAC, because the Cluster CRD lets a rollback reach only
// releases that grant this program its Lease.
func TestACopyRefusedOnTheLeaseWaitsAndNeverActs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := electiontest.New("liken-cluster-operator", "liken-system")
		server.Refuse(http.MethodPost, http.StatusForbidden)
		stop, cancel := context.WithTimeout(t.Context(), election.Duration)
		defer cancel()

		leader, err := election.Start(stop, server.Config(t), electionOptions("liken-cluster-operator-a", func(string) {}))

		if !errors.Is(err, election.ErrStopped) || leader != nil {
			t.Errorf("Start = %v, %v, want ErrStopped and no lead", leader, err)
		}
	})
}

func logged(lines []string, prefix string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// A copy that holds the Lease passes and its writes reach the API
// server. At shutdown it finishes the pass in flight, sends the Events
// that pass posted while it still holds the Lease, and then releases
// the Lease.
func TestALeaderReconcilesAndReleasesTheLeaseAtShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lease := electiontest.New(leaseName, leaseNamespace)
		stop, shutdown := context.WithCancel(t.Context())
		defer shutdown()
		fleetAPI := newFleetAPI(time.Now())
		recorded := &eventstest.Events{}
		var mu sync.Mutex
		var eventHolders []string
		client, watcher := fleetClients(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/events") {
				mu.Lock()
				eventHolders = append(eventHolders, lease.Holder())
				mu.Unlock()
				recorded.ServeHTTP(w, r)
				return
			}
			fleetAPI.ServeHTTP(w, r)
			if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/clusters/lab/status") {
				shutdown()
			}
		}))
		o := metrics.NewOperator(component, machine.Version, []string{clusterKind}, watchKinds)
		options := electionOptions("liken-cluster-operator-a", func(string) {})
		leader, err := election.Start(stop, lease.Config(t), options)
		if err != nil {
			t.Fatalf("starting the election: %v", err)
		}
		watches, cancel := context.WithCancel(t.Context())
		defer cancel()

		run(stop, watches, leader, client, watcher, make(chan time.Time), o, newClusterMetrics(o))

		if got := fleetAPI.ResourceVersion(kubernetes.ClustersPath, "lab"); got == "5" {
			t.Error("the leader's pass did not publish the Cluster's status, want one write")
		}
		mu.Lock()
		defer mu.Unlock()
		if len(eventHolders) == 0 {
			t.Error("the leader's pass wrote no Event, want at least one")
		}
		for i, holder := range eventHolders {
			if holder != leader.Identity() {
				t.Errorf("Event %d of %d was sent while the Lease was held by %q, want %q", i+1, len(eventHolders), holder, leader.Identity())
			}
		}
		if lease.Holder() != "" {
			t.Errorf("holder after the shutdown = %q, want the Lease released", lease.Holder())
		}
	})
}
