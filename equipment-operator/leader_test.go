//go:build !node

package main

// These tests cover how the operator uses kubernetes/election. The
// election's own tests are in its package.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/election/electiontest"

	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/liken-sh/liken/equipment-operator/wiim"
)

const testLeaseNamespace = "liken-system"

func newLeaseServer() *electiontest.Server {
	return electiontest.New(leaseName, testLeaseNamespace)
}

// candidate is one operator process under test. exitCode is -1 until
// the election calls Exit.
type candidate struct {
	*leadership
	exitCode atomic.Int32
}

func (c *candidate) exited() bool { return c.exitCode.Load() >= 0 }

func newCandidate(t *testing.T, server *electiontest.Server, client *Client, pod string) *candidate {
	t.Helper()
	c := &candidate{}
	c.exitCode.Store(-1)
	// The report lines are logged at cleanup, because client-go can call
	// Report after the test returns, and t.Log then panics.
	var lines sync.Mutex
	var reported []string
	l, err := newLeadership(client, server.Config(t), election.Options{
		Name: leaseName, Namespace: testLeaseNamespace, Pod: pod,
		Report: func(line string) {
			lines.Lock()
			defer lines.Unlock()
			reported = append(reported, line)
		},
		Exit: func(code int) { c.exitCode.CompareAndSwap(-1, int32(code)) },
	})
	mustSucceed(t, err)
	c.leadership = l
	c.Run()
	// A synctest bubble ends only when all its goroutines have ended, so
	// the election must be stopped.
	t.Cleanup(func() {
		c.StepDown()
		lines.Lock()
		defer lines.Unlock()
		for _, line := range reported {
			t.Log(line)
		}
	})
	return c
}

// leading runs actWhileLeading in the background with an act that
// blocks until the test calls finish. finish returns what
// actWhileLeading returned.
func leading(t *testing.T, c *candidate, act func(*Client) error) (finish func() (bool, error)) {
	t.Helper()
	release := make(chan struct{})
	type result struct {
		led bool
		err error
	}
	results := make(chan result, 1)
	go func() {
		led, err := c.actWhileLeading(context.Background(), func(client *Client) error {
			<-release
			return act(client)
		})
		results <- result{led, err}
	}()
	synctest.Wait()
	if !c.Leads() {
		t.Fatal("the copy did not take a Lease that nobody holds")
	}
	return func() (bool, error) {
		close(release)
		r := <-results
		return r.led, r.err
	}
}

// The Lease is released only after act returns, so no write arrives
// once another copy can lead. A shutdown does not make the election
// exit the process.
func TestTheLeaseIsReleasedOnlyAfterTheOperatorStops(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, startFakeAPI(t).client, "equipment-operator-a")
		holderWhileActing := ""
		finish := leading(t, leader, func(*Client) error {
			holderWhileActing = server.Holder()
			return nil
		})

		led, err := finish()

		mustSucceed(t, err)
		mustMatch(t, led, true)
		mustMatch(t, holderWhileActing, leader.Identity())
		mustMatch(t, server.Holder(), "")
		mustMatch(t, leader.exited(), false)
	})
}

// An operator whose writes did not stop in time keeps the Lease, so no
// other copy leads while a write may still arrive.
func TestAStillWritingOperatorKeepsTheLease(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := newCandidate(t, server, startFakeAPI(t).client, "equipment-operator-a")
		finish := leading(t, leader, func(*Client) error { return errStillWriting })

		led, err := finish()

		mustMatch(t, led, true)
		mustMatch(t, err, errStillWriting)
		mustMatch(t, server.Holder(), leader.Identity())
	})
}

// serve returns only after the last Event write has returned, so the
// Lease is released after the Events of the last pass are written.
func TestServeReturnsAfterTheRecordersLastWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		receiver := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", receiver.address()))
		api.gateEvents()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		config := settings{namespace: testNamespace, networkDiscoveryOff: true, dial: testNetwork.dial, listen: testNetwork.bind}
		returned := make(chan error, 1)
		go func() { returned <- serve(ctx, api.client, config, testMetrics(t)) }()
		api.waitForStatus(t, connected)
		synctest.Wait()

		cancel()
		synctest.Wait()
		select {
		case err := <-returned:
			t.Fatalf("serve returned %v before the Event write in flight finished, want it to keep running", err)
		default:
		}
		api.releaseEvents()

		mustSucceed(t, <-returned)
		mustMatch(t, len(api.events.About("Receiver", "theater")) > 0, true)
	})
}

// A shutdown while the copy waits for the Lease does not run act and
// leaves the current holder's Lease alone.
func TestAStopWhileWaitingRunsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		server.HoldAs("equipment-operator-old")
		waiting := newCandidate(t, server, startFakeAPI(t).client, "equipment-operator-new")
		stop, cancel := context.WithCancel(context.Background())
		cancel()
		acted := false

		led, err := waiting.actWhileLeading(stop, func(*Client) error {
			acted = true
			return nil
		})

		mustSucceed(t, err)
		mustMatch(t, led, false)
		mustMatch(t, acted, false)
		mustMatch(t, server.Holder(), "equipment-operator-old")
		mustMatch(t, waiting.exited(), false)
	})
}

// Once the last renewal is election.RenewDeadline old, the client that
// act receives refuses every write without sending it. That is before
// another copy can take the Lease, so a leader that cannot renew does
// not write over a new leader.
func TestTheOperatorWritesOnlyWhileItsLeaseIsFresh(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		api := startFakeAPI(t)
		leader := newCandidate(t, server, api.client, "equipment-operator-a")
		var fresh, stale error
		finish := leading(t, leader, func(client *Client) error {
			_, fresh = ApplyReceiverPower(client, "living-room", equipment.PowerOn)
			server.Refuse(http.MethodPut, http.StatusInternalServerError)
			time.Sleep(election.RenewDeadline)
			_, stale = ApplyReceiverPower(client, "living-room", equipment.PowerOff)
			return nil
		})

		_, err := finish()

		mustSucceed(t, err)
		mustSucceed(t, fresh)
		mustMatch(t, errors.Is(stale, election.ErrNotLeading), true)
		mustMatch(t, len(api.powers), 1)
		mustMatch(t, <-api.powers, equipment.PowerOn)
	})
}

// memoryListener never receives a connection. Accept blocks on a
// channel instead of a socket, because a goroutine blocked on a socket
// stops the synctest bubble's clock.
type memoryListener struct {
	closed chan struct{}
	once   sync.Once
}

func (l *memoryListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *memoryListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *memoryListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9260} }

// A copy that waits for the Lease binds no port, searches for no
// device, and lists nothing from the API server. Once it takes the
// Lease it does all of these.
func TestAWaitingCopyDoesNothingUntilItLeads(t *testing.T) {
	var searches, binds atomic.Int32
	restoreDiscover, restoreListen := discover, listen
	discover = func(context.Context, time.Duration) []wiim.Device {
		searches.Add(1)
		return nil
	}
	listen = func(string, string) (net.Listener, error) {
		binds.Add(1)
		return &memoryListener{closed: make(chan struct{})}, nil
	}
	t.Cleanup(func() { discover, listen = restoreDiscover, restoreListen })
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		api.setReceivers()
		server := newLeaseServer()
		old := newCandidate(t, server, api.client, "equipment-operator-old")
		synctest.Wait()
		if !old.Leads() {
			t.Fatal("the old copy did not take a Lease that nobody holds")
		}
		waiting := newCandidate(t, server, api.client, "equipment-operator-new")
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := waiting.actWhileLeading(ctx, func(client *Client) error {
				return operateAsLeader(ctx, client, settings{namespace: testNamespace, metricsAddress: ":9260", dial: testNetwork.dial})
			})
			done <- err
		}()

		time.Sleep(2 * election.Duration)
		synctest.Wait()
		api.mutex.Lock()
		listsWhileWaiting := api.lists
		api.mutex.Unlock()
		mustMatch(t, listsWhileWaiting, 0)
		mustMatch(t, searches.Load(), 0)
		mustMatch(t, binds.Load(), 0)

		old.StepDown()
		// client-go waits up to (1 + JitterFactor) retry periods, which is
		// 2.2, between tries to take the Lease.
		time.Sleep(3 * election.RetryPeriod)
		synctest.Wait()
		api.mutex.Lock()
		lists := api.lists
		api.mutex.Unlock()
		mustMatch(t, waiting.Leads(), true)
		mustMatch(t, lists > 0, true)
		mustMatch(t, searches.Load() > 0, true)
		mustMatch(t, binds.Load(), 1)

		cancel()
		mustSucceed(t, <-done)
		mustMatch(t, server.Holder(), "")
	})
}
