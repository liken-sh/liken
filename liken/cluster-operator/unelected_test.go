package main

// These tests cover acting without an election: only a Lease that the
// API server refuses starts the mode, the gauge follows it, and the
// mode ends when the Lease answers.

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// A refusal of the Lease itself makes the copy act without an election
// and sets the gauge. Any other error keeps the election, and the copy
// does not act.
func TestOnlyARefusedLeaseMakesACopyActWithoutAnElection(t *testing.T) {
	cases := []struct {
		name   string
		method string
		status int
		acts   bool
	}{
		{"a create refused by RBAC", http.MethodPost, http.StatusForbidden, true},
		{"a read refused by RBAC", http.MethodGet, http.StatusForbidden, true},
		{"a create of a resource type that does not exist", http.MethodPost, http.StatusNotFound, true},
		{"a create that fails in the API server", http.MethodPost, http.StatusInternalServerError, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := newLeaseServer()
			server.setRefusal(c.method, c.status)
			copy := newCandidate(t, server, "liken-cluster-operator-a")

			acts := awaitWithin(copy, 3*testLeaseTiming.duration)

			if acts != c.acts || copy.gauge.Load() != c.acts || (copy.mayWrite() == nil) != c.acts {
				t.Errorf("acts = %v, gauge = %v, write allowed = %v; want all %v",
					acts, copy.gauge.Load(), copy.mayWrite() == nil, c.acts)
			}
		})
	}
}

// A Lease that answers again ends the mode: the copy takes the Lease on
// its next try, leads, and clears the gauge.
func TestALeaseThatAnswersAgainIsTaken(t *testing.T) {
	server := newLeaseServer()
	server.setRefusal(http.MethodPost, http.StatusForbidden)
	copy := newCandidate(t, server, "liken-cluster-operator-a")
	if !awaitWithin(copy, 3*testLeaseTiming.duration) || !copy.gauge.Load() {
		t.Fatal("the copy never acted without an election")
	}

	server.clearRefusal(http.MethodPost)

	select {
	case <-copy.started:
	case <-time.After(3 * testLeaseTiming.duration):
		t.Fatal("the copy never took the Lease once it answered")
	}
	// OnStartedLeading starts the lead before it ends the mode, so
	// mayWrite allows a write at each moment between the two. The test
	// waits out that moment.
	deadline := time.Now().Add(testLeaseTiming.duration)
	for (copy.gauge.Load() || copy.unelected.Load()) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if copy.gauge.Load() || copy.unelected.Load() || server.holder() != copy.identity {
		t.Errorf("gauge = %v, unelected = %v, holder = %q; want the mode over and the Lease held",
			copy.gauge.Load(), copy.unelected.Load(), server.holder())
	}
}

// A copy that acts without an election and then finds another holder
// stops acting at its next try, and waits.
func TestACopyWithoutAnElectionStopsWhenAnotherCopyLeads(t *testing.T) {
	server := newLeaseServer()
	server.setRefusal(http.MethodPost, http.StatusForbidden)
	copy := newCandidate(t, server, "liken-cluster-operator-a")
	if !awaitWithin(copy, 3*testLeaseTiming.duration) {
		t.Fatal("the copy never acted without an election")
	}

	server.holdAs("liken-cluster-operator-b")

	deadline := time.Now().Add(3 * testLeaseTiming.duration)
	for copy.acting() && time.Now().Before(deadline) {
		time.Sleep(testLeaseTiming.retryPeriod / 4)
	}
	stop, cancel := context.WithTimeout(t.Context(), testLeaseTiming.retryPeriod)
	defer cancel()
	if copy.acting() || copy.mayAct(stop) || copy.gauge.Load() || copy.mayWrite() == nil {
		t.Error("the copy kept acting after another copy took the Lease")
	}
}

// A leader whose renewal gets one 403 enters the mode, and its next
// successful renewal ends it, so the write guard's age check returns.
func TestARenewalThatAnswersAgainEndsTheMode(t *testing.T) {
	server := newLeaseServer()
	leader := newCandidate(t, server, "liken-cluster-operator-a")
	if !awaitWithin(leader, 3*testLeaseTiming.duration) {
		t.Fatal("the copy never took the Lease")
	}
	server.setRefusal(http.MethodPut, http.StatusForbidden)
	deadline := time.Now().Add(3 * testLeaseTiming.duration)
	for !leader.unelected.Load() && time.Now().Before(deadline) {
		time.Sleep(testLeaseTiming.retryPeriod / 4)
	}
	if !leader.unelected.Load() {
		t.Fatal("a refused renewal did not enter the mode")
	}

	server.clearRefusal(http.MethodPut)

	deadline = time.Now().Add(3 * testLeaseTiming.duration)
	for leader.unelected.Load() && time.Now().Before(deadline) {
		time.Sleep(testLeaseTiming.retryPeriod / 4)
	}
	if leader.unelected.Load() || leader.gauge.Load() || leader.exited() {
		t.Errorf("unelected = %v, gauge = %v, exited = %v; want the mode over and the lead kept",
			leader.unelected.Load(), leader.gauge.Load(), leader.exited())
	}
}
