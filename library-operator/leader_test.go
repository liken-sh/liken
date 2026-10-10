//go:build !pod

package main

// These tests cover what this operator adds to the shared election: the
// Lease's name, the prefix on the election's log lines, and the order
// of a shutdown around the bus session. The election itself has its
// tests in kubernetes/election. These run against the fake Lease server
// of electiontest, in a synctest bubble, so a wait of a Lease's
// duration takes no real time.

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/election/electiontest"
)

func newLeaseServer() *electiontest.Server {
	return electiontest.New(leaseName, testOperatorNamespace)
}

// candidate is one operator process under test, with the exit code it
// would have ended with, or -1, and the lines it reported.
type candidate struct {
	*leadership
	exitCode atomic.Int32

	lines    sync.Mutex
	reported []string
}

func (c *candidate) exited() bool { return c.exitCode.Load() >= 0 }

func (c *candidate) lineLog() []string {
	c.lines.Lock()
	defer c.lines.Unlock()
	return slices.Clone(c.reported)
}

func newCandidate(t *testing.T, server *electiontest.Server, pod string) *candidate {
	t.Helper()
	c := &candidate{}
	c.exitCode.Store(-1)
	report := func(line string) {
		c.lines.Lock()
		defer c.lines.Unlock()
		c.reported = append(c.reported, line)
	}
	l, err := newLeadership(server.Config(t), testOperatorNamespace, pod,
		func(code int) { c.exitCode.CompareAndSwap(-1, int32(code)) }, report)
	if err != nil {
		t.Fatal(err)
	}
	c.leadership = l
	c.Run()
	// The step down ends the election, so no goroutine of client-go
	// outlives the bubble. The lines print when the test ends, because
	// client-go can call back after the test's last assertion.
	t.Cleanup(func() {
		c.StepDown()
		for _, line := range c.lineLog() {
			t.Log(line)
		}
	})
	return c
}

// holdingCandidate is a candidate that has taken the Lease.
func holdingCandidate(t *testing.T, server *electiontest.Server, pod string) *candidate {
	t.Helper()
	c := newCandidate(t, server, pod)
	stop, cancel := context.WithTimeout(context.Background(), 3*election.Duration)
	defer cancel()
	if !c.await(stop) {
		t.Fatalf("%s never took the Lease", pod)
	}
	return c
}

// The operator's copies compete for the Lease named for the operator,
// and every line the election logs carries the operator's prefix, the
// way the operator's own lines do.
func TestTheElectionNamesTheOperatorsLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := holdingCandidate(t, server, "library-operator-a")

		want := "library.liken.sh: holding Lease " + testOperatorNamespace + "/library-operator as " + leader.Identity()
		if lines := leader.lineLog(); !slices.Contains(lines, want) {
			t.Errorf("lines = %q, want %q", lines, want)
		}
		if !strings.HasPrefix(server.Holder(), "library-operator-a_") {
			t.Errorf("holder = %q, want this pod", server.Holder())
		}
	})
}

// A shutdown stops the bus session while the Lease still names this
// process, and releases the Lease after, so the next leader's session
// under the same client id never meets this one. A shutdown is not a
// loss.
func TestAStepDownQuietsTheBusBeforeItReleasesTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := holdingCandidate(t, server, "library-operator-a")
		holderWhileQuieting := ""

		leader.stepDown(func() bool {
			holderWhileQuieting = server.Holder()
			return true
		})

		if holderWhileQuieting != leader.Identity() {
			t.Errorf("holder while the bus stopped = %q, want %q", holderWhileQuieting, leader.Identity())
		}
		if server.Holder() != "" {
			t.Errorf("holder after the step down = %q, want the Lease released", server.Holder())
		}
		if leader.exited() {
			t.Errorf("a shutdown exited with %d", leader.exitCode.Load())
		}
	})
}

// A bus session that does not stop in time keeps the Lease, so no
// other copy leads while the session is still open. The Lease then
// expires on its own, and the line says so.
func TestAStepDownWhoseBusDoesNotStopKeepsTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := holdingCandidate(t, server, "library-operator-a")

		leader.stepDown(func() bool { return false })
		time.Sleep(election.RetryPeriod)

		if server.Holder() != leader.Identity() {
			t.Errorf("holder = %q, want the Lease still held by %q", server.Holder(), leader.Identity())
		}
		want := "library.liken.sh: the bus session did not stop; leaving the Lease to expire"
		if lines := leader.lineLog(); !slices.Contains(lines, want) {
			t.Errorf("lines = %q, want %q", lines, want)
		}
	})
}
