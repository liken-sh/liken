package main

// These tests cover this program's use of kubernetes/election: the
// Lease it competes for, the prefix on the election's log lines, the
// gauge of the mode without an election, and the sweep of a copy that
// leads. The package's own tests cover the election itself.

import (
	"context"
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

// A copy takes the Lease liken-cluster-operator in liken-system under
// its pod's name, and logs with this program's prefix. A copy that the
// API server refuses on the Lease acts without an election, and the
// gauge says so.
func TestThisProgramElectsOnItsOwnLease(t *testing.T) {
	cases := []struct {
		name      string
		refuse    int
		held      bool
		line      string
		unelected string
	}{
		{
			name:      "a Lease that nobody holds",
			held:      true,
			line:      "liken-cluster-operator: holding Lease liken-system/liken-cluster-operator as liken-cluster-operator-a_",
			unelected: "liken_cluster_operator_unelected 0",
		},
		{
			name:      "a Lease that RBAC refuses",
			refuse:    http.StatusForbidden,
			held:      false,
			line:      "liken-cluster-operator: the API server refuses Lease liken-system/liken-cluster-operator; acting without an election until it answers",
			unelected: "liken_cluster_operator_unelected 1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := metrics.NewOperator(component, machine.Version, []string{clusterKind}, []string{machineKind})
			gauge := newUnelectedGauge(o)
			gauge(false)
			var mu sync.Mutex
			var lines []string
			synctest.Test(t, func(t *testing.T) {
				server := electiontest.New("liken-cluster-operator", "liken-system")
				if c.refuse != 0 {
					server.Refuse(http.MethodPost, c.refuse)
				}
				options := electionOptions("liken-cluster-operator-a", gauge, func(line string) {
					mu.Lock()
					defer mu.Unlock()
					lines = append(lines, line)
				})
				options.Exit = func(code int) { t.Errorf("the election exited with %d", code) }

				stop, cancel := context.WithTimeout(t.Context(), election.Duration)
				defer cancel()
				leader, err := election.Start(stop, server.Config(t), options)
				if err != nil {
					t.Fatal(err)
				}
				if held := server.Holder() == leader.Identity(); held != c.held {
					t.Errorf("holder = %q and identity = %q, want held %v", server.Holder(), leader.Identity(), c.held)
				}
				if !strings.HasPrefix(leader.Identity(), "liken-cluster-operator-a_") {
					t.Errorf("identity = %q, want the pod's name first", leader.Identity())
				}
				leader.StepDown()
			})

			mu.Lock()
			defer mu.Unlock()
			if !logged(lines, c.line) {
				t.Errorf("the election logged %q, want a line that starts with %q", lines, c.line)
			}
			requireSeries(t, scrapeHandler(t, o.Handler()), c.unelected)
		})
	}
}

func logged(lines []string, prefix string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// A copy that holds the Lease sweeps through the guarded client, so its
// writes reach the API server, and at a shutdown it finishes the sweep
// in flight, writes the Events the sweep posted, and then releases the
// Lease for the copy that waits.
func TestALeaderSweepsAndReleasesTheLeaseAtAShutdown(t *testing.T) {
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
		options := electionOptions("liken-cluster-operator-a", newUnelectedGauge(o), func(string) {})
		leader, err := election.Start(stop, lease.Config(t), options)
		if err != nil {
			t.Fatal(err)
		}
		watches, cancel := context.WithCancel(t.Context())
		defer cancel()

		run(stop, watches, leader, client, watcher, make(chan time.Time), o, newClusterMetrics(o))

		if got := fleetAPI.ResourceVersion(kubernetes.ClustersPath, "lab"); got == "5" {
			t.Error("the leader's sweep did not publish the Cluster's status")
		}
		mu.Lock()
		defer mu.Unlock()
		if len(eventHolders) == 0 {
			t.Error("the leader's sweep wrote no Event")
		}
		for i, holder := range eventHolders {
			if holder != leader.Identity() {
				t.Errorf("Event %d of %d landed with the Lease held by %q, want %q", i+1, len(eventHolders), holder, leader.Identity())
			}
		}
		if lease.Holder() != "" {
			t.Errorf("holder after the shutdown = %q, want the Lease released", lease.Holder())
		}
	})
}
