package election_test

// These tests cover a Lease that the API server refuses. A refusal is
// an ordinary failure of the election: a copy that waits keeps
// waiting, and a leader whose renewals are refused stops writing and
// exits on the same schedule as for any other failed renewal.

import (
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/election"
)

// A 403 on any request of the Lease, or a 404 on its create, leaves a
// copy waiting. Even one such answer beside a healthy leader must not
// let the copy act, because two copies that act at once can each grant
// a reboot turn from their own view of the fleet.
func TestARefusedLeaseMakesACopyWait(t *testing.T) {
	cases := []struct {
		name   string
		holder string
		method string
		status int
	}{
		{"a read refused by RBAC beside a leader", "an-operator-b_0badc0de", http.MethodGet, http.StatusForbidden},
		{"a read refused by RBAC with no Lease", "", http.MethodGet, http.StatusForbidden},
		{"a create refused by RBAC", "", http.MethodPost, http.StatusForbidden},
		{"a create of a resource type that does not exist", "", http.MethodPost, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newServer()
				if c.holder != "" {
					server.HoldAs(c.holder)
				}
				server.Refuse(c.method, c.status)
				copy := newCandidate(t, server, "an-operator-a")

				synctest.Wait()
				if err := copy.MayWrite(); err == nil || copy.Leads() {
					t.Fatalf("after one refused try, leads = %v and MayWrite = %v; want the copy waiting", copy.Leads(), err)
				}
				if awaitWithin(copy, election.Duration) {
					t.Error("a copy whose Lease requests are refused acted")
				}
			})
		})
	}
}

// A leader whose renewals the API server refuses with 403 stops
// writing one renewal deadline after its last renewal, and exits with
// 1 before a waiting copy can take the Lease, the same as for a
// renewal that fails in the API server.
func TestALeaderWhoseRenewalsAreRefusedStopsWritingAndExits(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{"refused by RBAC", http.StatusForbidden},
		{"failed in the API server", http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newServer()
				leader := newCandidate(t, server, "an-operator-a")
				if !leadsAtOnce(leader) {
					t.Fatal("the copy did not take a Lease that nobody holds")
				}
				renewed := leader.LastRenewal()

				server.Refuse(http.MethodPut, c.status)

				time.Sleep(election.RenewDeadline)
				synctest.Wait()
				if err := leader.MayWrite(); err == nil || leader.exited() {
					t.Errorf("one renewal deadline after the last renewal, MayWrite = %v and exited = %v; want writes refused before the exit",
						err, leader.exited())
				}
				select {
				case <-leader.exits:
				case <-time.After(election.Duration):
					t.Fatal("the leader kept acting with no renewal")
				}
				if exit, limit := time.Since(renewed), election.Duration-election.RetryPeriod; exit > limit {
					t.Errorf("the leader exited %s after its last renewal, want at most %s", exit, limit)
				}
				if err := leader.MayWrite(); err == nil {
					t.Error("a leader that exited still allows writes")
				}
				if code := leader.exitCode.Load(); code != 1 {
					t.Errorf("exit code = %d, want 1", code)
				}
			})
		})
	}
}
