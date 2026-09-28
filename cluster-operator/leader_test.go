package main

// These tests cover what this operator adds to client-go's election:
// the write guard, the release on a shutdown, the exit on a lost Lease,
// and the prompt takeover a release gives a waiting copy. They run against the fake
// API server in leaseserver_test.go, with durations short enough that a
// loss or a takeover happens in a few seconds.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// client-go writes the duration in whole seconds, so the shortest
// duration a waiting copy can read is one second.
var testLeaseTiming = leaseTiming{
	duration:      2 * time.Second,
	renewDeadline: 1 * time.Second,
	retryPeriod:   200 * time.Millisecond,
}

// candidate is one operator process under test, with the exit code it
// would have ended with, or -1.
type candidate struct {
	*leadership
	exitCode atomic.Int32
	gauge    atomic.Bool
}

func (c *candidate) exited() bool { return c.exitCode.Load() >= 0 }

func newCandidate(t *testing.T, server *leaseServer, pod string) *candidate {
	t.Helper()
	c := &candidate{}
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
	l, err := newLeadership(server.config(t), pod, testLeaseTiming,
		func(unelected bool) { c.gauge.Store(unelected) },
		func(code int) { c.exitCode.Store(int32(code)) }, report)
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
		case <-time.After(3 * testLeaseTiming.duration):
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

// exitsWithin waits for the candidate's exit, for longer than one
// Lease duration, because client-go gives up only after the renewal
// deadline.
func exitsWithin(t *testing.T, c *candidate, complaint string) {
	t.Helper()
	deadline := time.Now().Add(3 * testLeaseTiming.duration)
	for !c.exited() {
		if time.Now().After(deadline) {
			t.Fatal(complaint)
		}
		time.Sleep(testLeaseTiming.retryPeriod / 4)
	}
}

// A shutdown releases the Lease, and it is not a loss.
func TestAStepDownReleasesTheLease(t *testing.T) {
	server := newLeaseServer()
	leader := newCandidate(t, server, "liken-cluster-operator-a")
	if !awaitWithin(leader, 3*testLeaseTiming.duration) {
		t.Fatal("the copy never took the Lease")
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
}

// A renewal the step down cancelled can still land on the API server
// after client-go's release read the Lease, and the release then fails
// with a conflict. The step down still leaves the Lease released, so a
// waiting copy does not wait out the Lease's duration.
func TestAStepDownReleasesTheLeaseAfterALateRenewal(t *testing.T) {
	server := newLeaseServer()
	leader := newCandidate(t, server, "liken-cluster-operator-a")
	if !awaitWithin(leader, 3*testLeaseTiming.duration) {
		t.Fatal("the copy never took the Lease")
	}

	server.landAWriteAfterTheNextRead()
	leader.stepDown()

	if server.holder() != "" {
		t.Errorf("holder after the step down = %q, want the Lease released", server.holder())
	}
}

// A waiting copy takes a released Lease on its next retry, well inside
// the Lease's duration. This is what makes a rolling update fast: the
// new pod waits beside the old one, and takes over when the old one
// steps down.
func TestAWaitingCopyTakesAReleasedLeaseAtOnce(t *testing.T) {
	server := newLeaseServer()
	old := newCandidate(t, server, "liken-cluster-operator-old")
	if !awaitWithin(old, 3*testLeaseTiming.duration) {
		t.Fatal("the old copy never took the Lease")
	}
	replacement := newCandidate(t, server, "liken-cluster-operator-new")
	select {
	case <-replacement.started:
		t.Fatal("the new copy took a Lease the old copy holds")
	case <-time.After(2 * testLeaseTiming.duration):
	}

	old.stepDown()
	released := time.Now()

	if !awaitWithin(replacement, 3*testLeaseTiming.duration) {
		t.Fatal("the new copy never took the released Lease")
	}
	if waited := time.Since(released); waited >= testLeaseTiming.duration {
		t.Errorf("took the released Lease after %s, want less than %s", waited, testLeaseTiming.duration)
	}
}

// A leader that cannot renew refuses writes before the election gives
// up, and then exits.
func TestALeaderThatCannotRenewExits(t *testing.T) {
	server := newLeaseServer()
	leader := newCandidate(t, server, "liken-cluster-operator-a")
	if !awaitWithin(leader, 3*testLeaseTiming.duration) {
		t.Fatal("the copy never took the Lease")
	}

	server.setRefusal(http.MethodPut, http.StatusInternalServerError)

	exitsWithin(t, leader, "the leader kept acting with no renewal")
	if err := leader.mayWrite(); err == nil {
		t.Error("a leader that exited still allows writes")
	}
	if code := leader.exitCode.Load(); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestALeaderWhoseLeaseAnotherProcessTookExits(t *testing.T) {
	server := newLeaseServer()
	leader := newCandidate(t, server, "liken-cluster-operator-a")
	if !awaitWithin(leader, 3*testLeaseTiming.duration) {
		t.Fatal("the copy never took the Lease")
	}

	server.holdAs("liken-cluster-operator-b")

	exitsWithin(t, leader, "the leader kept acting after another process took the Lease")
}

// A shutdown that arrives while the copy waits ends the wait with no
// Lease and no exit code of its own; lead then exits cleanly.
func TestAStopWhileWaitingLeavesTheHolderAlone(t *testing.T) {
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
}

// The leader renews from the version it wrote last, so a steady leader
// sends updates and does not read the Lease before each one.
func TestASteadyLeaderRenewsWithNoRead(t *testing.T) {
	server := newLeaseServer()
	leader := newCandidate(t, server, "liken-cluster-operator-a")
	if !awaitWithin(leader, 3*testLeaseTiming.duration) {
		t.Fatal("the copy never took the Lease")
	}
	reads := server.count(http.MethodGet)

	time.Sleep(5 * testLeaseTiming.retryPeriod)

	if server.count(http.MethodPut) < 3 || server.count(http.MethodGet) != reads {
		t.Errorf("%d updates and %d reads while leading, want at least 3 updates and no read",
			server.count(http.MethodPut), server.count(http.MethodGet)-reads)
	}
}

func TestTheIdentityIsNewForEachProcess(t *testing.T) {
	server := newLeaseServer()
	first := newCandidate(t, server, "liken-cluster-operator-a")
	second := newCandidate(t, server, "liken-cluster-operator-a")

	if first.identity == second.identity || !strings.HasPrefix(first.identity, "liken-cluster-operator-a_") {
		t.Errorf("identities = %q and %q, want two identities for one pod", first.identity, second.identity)
	}
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
			l := &leadership{timing: operatorLeaseTiming, started: make(chan struct{}), lock: &renewalClock{}}
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
			server := newLeaseServer()
			leader := newCandidate(t, server, "liken-cluster-operator-a")
			if !awaitWithin(leader, 3*testLeaseTiming.duration) {
				t.Fatal("the copy never took the Lease")
			}
			server.setRefusal(method, http.StatusInternalServerError)

			leader.stepDown()

			if server.holder() != leader.identity || leader.exited() {
				t.Errorf("holder = %q, exit = %d; want the Lease still held and no exit",
					server.holder(), leader.exitCode.Load())
			}
		})
	}
}
