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
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/election/electiontest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
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
		}, nil)

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

		leader.stepDown(func() bool { return false }, nil)
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
	api := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-h.through
		h.mu.Lock()
		h.holders = append(h.holders, server.Holder())
		h.mu.Unlock()
		recorded.ServeHTTP(w, r)
	}))
	client := apiclient.New(apiservertest.Host, api.Client(), "")
	h.recorder = events.New(context.Background(), client, eventComponent, events.Options{})
	return h
}

func (h *heldEvents) landedWhileHeldBy() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.holders)
}

var movies = events.ObjectReference{Kind: "Library", Namespace: "house", Name: "movies"}

// A shutdown waits for the Event the recorder is writing, and releases
// the Lease after it lands, so no Event of this copy lands while another
// copy leads.
func TestAStepDownReleasesTheLeaseAfterTheRecordersLastWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := holdingCandidate(t, server, "library-operator-a")
		held := holdEvents(t, server)
		held.recorder.Normal(movies, "ScanStarted", "Started a scan.")
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

		if holderWhileWriting != leader.Identity() {
			t.Errorf("holder while the Event was in flight = %q, want %q", holderWhileWriting, leader.Identity())
		}
		if landed := held.landedWhileHeldBy(); !slices.Equal(landed, []string{leader.Identity()}) {
			t.Errorf("the Events landed while the Lease named %q, want only %q", landed, leader.Identity())
		}
		if server.Holder() != "" {
			t.Errorf("holder after the step down = %q, want the Lease released", server.Holder())
		}
	})
}

// A recorder whose last write does not return in time keeps the Lease,
// so the write can land before another copy leads.
func TestAStepDownWhoseRecorderDoesNotStopKeepsTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newLeaseServer()
		leader := holdingCandidate(t, server, "library-operator-a")
		held := holdEvents(t, server)
		held.recorder.Normal(movies, "ScanStarted", "Started a scan.")
		synctest.Wait()

		leader.stepDown(func() bool { return true }, held.recorder)
		close(held.through)
		synctest.Wait()

		if server.Holder() != leader.Identity() {
			t.Errorf("holder = %q, want the Lease still held by %q", server.Holder(), leader.Identity())
		}
		want := "library.liken.sh: the Event recorder's last write had not returned: context deadline exceeded; leaving the Lease to expire"
		if lines := leader.lineLog(); !slices.Contains(lines, want) {
			t.Errorf("lines = %q, want %q", lines, want)
		}
	})
}
