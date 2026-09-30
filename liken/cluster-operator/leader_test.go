package main

// These tests cover what this operator adds to client-go's election:
// the write guard, the release on a shutdown, the exit on a lost Lease,
// and the prompt takeover a release gives a waiting copy. They run in
// synctest bubbles against the fake API server in leaseserver_test.go,
// with the operator's own durations. synctest.Wait returns once the
// elector has done all it can at the present moment, and a time.Sleep
// waits out a retry period or a Lease duration on the fake clock.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/tools/leaderelection"
)

// nextTry is the longest wait between two tries of a copy that waits
// for the Lease: one retry period, and up to 1.2 retry periods of
// client-go's jitter.
var nextTry = operatorLeaseTiming.retryPeriod +
	time.Duration(leaderelection.JitterFactor*float64(operatorLeaseTiming.retryPeriod))

// candidate is one operator process under test, with the exit code it
// would have ended with, or -1. exits closes when it exits.
type candidate struct {
	*leadership
	exitCode atomic.Int32
	exits    chan struct{}
	gauge    atomic.Bool
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
	var exit sync.Once
	l, err := newLeadership(server.config(t), pod,
		func(unelected bool) { c.gauge.Store(unelected) },
		func(code int) {
			exit.Do(func() {
				c.exitCode.Store(int32(code))
				close(c.exits)
			})
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
		case <-time.After(operatorLeaseTiming.duration):
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

// leadsAtOnce answers whether the candidate holds the Lease once the
// election has done all it can with no time passing.
func leadsAtOnce(c *candidate) bool {
	synctest.Wait()
	return c.leads()
}

// awaitWithin runs await with a deadline, and answers whether the
// candidate took the Lease.
func awaitWithin(c *candidate, spell time.Duration) bool {
	stop, cancel := context.WithTimeout(context.Background(), spell)
	defer cancel()
	return c.await(stop)
}

// A shutdown releases the Lease, and it is not a loss.
func TestAStepDownReleasesTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "liken-cluster-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the copy did not take a Lease that nobody holds")
		}

		leader.stepDown()

		if server.holder() != "" {
			t.Errorf("holder after the step down = %q, want the Lease released", server.holder())
		}
		if leader.exited() {
			t.Errorf("a shutdown exited with %d", leader.exitCode.Load())
		}
		if err := leader.mayWrite(); err == nil {
			t.Error("a copy that stepped down still allows writes")
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
		leader := newCandidate(t, server, "liken-cluster-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the copy did not take a Lease that nobody holds")
		}

		server.landAWriteAfterTheNextRead()
		leader.stepDown()

		if server.holder() != "" {
			t.Errorf("holder after the step down = %q, want the Lease released", server.holder())
		}
	})
}

// A waiting copy takes a released Lease on its next try, well inside
// the Lease's duration. This is what makes a rolling update fast: the
// new pod waits beside the old one, and takes over when the old one
// steps down.
func TestAWaitingCopyTakesAReleasedLeaseAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		old := newCandidate(t, server, "liken-cluster-operator-old")
		if !leadsAtOnce(old) {
			t.Fatal("the old copy did not take a Lease that nobody holds")
		}
		replacement := newCandidate(t, server, "liken-cluster-operator-new")
		time.Sleep(2 * operatorLeaseTiming.duration)
		synctest.Wait()
		if replacement.leads() {
			t.Fatal("the new copy took a Lease the old copy holds")
		}

		old.stepDown()

		if !awaitWithin(replacement, nextTry) {
			t.Fatalf("the new copy did not take the released Lease within %s, its longest wait between tries", nextTry)
		}
	})
}

// A leader that cannot renew refuses writes one renewal deadline after
// its last renewal, before the election gives up, and then exits. It
// exits before a waiting copy can take the Lease, which is one Lease
// duration after the last renewal a waiting copy read. The renewal
// after that exit is one retry period later at the earliest, so the
// exit comes at most the duration less one retry period after the last
// renewal (operatorLeaseTiming).
func TestALeaderThatCannotRenewExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "liken-cluster-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the copy did not take a Lease that nobody holds")
		}
		renewed := leader.lock.lastRenewal()

		server.setRefusal(http.MethodPut, http.StatusInternalServerError)

		time.Sleep(operatorLeaseTiming.renewDeadline)
		synctest.Wait()
		if err := leader.mayWrite(); err == nil || leader.exited() {
			t.Errorf("one renewal deadline after the last renewal, mayWrite = %v and exited = %v; want writes refused before the exit",
				err, leader.exited())
		}
		select {
		case <-leader.exits:
		case <-time.After(operatorLeaseTiming.duration):
			t.Fatal("the leader kept acting with no renewal")
		}
		if exit, limit := time.Since(renewed), operatorLeaseTiming.duration-operatorLeaseTiming.retryPeriod; exit > limit {
			t.Errorf("the leader exited %s after its last renewal, want at most %s", exit, limit)
		}
		if err := leader.mayWrite(); err == nil {
			t.Error("a leader that exited still allows writes")
		}
		if code := leader.exitCode.Load(); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	})
}

func TestALeaderWhoseLeaseAnotherProcessTookExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "liken-cluster-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the copy did not take a Lease that nobody holds")
		}
		renewed := leader.lock.lastRenewal()

		server.holdAs("liken-cluster-operator-b")

		select {
		case <-leader.exits:
		case <-time.After(operatorLeaseTiming.duration):
			t.Fatal("the leader kept acting after another process took the Lease")
		}
		if exit, limit := time.Since(renewed), operatorLeaseTiming.duration-operatorLeaseTiming.retryPeriod; exit > limit {
			t.Errorf("the leader exited %s after its last renewal, want at most %s", exit, limit)
		}
	})
}

// A shutdown that arrives while the copy waits ends the wait with no
// Lease and no exit code of its own; lead then exits cleanly.
func TestAStopWhileWaitingLeavesTheHolderAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		server.holdAs("liken-cluster-operator-old")
		waiting := newCandidate(t, server, "liken-cluster-operator-new")
		stop, cancel := context.WithCancel(context.Background())
		cancel()

		if waiting.await(stop) {
			t.Fatal("await reported a Lease after the stop")
		}
		if server.holder() != "liken-cluster-operator-old" || waiting.exited() {
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
		leader := newCandidate(t, server, "liken-cluster-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the copy did not take a Lease that nobody holds")
		}
		reads, updates := server.count(http.MethodGet), server.count(http.MethodPut)

		time.Sleep(5 * operatorLeaseTiming.retryPeriod)
		synctest.Wait()

		if sent, read := server.count(http.MethodPut)-updates, server.count(http.MethodGet)-reads; sent != 5 || read != 0 {
			t.Errorf("%d updates and %d reads in five retry periods of the lead, want 5 updates and no read", sent, read)
		}
	})
}

func TestTheIdentityIsNewForEachProcess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		first := newCandidate(t, server, "liken-cluster-operator-a")
		second := newCandidate(t, server, "liken-cluster-operator-a")

		if first.identity == second.identity || !strings.HasPrefix(first.identity, "liken-cluster-operator-a_") {
			t.Errorf("identities = %q and %q, want two identities for one pod", first.identity, second.identity)
		}
	})
}

// The write guard allows a write only while this copy leads and its
// last renewal is younger than one renewal deadline. The elector gives
// up later than that, so the guard stops writes first.
func TestTheWriteGuardAllowsOnlyAFreshLeader(t *testing.T) {
	cases := []struct {
		name      string
		leading   bool
		stepping  bool
		renewedAt time.Duration
		allowed   bool
	}{
		{name: "a waiting copy", leading: false, renewedAt: 0, allowed: false},
		{name: "a leader that just renewed", leading: true, renewedAt: 0, allowed: true},
		{name: "a leader one retry period after its renewal", leading: true, renewedAt: operatorLeaseTiming.retryPeriod, allowed: true},
		{name: "a leader one renewal deadline after its renewal", leading: true, renewedAt: operatorLeaseTiming.renewDeadline, allowed: false},
		{name: "a leader that is stepping down", leading: true, stepping: true, renewedAt: 0, allowed: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := &leadership{started: make(chan struct{}), lock: &renewalClock{}}
			if c.leading {
				close(l.started)
			}
			l.stepping.Store(c.stepping)
			l.lock.renewed = time.Now().Add(-c.renewedAt)

			if err := l.mayWrite(); (err == nil) != c.allowed {
				t.Errorf("mayWrite = %v, want allowed %v", err, c.allowed)
			}
		})
	}
}

// A step down whose release the API server refuses leaves the Lease
// with this copy's name, to expire after its duration, and still
// returns, so the process exits inside its grace period.
func TestAStepDownWhoseReleaseIsRefusedLeavesTheLeaseToExpire(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newLeaseServer()
				leader := newCandidate(t, server, "liken-cluster-operator-a")
				if !leadsAtOnce(leader) {
					t.Fatal("the copy did not take a Lease that nobody holds")
				}
				server.setRefusal(method, http.StatusInternalServerError)

				leader.stepDown()

				if server.holder() != leader.identity || leader.exited() {
					t.Errorf("holder = %q, exit = %d; want the Lease still held and no exit",
						server.holder(), leader.exitCode.Load())
				}
			})
		})
	}
}

// A Lease that an earlier process of this pod held is free once its
// last renewal is one Lease duration old. The kubelet runs one process
// of a container at a time, so that process has exited, and its
// renewal time comes from this node's clock. A Lease that another pod
// holds, or that this pod's earlier process renewed within the
// duration, waits out the duration from this process's first read of
// it, as client-go measures it. So a copy takes only the first kind
// within one second less than a whole duration.
func TestAnExpiredLeaseOfAnEarlierProcessOfThisPodIsTakenAtOnce(t *testing.T) {
	cases := []struct {
		name    string
		holder  string
		renewed time.Duration
		taken   bool
	}{
		{name: "this pod, renewed long ago", holder: "liken-cluster-operator-a_0badc0de", renewed: 3 * operatorLeaseTiming.duration, taken: true},
		{name: "this pod, renewed within the duration", holder: "liken-cluster-operator-a_0badc0de", renewed: 0, taken: false},
		{name: "another pod, renewed long ago", holder: "liken-cluster-operator-b_0badc0de", renewed: 3 * operatorLeaseTiming.duration, taken: false},
		{name: "a pod whose name starts with this pod's", holder: "liken-cluster-operator-a-2_0badc0de", renewed: 3 * operatorLeaseTiming.duration, taken: false},
		// The first read finds the Lease current, and a later read finds
		// it expired, before a whole duration from the first read: half
		// a duration, and at most one wait between tries, from the start.
		{name: "this pod, expiring while this process waits", holder: "liken-cluster-operator-a_0badc0de", renewed: operatorLeaseTiming.duration / 2, taken: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newLeaseServer()
				server.holdAsRenewedAt(c.holder, time.Now().Add(-c.renewed))

				restarted := newCandidate(t, server, "liken-cluster-operator-a")

				if taken := awaitWithin(restarted, operatorLeaseTiming.duration-time.Second); taken != c.taken {
					t.Errorf("took the Lease within one second less than its duration = %v, want %v", taken, c.taken)
				}
			})
		})
	}
}

// A stop that arrives before the elector's next read still frees a
// Lease that an earlier process of this pod left, so the copy that
// waits in another pod takes it on its next retry. The same rule as
// the take above decides which Lease the release may clear.
func TestTheReleaseClearsAnExpiredLeaseOfAnEarlierProcessOfThisPod(t *testing.T) {
	cases := []struct {
		name    string
		holder  string
		renewed time.Duration
		cleared bool
	}{
		{name: "this pod, renewed long ago", holder: "liken-cluster-operator-a_0badc0de", renewed: 3 * operatorLeaseTiming.duration, cleared: true},
		{name: "this pod, renewed within the duration", holder: "liken-cluster-operator-a_0badc0de", renewed: 0, cleared: false},
		{name: "another pod, renewed long ago", holder: "liken-cluster-operator-b_0badc0de", renewed: 3 * operatorLeaseTiming.duration, cleared: false},
		{name: "a pod whose name starts with this pod's", holder: "liken-cluster-operator-a-2_0badc0de", renewed: 3 * operatorLeaseTiming.duration, cleared: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := newLeaseServer()
			server.holdAsRenewedAt(c.holder, time.Now().Add(-c.renewed))
			stopped, err := newLeadership(server.config(t), "liken-cluster-operator-a",
				nil, func(int) {}, func(string) {})
			if err != nil {
				t.Fatal(err)
			}

			stopped.clearIfHeld(context.Background())

			if cleared := server.holder() == ""; cleared != c.cleared {
				t.Errorf("holder after the release = %q, want cleared %v", server.holder(), c.cleared)
			}
		})
	}
}
