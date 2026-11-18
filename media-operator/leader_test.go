//go:build !pod

package main

// These tests cover how the operator uses kubernetes/election. The
// tests of kubernetes/election cover the election itself. These tests
// run against the fake Lease server of electiontest in a synctest
// bubble.

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

// candidate is one operator process under test, with the log lines it
// wrote.
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

// newLeader is a candidate that takes a Lease that nobody held.
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
	// StepDown ends the election goroutines, so the synctest bubble can
	// end. A second StepDown in a test writes nothing.
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

// A shutdown stops the bus session while this process still holds the
// Lease, and releases the Lease afterwards, so the operator handles no
// bus message once another copy can lead.
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

// When the bus session does not stop in time, the process keeps the
// Lease, so no other copy leads while the session can still write.
func TestAStepDownWhoseBusDoesNotStopKeepsTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := electiontest.New(leaseName, testLeaseNamespace)
		leader := newLeader(t, server)

		leader.stepDown(func() bool { return false }, nil)

		mustMatch(t, server.Holder(), leader.Identity())
		lines := leader.reported()
		mustMatch(t, lines[len(lines)-1],
			"media.liken.sh: the bus session did not stop, so the Lease is left to expire")
	})
}

// heldEvents is an Event recorder whose API server blocks each write
// until the test closes through, and records the Lease holder when each
// write arrives.
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

// A shutdown releases the Lease only after the recorder's Event write
// completes, so no Event from this copy is written while another copy
// leads.
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

// When the recorder's last write does not return in time, the process
// keeps the Lease, so the write can complete before another copy
// leads.
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
			"media.liken.sh: the Event recorder's last write had not returned: context deadline exceeded, so the Lease is left to expire")
	})
}

// The election's log lines have the operator's prefix and name the
// Lease in the operator's namespace.
func TestTheElectionReportsWithTheOperatorsPrefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leader := newLeader(t, electiontest.New(leaseName, testLeaseNamespace))

		mustMatch(t, leader.reported()[0],
			"media.liken.sh: holding Lease liken-system/media-operator as "+leader.Identity())
	})
}

// The guarded client sends writes while this copy leads and sends none
// after stepDown. Reads still go through.
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
