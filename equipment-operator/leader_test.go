//go:build !node

package main

// These tests cover what this operator adds to client-go's election:
// the order of a shutdown, the exit on a lost Lease, the prompt
// takeover a release gives a waiting copy, and a waiting copy that does
// nothing. They run against the fake API server in leaseserver_test.go,
// at the durations the operator runs with, on the fake clock of a
// synctest bubble.

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/wiim"
)

const testLeaseNamespace = "liken-system"

// heldPastItsDuration is how long a test watches a waiting copy while
// another copy holds the Lease. A holder that did not renew would lose
// the Lease at its duration, and the waiting copy would take it on its
// next retry, so the wait runs two retries past the duration.
func heldPastItsDuration(timing leaseTiming) time.Duration {
	return timing.duration + 2*timing.retryPeriod
}

// candidate is one operator process under test, with the timing of its
// election and the exit code it would have ended with, or -1.
type candidate struct {
	*leadership
	timing   leaseTiming
	exitCode atomic.Int32
}

func (c *candidate) exited() bool { return c.exitCode.Load() >= 0 }

func newCandidate(t *testing.T, server *leaseServer, pod string) *candidate {
	t.Helper()
	c := &candidate{timing: server.timing}
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
	l, err := newLeadership(server.config(t), testLeaseNamespace, pod, c.timing,
		func(code int) { c.exitCode.Store(int32(code)) }, report)
	mustSucceed(t, err)
	c.leadership = l
	c.run()
	t.Cleanup(func() {
		c.stepping.Store(true)
		c.cancel()
		select {
		case <-c.done:
		case <-time.After(3 * c.timing.duration):
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
	deadline := time.Now().Add(3 * c.timing.duration)
	for !c.exited() {
		if time.Now().After(deadline) {
			t.Fatal(complaint)
		}
		time.Sleep(c.timing.retryPeriod / 4)
	}
}

// leading runs actWhileLeading in the background with an act that
// holds until the test releases it, and answers the channel that
// closes when act starts and the function that ends act.
func leading(t *testing.T, c *candidate, act func() error) (started <-chan struct{}, finish func() (bool, error)) {
	t.Helper()
	begun, release := make(chan struct{}), make(chan struct{})
	type result struct {
		led bool
		err error
	}
	results := make(chan result, 1)
	go func() {
		led, err := c.actWhileLeading(context.Background(), func() error {
			close(begun)
			<-release
			return act()
		})
		results <- result{led, err}
	}()
	return begun, func() (bool, error) {
		close(release)
		select {
		case r := <-results:
			return r.led, r.err
		case <-time.After(3 * c.timing.duration):
			t.Fatal("actWhileLeading did not return")
			return false, nil
		}
	}
}

// awaitStart waits for the candidate's act to start.
func awaitStart(t *testing.T, c *candidate, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(3 * c.timing.duration):
		t.Fatal("the copy never took the Lease")
	}
}

// The operator acts only while the Lease names this process, and
// releases it only after act has returned, so no write of the operator
// lands once another copy can lead. A shutdown is not a loss.
func TestTheLeaseIsReleasedOnlyAfterTheOperatorStops(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "equipment-operator-a")
		holderWhileActing := ""
		started, finish := leading(t, leader, func() error {
			holderWhileActing = server.holder()
			return nil
		})
		awaitStart(t, leader, started)

		led, err := finish()

		mustSucceed(t, err)
		mustMatch(t, led, true)
		mustMatch(t, holderWhileActing, leader.identity)
		mustMatch(t, server.holder(), "")
		mustMatch(t, leader.exited(), false)
	})
}

// A renewal the shutdown cancelled can still land on the API server
// after client-go's release read the Lease, and the release then fails
// with a conflict. The shutdown still leaves the Lease released, so a
// waiting copy does not wait out the Lease's duration.
func TestAStepDownReleasesTheLeaseAfterALateRenewal(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "equipment-operator-a")
		started, finish := leading(t, leader, func() error {
			server.landAWriteAfterTheNextRead()
			return nil
		})
		awaitStart(t, leader, started)

		_, err := finish()

		mustSucceed(t, err)
		mustMatch(t, server.holder(), "")
	})
}

// A waiting copy takes a released Lease on its next retry, well inside
// the Lease's duration, so a replacement pod takes over soon after the
// old one stops.
func TestAWaitingCopyTakesAReleasedLeaseAtOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		old := newCandidate(t, server, "equipment-operator-old")
		started, finish := leading(t, old, func() error { return nil })
		awaitStart(t, old, started)
		replacement := newCandidate(t, server, "equipment-operator-new")
		select {
		case <-replacement.started:
			t.Fatal("the new copy took a Lease the old copy holds")
		case <-time.After(heldPastItsDuration(operatorLeaseTiming)):
		}

		_, _ = finish()
		released := time.Now()

		if !awaitWithin(replacement, 3*operatorLeaseTiming.duration) {
			t.Fatal("the new copy never took the released Lease")
		}
		if waited := time.Since(released); waited >= operatorLeaseTiming.duration {
			t.Errorf("took the released Lease after %s, want less than %s", waited, operatorLeaseTiming.duration)
		}
	})
}

func TestALeaderThatCannotRenewExits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "equipment-operator-a")
		if !awaitWithin(leader, 3*operatorLeaseTiming.duration) {
			t.Fatal("the copy never took the Lease")
		}

		server.setRefusal(http.MethodPut, http.StatusInternalServerError)

		exitsWithin(t, leader, "the leader kept acting with no renewal")
		if code := leader.exitCode.Load(); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	})
}

func TestALeaderWhoseLeaseAnotherProcessTookExits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "equipment-operator-a")
		if !awaitWithin(leader, 3*operatorLeaseTiming.duration) {
			t.Fatal("the copy never took the Lease")
		}

		server.holdAs("equipment-operator-b")

		exitsWithin(t, leader, "the leader kept acting after another process took the Lease")
	})
}

// A shutdown that arrives while the copy waits ends the wait with no
// Lease, runs nothing, and leaves the holder alone.
func TestAStopWhileWaitingLeavesTheHolderAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		server.holdAs("equipment-operator-old")
		waiting := newCandidate(t, server, "equipment-operator-new")
		stop, cancel := context.WithCancel(context.Background())
		cancel()
		acted := false

		led, err := waiting.actWhileLeading(stop, func() error {
			acted = true
			return nil
		})

		mustSucceed(t, err)
		mustMatch(t, led, false)
		mustMatch(t, acted, false)
		mustMatch(t, server.holder(), "equipment-operator-old")
		mustMatch(t, waiting.exited(), false)
	})
}

// The leader renews from the version it wrote last, so a steady leader
// sends updates and does not read the Lease before each one.
func TestASteadyLeaderRenewsWithNoRead(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "equipment-operator-a")
		if !awaitWithin(leader, 3*operatorLeaseTiming.duration) {
			t.Fatal("the copy never took the Lease")
		}
		reads := server.count(http.MethodGet)

		time.Sleep(5 * operatorLeaseTiming.retryPeriod)

		if server.count(http.MethodPut) < 3 || server.count(http.MethodGet) != reads {
			t.Errorf("%d updates and %d reads while leading, want at least 3 updates and no read",
				server.count(http.MethodPut), server.count(http.MethodGet)-reads)
		}
	})
}

func TestTheIdentityIsNewForEachProcess(t *testing.T) {
	t.Parallel()
	server := newLeaseServer()
	first := newCandidate(t, server, "equipment-operator-a")
	second := newCandidate(t, server, "equipment-operator-a")

	if first.identity == second.identity || !strings.HasPrefix(first.identity, "equipment-operator-a_") {
		t.Errorf("identities = %q and %q, want two identities for one pod", first.identity, second.identity)
	}
}

// freeAddress answers a loopback address with a port nothing listens on.
func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	mustSucceed(t, err)
	address := listener.Addr().String()
	mustSucceed(t, listener.Close())
	return address
}

// bound answers whether something listens on the address.
func bound(address string) bool {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return true
	}
	_ = listener.Close()
	return false
}

// A copy that waits for the Lease binds no port, searches for no
// device, and sends the API server nothing but its reads of the Lease,
// which the Lease server answers. So it opens no receiver session
// either, because a session starts from a pass. Once it takes the
// Lease, it does all of these.
func TestAWaitingCopyDoesNothingUntilItLeads(t *testing.T) {
	var searches atomic.Int32
	restore := discover
	discover = func(context.Context, time.Duration) []wiim.Device {
		searches.Add(1)
		return nil
	}
	t.Cleanup(func() { discover = restore })
	api := startFakeAPI(t)
	api.setReceivers()
	// The copy that takes the Lease binds a real port for its metrics,
	// and a goroutine that waits in a socket's accept keeps a synctest
	// bubble's clock still. So this test runs on the real clock, with a
	// Lease short enough that a takeover happens in two seconds. client-go
	// writes the duration in whole seconds, so one second is the shortest
	// duration a waiting copy can read.
	server := newLeaseServer()
	server.timing = leaseTiming{duration: 2 * time.Second, renewDeadline: time.Second, retryPeriod: 200 * time.Millisecond}
	old := newCandidate(t, server, "equipment-operator-old")
	oldStarted, oldFinish := leading(t, old, func() error { return nil })
	awaitStart(t, old, oldStarted)
	waiting := newCandidate(t, server, "equipment-operator-new")
	metricsAddress := freeAddress(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := waiting.actWhileLeading(ctx, func() error {
			return operateAsLeader(ctx, api.client, settings{metricsAddress: metricsAddress, dial: testNetwork.dial})
		})
		done <- err
	}()

	time.Sleep(heldPastItsDuration(server.timing))
	api.mutex.Lock()
	listsWhileWaiting := api.lists
	api.mutex.Unlock()
	mustMatch(t, listsWhileWaiting, 0)
	mustMatch(t, searches.Load(), 0)
	mustMatch(t, bound(metricsAddress), false)

	_, _ = oldFinish()
	deadline := time.Now().Add(3 * server.timing.duration)
	for {
		api.mutex.Lock()
		lists := api.lists
		api.mutex.Unlock()
		if lists > 0 && searches.Load() > 0 && bound(metricsAddress) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the Lease: %d lists, %d searches, metrics bound %t", lists, searches.Load(), bound(metricsAddress))
		}
		time.Sleep(server.timing.retryPeriod / 4)
	}
	cancel()
	select {
	case err := <-done:
		mustSucceed(t, err)
	case <-time.After(testTimeout):
		t.Fatal("the operator did not stop")
	}
}

// An operator whose writes did not stop in time keeps the Lease, so no
// other copy leads while a write can still land. The Lease expires
// after the process exits.
func TestAStillWritingOperatorKeepsTheLease(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, "equipment-operator-a")
		started, finish := leading(t, leader, func() error { return errStillWriting })
		awaitStart(t, leader, started)

		led, err := finish()

		mustMatch(t, led, true)
		mustMatch(t, err, errStillWriting)
		mustMatch(t, server.holder(), leader.identity)
	})
}
