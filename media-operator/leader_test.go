//go:build !pod

package main

// These tests cover how the operator uses kubernetes/election: the
// order of a shutdown, the Lease a bus session that does not stop
// keeps, the prefix of the election's lines, and the write guard on
// the operator's client. The package's own tests cover the election.
// These run against the fake Lease server of electiontest, in a
// synctest bubble, on the fake clock.

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/election/electiontest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

const testLeaseNamespace = "liken-system"

// candidate is one operator process under test, with the lines it
// reported.
type candidate struct {
	*leadership
	lines sync.Mutex
	lined []string
}

func (c *candidate) reported() []string {
	c.lines.Lock()
	defer c.lines.Unlock()
	return append([]string(nil), c.lined...)
}

// newLeader is a candidate that holds the Lease, which nobody held
// before.
func newLeader(t *testing.T, server *electiontest.Server) *candidate {
	t.Helper()
	c := &candidate{}
	report := prefixed(func(line string) {
		c.lines.Lock()
		defer c.lines.Unlock()
		c.lined = append(c.lined, line)
	})
	options := leaseOptions(testLeaseNamespace, "media-operator-a", report)
	// A lost Lease ends the test, never the test binary.
	options.Exit = func(code int) { t.Errorf("the leader exited with %d", code) }
	lead, err := election.New(server.Config(t), options)
	mustSucceed(t, err)
	c.leadership = &leadership{Lead: lead, report: report}
	lead.Run()
	// The step down ends the election, so the bubble has no goroutine
	// left when the test ends. A second step down writes nothing.
	t.Cleanup(func() {
		c.StepDown()
		for _, line := range c.reported() {
			t.Log(line)
		}
	})
	synctest.Wait()
	if !c.Leads() {
		t.Fatal("the copy never took the Lease")
	}
	return c
}

// A shutdown stops the bus session while the Lease still names this
// process, and releases the Lease after, so no bus message is handled
// once another copy can lead.
func TestAStepDownQuietsTheBusBeforeItReleasesTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := electiontest.New(leaseName, testLeaseNamespace)
		leader := newLeader(t, server)
		holderWhileQuieting := ""

		leader.stepDown(func() bool {
			holderWhileQuieting = server.Holder()
			return true
		}, nil)

		mustMatch(t, holderWhileQuieting, leader.Identity())
		mustMatch(t, server.Holder(), "")
	})
}

// A bus session that does not stop in time keeps the Lease, so no
// other copy leads while the session can still write.
func TestAStepDownWhoseBusDoesNotStopKeepsTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := electiontest.New(leaseName, testLeaseNamespace)
		leader := newLeader(t, server)

		leader.stepDown(func() bool { return false }, nil)

		mustMatch(t, server.Holder(), leader.Identity())
		lines := leader.reported()
		mustMatch(t, lines[len(lines)-1],
			"media.liken.sh: the bus session did not stop; leaving the Lease to expire")
	})
}

// heldEvents is an Event recorder whose API server holds each write
// until the test lets it through, and notes the Lease's holder as each
// write lands.
type heldEvents struct {
	recorder *events.Recorder
	through  chan struct{}

	mu      sync.Mutex
	holders []string
}

func holdEvents(t *testing.T, server *electiontest.Server) *heldEvents {
	h := &heldEvents{through: make(chan struct{})}
	recorded := &eventstest.Events{}
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-h.through
		h.mu.Lock()
		h.holders = append(h.holders, server.Holder())
		h.mu.Unlock()
		recorded.ServeHTTP(w, r)
	}))
	h.recorder = events.New(context.Background(), client, operatorComponent, events.Options{})
	return h
}

func (h *heldEvents) landedWhileHeldBy() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.holders...)
}

// A shutdown waits for the Event the recorder is writing, and releases
// the Lease after it lands, so no Event of this copy lands while another
// copy leads.
func TestAStepDownReleasesTheLeaseAfterTheRecordersLastWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := electiontest.New(leaseName, testLeaseNamespace)
		leader := newLeader(t, server)
		held := holdEvents(t, server)
		held.recorder.Normal(playRef(testPlay()), "Started", "Started the run.")
		synctest.Wait()

		stepped := make(chan struct{})
		go func() {
			leader.stepDown(func() bool { return true }, held.recorder)
			close(stepped)
		}()
		synctest.Wait()
		holderWhileWriting := server.Holder()
		close(held.through)
		<-stepped

		mustMatch(t, holderWhileWriting, leader.Identity())
		mustMatchAll(t, held.landedWhileHeldBy(), []string{leader.Identity()})
		mustMatch(t, server.Holder(), "")
	})
}

// A recorder whose last write does not return in time keeps the Lease,
// so the write can land before another copy leads.
func TestAStepDownWhoseRecorderDoesNotStopKeepsTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := electiontest.New(leaseName, testLeaseNamespace)
		leader := newLeader(t, server)
		held := holdEvents(t, server)
		held.recorder.Normal(playRef(testPlay()), "Started", "Started the run.")
		synctest.Wait()
		start := time.Now()

		leader.stepDown(func() bool { return true }, held.recorder)
		waited := time.Since(start)
		close(held.through)
		synctest.Wait()

		mustMatch(t, waited, events.ShutdownTimeout)
		mustMatch(t, server.Holder(), leader.Identity())
		lines := leader.reported()
		mustMatch(t, lines[len(lines)-1],
			"media.liken.sh: the Event recorder's last write had not returned: context deadline exceeded; leaving the Lease to expire")
	})
}

// The election's lines carry the operator's prefix, and name the Lease
// in the operator's namespace.
func TestTheElectionReportsWithTheOperatorsPrefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leader := newLeader(t, electiontest.New(leaseName, testLeaseNamespace))

		mustMatch(t, leader.reported()[0],
			"media.liken.sh: holding Lease liken-system/media-operator as "+leader.Identity())
	})
}

// The operator's client sends writes while this copy leads, and sends
// none once it has stepped down, so a write that a timer starts after
// the bus session ended never reaches the API server.
func TestTheGuardedClientSendsNoWriteAfterTheStepDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leader := newLeader(t, electiontest.New(leaseName, testLeaseNamespace))
		var sent atomic.Int32
		client := leader.guard(testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sent.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
		})))
		write := func() error {
			return client.Request(http.MethodPatch, playPath("den", "film"), mergePatchType, []byte("{}"), nil)
		}
		mustSucceed(t, write())

		leader.stepDown(func() bool { return true }, nil)

		mustMatch(t, errors.Is(write(), election.ErrNotLeading), true)
		mustSucceed(t, client.Request(http.MethodGet, playPath("den", "film"), "", nil, nil))
		mustMatch(t, sent.Load(), int32(2))
	})
}
