package election_test

// These tests cover acting without an election: only a Lease that the
// API server refuses starts the mode, and only in an election that asks
// for it. Options.Unelected follows the mode, and the mode ends when
// the Lease answers.

import (
	"context"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/election"
)

// A refusal of the Lease itself makes the copy act without an election,
// when the election asks for that. Any other error keeps the election,
// and the copy does not act.
func TestOnlyARefusedLeaseMakesACopyActWithoutAnElection(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		status  int
		refused bool
		acts    bool
	}{
		{"a create refused by RBAC", http.MethodPost, http.StatusForbidden, true, true},
		{"a read refused by RBAC", http.MethodGet, http.StatusForbidden, true, true},
		{"a create of a resource type that does not exist", http.MethodPost, http.StatusNotFound, true, true},
		{"a create that fails in the API server", http.MethodPost, http.StatusInternalServerError, true, false},
		{"a refusal in an election that does not act on one", http.MethodPost, http.StatusForbidden, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newServer()
				server.Refuse(c.method, c.status)
				copy := newCandidateActing(t, server, "an-operator-a", c.refused)

				acts := awaitWithin(copy, election.Duration)

				if acts != c.acts || copy.unelected.Load() != c.acts || (copy.MayWrite() == nil) != c.acts {
					t.Errorf("acts = %v, unelected = %v, write allowed = %v; want all %v",
						acts, copy.unelected.Load(), copy.MayWrite() == nil, c.acts)
				}
			})
		})
	}
}

// A Lease that answers again ends the mode: the copy takes the Lease on
// its next try, leads, and reports the mode over.
func TestALeaseThatAnswersAgainIsTaken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		server.Refuse(http.MethodPost, http.StatusForbidden)
		copy := newCandidateActing(t, server, "an-operator-a", true)
		if !awaitWithin(copy, election.Duration) || !copy.unelected.Load() {
			t.Fatal("the copy never acted without an election")
		}

		server.Allow(http.MethodPost)

		select {
		case <-copy.Started():
		case <-time.After(nextTry):
			t.Fatal("the copy did not take the Lease on its next try once it answered")
		}
		// OnStartedLeading starts the lead before it ends the mode, so
		// MayWrite allows a write at each moment between the two. The
		// test waits for the callback to return.
		synctest.Wait()
		if copy.unelected.Load() || copy.Unelected() || server.Holder() != copy.Identity() {
			t.Errorf("reported = %v, unelected = %v, holder = %q; want the mode over and the Lease held",
				copy.unelected.Load(), copy.Unelected(), server.Holder())
		}
	})
}

// A copy that acts without an election and then finds another holder
// stops acting at its next try, and waits.
func TestACopyWithoutAnElectionStopsWhenAnotherCopyLeads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		server.Refuse(http.MethodPost, http.StatusForbidden)
		copy := newCandidateActing(t, server, "an-operator-a", true)
		if !awaitWithin(copy, election.Duration) {
			t.Fatal("the copy never acted without an election")
		}

		server.HoldAs("an-operator-b")

		time.Sleep(nextTry)
		synctest.Wait()
		stop, cancel := context.WithTimeout(t.Context(), election.RetryPeriod)
		defer cancel()
		if copy.Acting() || copy.MayAct(stop) || copy.unelected.Load() || copy.MayWrite() == nil {
			t.Error("the copy kept acting after its next try found another copy that holds the Lease")
		}
	})
}

// A leader whose renewal gets one 403 enters the mode, and its next
// successful renewal ends it, so the write guard's age check returns.
func TestARenewalThatAnswersAgainEndsTheMode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		leader := newCandidateActing(t, server, "an-operator-a", true)
		if !leadsAtOnce(leader) {
			t.Fatal("the copy did not take a Lease that nobody holds")
		}
		server.Refuse(http.MethodPut, http.StatusForbidden)
		time.Sleep(election.RetryPeriod)
		synctest.Wait()
		if !leader.Unelected() {
			t.Fatal("the refused renewal one retry period later did not enter the mode")
		}

		server.Allow(http.MethodPut)

		time.Sleep(election.RetryPeriod)
		synctest.Wait()
		if leader.Unelected() || leader.unelected.Load() || leader.exited() {
			t.Errorf("unelected = %v, reported = %v, exited = %v; want the mode over and the lead kept",
				leader.Unelected(), leader.unelected.Load(), leader.exited())
		}
	})
}
