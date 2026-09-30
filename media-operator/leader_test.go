//go:build !pod

package main

// These tests cover what this operator adds to client-go's election:
// the order of a shutdown, the exit on a lost Lease, and the prompt
// takeover a release gives a waiting copy. They run against the fake
// API server in leaseserver_test.go, in a synctest bubble, so the
// election runs at the operator's own durations and a test checks each
// wait against them.

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

const testLeaseNamespace = "liken-system"

// candidate is one operator process under test, with the exit code it
// would have ended with, or -1, and the channel that closes when it
// exits.
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
	exit := func(code int) {
		c.exitCode.Store(int32(code))
		close(c.exits)
	}
	l, err := newLeadership(server.config(t), testLeaseNamespace, pod, exit, report)
	mustSucceed(t, err)
	c.leadership = l
	c.run()
	t.Cleanup(func() {
		c.stepping.Store(true)
		c.cancel()
		<-c.done
		lines.Lock()
		defer lines.Unlock()
		for _, line := range reported {
			t.Log(line)
		}
		reported = nil
	})
	return c
}

// leads fails the test unless the candidate holds the Lease once every
// goroutine in the bubble waits.
func leads(t *testing.T, c *candidate) {
	t.Helper()
	synctest.Wait()
	if !received(c.started) {
		t.Fatal("the copy never took the Lease")
	}
}

// exitsWithin answers how long the candidate took to exit, and fails
// the test when it does not exit within the wait.
func exitsWithin(t *testing.T, c *candidate, wait time.Duration) time.Duration {
	t.Helper()
	began := time.Now()
	select {
	case <-c.exits:
		return time.Since(began)
	case <-time.After(wait):
		t.Fatalf("the leader kept acting for %s", wait)
		return 0
	}
}

// A shutdown stops the bus session while the Lease still names this
// process, and releases the Lease after, so no bus message is handled
// once another copy can lead. A shutdown is not a loss.
func TestAStepDownQuietsTheBusBeforeItReleasesTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "media-operator-a")
		leads(t, leader)
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
		leader := newCandidate(t, server, "media-operator-a")
		leads(t, leader)

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
// other copy leads while the session can still write.
func TestAStepDownWhoseBusDoesNotStopKeepsTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "media-operator-a")
		leads(t, leader)

		leader.stepDown(func() bool { return false })

		if server.holder() != leader.identity {
			t.Errorf("holder = %q, want the Lease still held by %q", server.holder(), leader.identity)
		}
	})
}

// A waiting copy takes a released Lease on its next retry, within 11
// seconds, well inside the Lease's duration. This is what makes a
// rolling update fast: the new pod waits beside the old one, and takes
// over when the old one steps down.
func TestAWaitingCopyTakesAReleasedLeaseAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		old := newCandidate(t, server, "media-operator-old")
		leads(t, old)
		replacement := newCandidate(t, server, "media-operator-new")
		time.Sleep(2 * leaseDuration)
		synctest.Wait()
		if received(replacement.started) {
			t.Fatal("the new copy took a Lease the old copy holds")
		}

		old.stepDown(func() bool { return true })
		released := time.Now()

		if !awaitWithin(replacement, leaseDuration) {
			t.Fatal("the new copy never took the released Lease")
		}
		// client-go adds up to 1.2 retry periods of jitter to each retry.
		if waited := time.Since(released); waited > leaseRetryPeriod*11/5 {
			t.Errorf("took the released Lease after %s, want at most %s", waited, leaseRetryPeriod*11/5)
		}
	})
}

// awaitWithin runs await with a deadline, and answers whether the
// candidate took the Lease.
func awaitWithin(c *candidate, wait time.Duration) bool {
	stop, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	return c.await(stop)
}

// A leader whose renewals fail exits before the Lease's duration runs
// out after the last renewal, so it stops acting before a waiting copy
// can take the Lease.
func TestALeaderThatCannotRenewExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "media-operator-a")
		leads(t, leader)

		server.setRefusal(http.MethodPut, http.StatusInternalServerError)

		if waited := exitsWithin(t, leader, leaseDuration); waited >= leaseDuration {
			t.Errorf("the leader exited %s after its renewals failed, want less than %s", waited, leaseDuration)
		}
		if code := leader.exitCode.Load(); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	})
}

func TestALeaderWhoseLeaseAnotherProcessTookExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "media-operator-a")
		leads(t, leader)

		server.holdAs("media-operator-b")

		exitsWithin(t, leader, leaseDuration)
		if code := leader.exitCode.Load(); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	})
}

// A shutdown that arrives while the copy waits ends the wait with no
// Lease and no exit code of its own; lead then exits cleanly.
func TestAStopWhileWaitingLeavesTheHolderAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		server.holdAs("media-operator-old")
		waiting := newCandidate(t, server, "media-operator-new")
		stop, cancel := context.WithCancel(context.Background())
		cancel()

		if waiting.await(stop) {
			t.Fatal("await reported a Lease after the stop")
		}
		if server.holder() != "media-operator-old" || waiting.exited() {
			t.Errorf("holder = %q, exit = %d, want the old holder and no exit",
				server.holder(), waiting.exitCode.Load())
		}
	})
}

// The leader renews once per retry period from the version it wrote
// last, so a steady leader sends updates and does not read the Lease
// before each one.
func TestASteadyLeaderRenewsWithNoRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "media-operator-a")
		leads(t, leader)
		reads, updates := server.count(http.MethodGet), server.count(http.MethodPut)

		time.Sleep(5 * leaseRetryPeriod)
		synctest.Wait()

		if server.count(http.MethodPut)-updates != 5 || server.count(http.MethodGet) != reads {
			t.Errorf("%d updates and %d reads in five retry periods, want 5 updates and no read",
				server.count(http.MethodPut)-updates, server.count(http.MethodGet)-reads)
		}
	})
}

func TestTheIdentityIsNewForEachProcess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		first := newCandidate(t, server, "media-operator-a")
		second := newCandidate(t, server, "media-operator-a")

		if first.identity == second.identity || !strings.HasPrefix(first.identity, "media-operator-a_") {
			t.Errorf("identities = %q and %q, want two identities for one pod", first.identity, second.identity)
		}
	})
}
