//go:build !node

package main

// These tests cover the operator's own use of kubernetes/election: it
// acts only while it holds the Lease, it releases the Lease only after
// its last write, it keeps the Lease when a write can still be in
// flight, and its writes go through the election's write guard. The
// package's tests cover the election itself. These tests run in
// synctest bubbles against the fake Lease server of electiontest.

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

	"github.com/liken-sh/equipment-operator/equipment"
	"github.com/liken-sh/equipment-operator/wiim"
)

const testLeaseNamespace = "liken-system"

func newLeaseServer() *electiontest.Server {
	return electiontest.New(leaseName, testLeaseNamespace)
}

// candidate is one operator process under test, with the exit code it
// would have ended with, or -1.
type candidate struct {
	*leadership
	exitCode atomic.Int32
}

func (c *candidate) exited() bool { return c.exitCode.Load() >= 0 }

// newCandidate starts the election of one process of pod, whose writes
// go to client.
func newCandidate(t *testing.T, server *electiontest.Server, client *Client, pod string) *candidate {
	t.Helper()
	c := &candidate{}
	c.exitCode.Store(-1)
	// The lines go to a buffer the test prints when it ends, because
	// client-go can call back after the test's last assertion.
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
	// The election runs until it ends, and a bubble ends only when every
	// goroutine in it has.
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
// holds until the test releases it, and answers the function that ends
// act and reports what actWhileLeading answered.
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

// The operator acts only while the Lease names this process, and
// releases it only after act has returned, so no write of the operator
// lands once another copy can lead. A shutdown is not a loss.
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
// other copy leads while a write can still land. The Lease expires
// after the process exits.
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

// A shutdown that arrives while the copy waits ends the wait with no
// Lease, runs nothing, and leaves the holder alone.
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

// The client act receives writes while the leader renews its Lease,
// and refuses every write, sending nothing, once the last renewal is
// one renewal deadline old. That comes before a waiting copy can take
// the Lease, so a leader that lost the API server's answers cannot
// write over a new leader.
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

// memoryListener is a listener that no connection reaches. Its Accept
// waits on a channel, so a bubble's clock moves while the metrics
// server waits in it.
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
// device, and sends the API server nothing but its reads of the Lease,
// which the Lease server answers. So it opens no receiver session
// either, because a session starts from a pass. Once it takes the
// Lease, it does all of these, and a shutdown releases the Lease.
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
				return operateAsLeader(ctx, client, settings{metricsAddress: ":9260", dial: testNetwork.dial})
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
		time.Sleep(2 * election.RetryPeriod)
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
