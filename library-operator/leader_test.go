//go:build !pod

package main

// These tests cover what this operator adds to client-go's election:
// the order of a shutdown, the exit on a lost Lease, and the prompt
// takeover a release gives a waiting copy. They run against the fake
// API server in leaseserver_test.go, in a synctest bubble, so the
// election runs at the operator's own timings and a wait of a Lease's
// duration takes no real time.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

const testLeaseNamespace = testOperatorNamespace

// candidate is one operator process under test, with the exit code it
// would have ended with, or -1. exits closes when the process would
// have ended.
type candidate struct {
	*leadership
	exitCode atomic.Int32
	exits    chan struct{}
}

func (c *candidate) exited() bool { return c.exitCode.Load() >= 0 }

func newCandidate(t *testing.T, server *leaseServer, pod string) *candidate {
	t.Helper()
	c := &candidate{exits: make(chan struct{})}
	c.exitCode.Store(-1)
	// The lines go to a buffer the test prints when it ends, because
	// client-go can call back after the test's last assertion.
	var lines sync.Mutex
	var reported []string
	report := func(line string) {
		lines.Lock()
		defer lines.Unlock()
		reported = append(reported, line)
	}
	var exiting sync.Once
	l, err := newLeadership(server.config(t), testLeaseNamespace, pod,
		func(code int) {
			c.exitCode.Store(int32(code))
			exiting.Do(func() { close(c.exits) })
		}, report)
	if err != nil {
		t.Fatal(err)
	}
	c.leadership = l
	c.run()
	t.Cleanup(func() {
		c.stepping.Store(true)
		c.cancel()
		select {
		case <-c.done:
		case <-time.After(3 * operatorLeaseTiming.duration):
			t.Error("the election never ended")
		}
		lines.Lock()
		defer lines.Unlock()
		for _, line := range reported {
			t.Log(line)
		}
		reported = nil
	})
	return c
}

// awaitWithin runs await with a deadline, and answers whether the
// candidate took the Lease.
func awaitWithin(c *candidate, spell time.Duration) bool {
	stop, cancel := context.WithTimeout(context.Background(), spell)
	defer cancel()
	return c.await(stop)
}

// holdingCandidate is a candidate that has taken the Lease.
func holdingCandidate(t *testing.T, server *leaseServer, pod string) *candidate {
	t.Helper()
	c := newCandidate(t, server, pod)
	if !awaitWithin(c, 3*operatorLeaseTiming.duration) {
		t.Fatalf("%s never took the Lease", pod)
	}
	return c
}

// exitsWithin waits for the candidate's exit for three Lease
// durations, and answers how long the exit took.
func exitsWithin(t *testing.T, c *candidate, complaint string) time.Duration {
	t.Helper()
	started := time.Now()
	select {
	case <-c.exits:
		return time.Since(started)
	case <-time.After(3 * operatorLeaseTiming.duration):
		t.Fatal(complaint)
		return 0
	}
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
			holderWhileQuieting = server.holder()
			return true
		})

		if holderWhileQuieting != leader.identity {
			t.Errorf("holder while the bus stopped = %q, want %q", holderWhileQuieting, leader.identity)
		}
		if server.holder() != "" {
			t.Errorf("holder after the step down = %q, want the Lease released", server.holder())
		}
		if leader.exited() {
			t.Errorf("a shutdown exited with %d", leader.exitCode.Load())
		}
	})
}

// A renewal the step down cancelled can still land on the API server
// after client-go's release read the Lease, and the release then fails
// with a conflict. The step down still leaves the Lease released, so a
// waiting copy does not wait out the Lease's duration.
func TestAStepDownReleasesTheLeaseAfterALateRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := holdingCandidate(t, server, "library-operator-a")

		leader.stepDown(func() bool {
			server.landAWriteAfterTheNextRead()
			return true
		})

		if server.holder() != "" {
			t.Errorf("holder after the step down = %q, want the Lease released", server.holder())
		}
	})
}

// A bus session that does not stop in time keeps the Lease, so no
// other copy leads while the session is still open.
func TestAStepDownWhoseBusDoesNotStopKeepsTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := holdingCandidate(t, server, "library-operator-a")

		leader.stepDown(func() bool { return false })

		if server.holder() != leader.identity {
			t.Errorf("holder = %q, want the Lease still held by %q", server.holder(), leader.identity)
		}
	})
}

// A waiting copy takes a released Lease on its next retry, well inside
// the Lease's duration. This is what makes a rolling update fast: the
// new pod waits beside the old one, and takes over when the old one
// steps down. client-go adds up to 1.2 retry periods of jitter to each
// retry, so the next retry comes at most 2.2 retry periods after the
// last one.
func TestAWaitingCopyTakesAReleasedLeaseAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		old := holdingCandidate(t, server, "library-operator-old")
		replacement := newCandidate(t, server, "library-operator-new")
		select {
		case <-replacement.started:
			t.Fatal("the new copy took a Lease the old copy holds")
		case <-time.After(2 * operatorLeaseTiming.duration):
		}

		old.stepDown(func() bool { return true })
		released := time.Now()

		if !awaitWithin(replacement, 3*operatorLeaseTiming.duration) {
			t.Fatal("the new copy never took the released Lease")
		}
		longestRetry := operatorLeaseTiming.retryPeriod * 22 / 10
		if waited := time.Since(released); waited > longestRetry {
			t.Errorf("took the released Lease after %s, want at most %s", waited, longestRetry)
		}
	})
}

// A leader whose renewals fail exits before a waiting copy could take
// the Lease, which is a Lease duration after the last renewal.
func TestALeaderThatCannotRenewExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := holdingCandidate(t, server, "library-operator-a")

		server.setRefusal(http.MethodPut, http.StatusInternalServerError)

		took := exitsWithin(t, leader, "the leader kept acting with no renewal")
		if code := leader.exitCode.Load(); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if took >= operatorLeaseTiming.duration {
			t.Errorf("exited %s after the renewals failed, want less than the Lease's %s",
				took, operatorLeaseTiming.duration)
		}
	})
}

func TestALeaderWhoseLeaseAnotherProcessTookExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := holdingCandidate(t, server, "library-operator-a")

		server.holdAs("library-operator-b")

		exitsWithin(t, leader, "the leader kept acting after another process took the Lease")
	})
}

// A shutdown that arrives while the copy waits ends the wait with no
// Lease and no exit code of its own; lead then exits cleanly.
func TestAStopWhileWaitingLeavesTheHolderAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		server.holdAs("library-operator-old")
		waiting := newCandidate(t, server, "library-operator-new")
		stop, cancel := context.WithCancel(context.Background())
		cancel()

		if waiting.await(stop) {
			t.Fatal("await reported a Lease after the stop")
		}
		if server.holder() != "library-operator-old" || waiting.exited() {
			t.Errorf("holder = %q, exit = %d, want the old holder and no exit",
				server.holder(), waiting.exitCode.Load())
		}
	})
}

// The leader renews from the version it wrote last, so a steady leader
// sends one update each retry period and does not read the Lease
// before each one.
func TestASteadyLeaderRenewsWithNoRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		holdingCandidate(t, server, "library-operator-a")
		synctest.Wait()
		reads, updates := server.count(http.MethodGet), server.count(http.MethodPut)

		time.Sleep(5 * operatorLeaseTiming.retryPeriod)
		synctest.Wait()

		renewals, read := server.count(http.MethodPut)-updates, server.count(http.MethodGet)-reads
		if renewals != 5 || read != 0 {
			t.Errorf("%d updates and %d reads while leading, want 5 updates and no read", renewals, read)
		}
	})
}

func TestTheIdentityIsNewForEachProcess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		first := newCandidate(t, server, "library-operator-a")
		second := newCandidate(t, server, "library-operator-a")

		if first.identity == second.identity || !strings.HasPrefix(first.identity, "library-operator-a_") {
			t.Errorf("identities = %q and %q, want two identities for one pod", first.identity, second.identity)
		}
	})
}
