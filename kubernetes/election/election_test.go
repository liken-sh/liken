package election_test

// These tests cover what the package adds to client-go's election: the
// write guard, the release on a shutdown, the exit on a lost Lease, the
// prompt takeover a release gives a waiting copy, and the take of an
// expired Lease of an earlier process of the same pod. They run in
// synctest bubbles against the fake API server of electiontest.
// synctest.Wait returns once the elector has done all it can at the
// present moment, and a time.Sleep waits out a retry period or a Lease
// duration on the fake clock.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"

	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/election/electiontest"
)

const (
	leaseName      = "an-operator"
	leaseNamespace = "liken-system"
)

// nextTry is the longest wait between two tries of a copy that waits
// for the Lease: one retry period, and up to 1.2 retry periods of
// client-go's jitter.
var nextTry = election.RetryPeriod +
	time.Duration(leaderelection.JitterFactor*float64(election.RetryPeriod))

func newServer() *electiontest.Server {
	return electiontest.New(leaseName, leaseNamespace)
}

// candidate is one operator process under test, with the exit code it
// would have ended with, or -1. exits closes when it exits. unelected
// holds the last mode Options.Unelected received.
type candidate struct {
	*election.Lead
	exitCode  atomic.Int32
	exits     chan struct{}
	unelected atomic.Bool
}

func (c *candidate) exited() bool { return c.exitCode.Load() >= 0 }

func newCandidate(t *testing.T, server *electiontest.Server, pod string) *candidate {
	return newCandidateActing(t, server, pod, false)
}

// newCandidateActing is a candidate that acts without an election while
// the API server refuses the Lease, when refused is true.
func newCandidateActing(t *testing.T, server *electiontest.Server, pod string, refused bool) *candidate {
	t.Helper()
	c := &candidate{exits: make(chan struct{})}
	c.exitCode.Store(-1)
	// The lines go to a buffer the test prints when it ends, because
	// client-go can call back after the test's last assertion.
	var lines sync.Mutex
	var reported []string
	var exit sync.Once
	l, err := election.New(server.Config(t), election.Options{
		Name: leaseName, Namespace: leaseNamespace, Pod: pod,
		Report: func(line string) {
			lines.Lock()
			defer lines.Unlock()
			reported = append(reported, line)
		},
		Exit: func(code int) {
			exit.Do(func() {
				c.exitCode.Store(int32(code))
				close(c.exits)
			})
		},
		ActWhenRefused: refused,
		Unelected:      c.unelected.Store,
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Lead = l
	c.Run()
	t.Cleanup(func() {
		select {
		case <-c.Halt():
		case <-time.After(election.Duration):
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
	return c.Leads()
}

// awaitWithin runs Await with a deadline, and answers whether the
// candidate may act.
func awaitWithin(c *candidate, spell time.Duration) bool {
	stop, cancel := context.WithTimeout(context.Background(), spell)
	defer cancel()
	return c.Await(stop)
}

// A shutdown releases the Lease, and it is not a loss.
func TestAStepDownReleasesTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		leader := newCandidate(t, server, "an-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the copy did not take a Lease that nobody holds")
		}

		leader.StepDown()

		if server.Holder() != "" {
			t.Errorf("holder after the step down = %q, want the Lease released", server.Holder())
		}
		if leader.exited() {
			t.Errorf("a shutdown exited with %d", leader.exitCode.Load())
		}
		if err := leader.MayWrite(); err == nil {
			t.Error("a copy that stepped down still allows writes")
		}
		stop, cancel := context.WithTimeout(t.Context(), election.RetryPeriod)
		defer cancel()
		if leader.MayAct(stop) {
			t.Error("a copy that stepped down may still act")
		}
	})
}

// A renewal the step down cancelled can still land on the API server
// after client-go's release read the Lease, and the release then fails
// with a conflict. The step down still leaves the Lease released, so a
// waiting copy does not wait out the Lease's duration.
func TestAStepDownReleasesTheLeaseAfterALateRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		leader := newCandidate(t, server, "an-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the copy did not take a Lease that nobody holds")
		}

		server.LandAWriteAfterTheNextRead()
		leader.StepDown()

		if server.Holder() != "" {
			t.Errorf("holder after the step down = %q, want the Lease released", server.Holder())
		}
	})
}

// A waiting copy takes a released Lease on its next try, well inside
// the Lease's duration. This is what makes a rolling update fast: the
// new pod waits beside the old one, and takes over when the old one
// steps down.
func TestAWaitingCopyTakesAReleasedLeaseAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		old := newCandidate(t, server, "an-operator-old")
		if !leadsAtOnce(old) {
			t.Fatal("the old copy did not take a Lease that nobody holds")
		}
		replacement := newCandidate(t, server, "an-operator-new")
		time.Sleep(2 * election.Duration)
		synctest.Wait()
		if replacement.Leads() {
			t.Fatal("the new copy took a Lease the old copy holds")
		}

		old.StepDown()

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
// renewal.
func TestALeaderThatCannotRenewExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		leader := newCandidate(t, server, "an-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the copy did not take a Lease that nobody holds")
		}
		renewed := leader.LastRenewal()

		server.Refuse(http.MethodPut, http.StatusInternalServerError)

		time.Sleep(election.RenewDeadline)
		synctest.Wait()
		if err := leader.MayWrite(); err == nil || leader.exited() {
			t.Errorf("one renewal deadline after the last renewal, MayWrite = %v and exited = %v; want writes refused before the exit",
				err, leader.exited())
		}
		select {
		case <-leader.exits:
		case <-time.After(election.Duration):
			t.Fatal("the leader kept acting with no renewal")
		}
		if exit, limit := time.Since(renewed), election.Duration-election.RetryPeriod; exit > limit {
			t.Errorf("the leader exited %s after its last renewal, want at most %s", exit, limit)
		}
		if err := leader.MayWrite(); err == nil {
			t.Error("a leader that exited still allows writes")
		}
		if code := leader.exitCode.Load(); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	})
}

func TestALeaderWhoseLeaseAnotherProcessTookExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		leader := newCandidate(t, server, "an-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the copy did not take a Lease that nobody holds")
		}
		renewed := leader.LastRenewal()

		server.HoldAs("an-operator-b")

		select {
		case <-leader.exits:
		case <-time.After(election.Duration):
			t.Fatal("the leader kept acting after another process took the Lease")
		}
		if exit, limit := time.Since(renewed), election.Duration-election.RetryPeriod; exit > limit {
			t.Errorf("the leader exited %s after its last renewal, want at most %s", exit, limit)
		}
	})
}

// A shutdown that arrives while the copy waits ends the wait with no
// Lease and no exit code of its own.
func TestAStopWhileWaitingLeavesTheHolderAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		server.HoldAs("an-operator-old")
		waiting := newCandidate(t, server, "an-operator-new")
		stop, cancel := context.WithCancel(context.Background())
		cancel()

		if waiting.Await(stop) {
			t.Fatal("Await reported a Lease after the stop")
		}
		if server.Holder() != "an-operator-old" || waiting.exited() {
			t.Errorf("holder = %q, exit = %d, want the old holder and no exit",
				server.Holder(), waiting.exitCode.Load())
		}
	})
}

// The leader renews from the version it wrote last, so a steady leader
// sends one update each retry period and does not read the Lease
// before each one.
func TestASteadyLeaderRenewsWithNoRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		leader := newCandidate(t, server, "an-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the copy did not take a Lease that nobody holds")
		}
		reads, updates := server.Count(http.MethodGet), server.Count(http.MethodPut)

		time.Sleep(5 * election.RetryPeriod)
		synctest.Wait()

		if sent, read := server.Count(http.MethodPut)-updates, server.Count(http.MethodGet)-reads; sent != 5 || read != 0 {
			t.Errorf("%d updates and %d reads in five retry periods of the lead, want 5 updates and no read", sent, read)
		}
	})
}

func TestTheIdentityIsNewForEachProcess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		first := newCandidate(t, server, "an-operator-a")
		second := newCandidate(t, server, "an-operator-a")

		if first.Identity() == second.Identity() || !strings.HasPrefix(first.Identity(), "an-operator-a_") {
			t.Errorf("identities = %q and %q, want two identities for one pod", first.Identity(), second.Identity())
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
		{name: "a leader one retry period after its renewal", leading: true, renewedAt: election.RetryPeriod, allowed: true},
		{name: "a leader one renewal deadline after its renewal", leading: true, renewedAt: election.RenewDeadline, allowed: false},
		{name: "a leader that is stepping down", leading: true, stepping: true, renewedAt: 0, allowed: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := election.Guarded(c.leading, c.stepping, time.Now().Add(-c.renewedAt))

			if err := l.MayWrite(); (err == nil) != c.allowed {
				t.Errorf("MayWrite = %v, want allowed %v", err, c.allowed)
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
				server := newServer()
				leader := newCandidate(t, server, "an-operator-a")
				if !leadsAtOnce(leader) {
					t.Fatal("the copy did not take a Lease that nobody holds")
				}
				server.Refuse(method, http.StatusInternalServerError)

				leader.StepDown()

				if server.Holder() != leader.Identity() || leader.exited() {
					t.Errorf("holder = %q, exit = %d; want the Lease still held and no exit",
						server.Holder(), leader.exitCode.Load())
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
		{name: "this pod, renewed long ago", holder: "an-operator-a_0badc0de", renewed: 3 * election.Duration, taken: true},
		{name: "this pod, renewed within the duration", holder: "an-operator-a_0badc0de", renewed: 0, taken: false},
		{name: "another pod, renewed long ago", holder: "an-operator-b_0badc0de", renewed: 3 * election.Duration, taken: false},
		{name: "a pod whose name starts with this pod's", holder: "an-operator-a-2_0badc0de", renewed: 3 * election.Duration, taken: false},
		// The first read finds the Lease current, and a later read finds
		// it expired, before a whole duration from the first read: half
		// a duration, and at most one wait between tries, from the start.
		{name: "this pod, expiring while this process waits", holder: "an-operator-a_0badc0de", renewed: election.Duration / 2, taken: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newServer()
				server.HoldAsRenewedAt(c.holder, time.Now().Add(-c.renewed))

				restarted := newCandidate(t, server, "an-operator-a")

				if taken := awaitWithin(restarted, election.Duration-time.Second); taken != c.taken {
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
		{name: "this pod, renewed long ago", holder: "an-operator-a_0badc0de", renewed: 3 * election.Duration, cleared: true},
		{name: "this pod, renewed within the duration", holder: "an-operator-a_0badc0de", renewed: 0, cleared: false},
		{name: "another pod, renewed long ago", holder: "an-operator-b_0badc0de", renewed: 3 * election.Duration, cleared: false},
		{name: "a pod whose name starts with this pod's", holder: "an-operator-a-2_0badc0de", renewed: 3 * election.Duration, cleared: false},
		{name: "no holder", holder: "", renewed: 3 * election.Duration, cleared: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := newServer()
			server.HoldAsRenewedAt(c.holder, time.Now().Add(-c.renewed))
			stopped, err := election.New(server.Config(t), election.Options{
				Name: leaseName, Namespace: leaseNamespace, Pod: "an-operator-a",
			})
			if err != nil {
				t.Fatal(err)
			}

			election.ClearIfHeld(stopped, context.Background())

			if cleared := server.Holder() == ""; cleared != c.cleared {
				t.Errorf("holder after the release = %q, want cleared %v", server.Holder(), c.cleared)
			}
		})
	}
}

// A release of a Lease that does not exist, or that the API server
// cannot read, writes nothing.
func TestTheReleaseOfALeaseItCannotReadWritesNothing(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{name: "no Lease", status: 0},
		{name: "a read that fails", status: http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := newServer()
			if c.status != 0 {
				server.HoldAs("an-operator-a_0badc0de")
				server.Refuse(http.MethodGet, c.status)
			}
			stopped, err := election.New(server.Config(t), election.Options{
				Name: leaseName, Namespace: leaseNamespace, Pod: "an-operator-a",
			})
			if err != nil {
				t.Fatal(err)
			}

			election.ClearIfHeld(stopped, context.Background())

			if writes := server.Count(http.MethodPut); writes != 0 {
				t.Errorf("%d writes of the Lease, want none", writes)
			}
		})
	}
}

// An election needs the name of its Lease and of its pod.
func TestAnElectionWithoutANameIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		options election.Options
	}{
		{name: "no Lease", options: election.Options{Namespace: leaseNamespace, Pod: "an-operator-a"}},
		{name: "no namespace", options: election.Options{Name: leaseName, Pod: "an-operator-a"}},
		{name: "no pod", options: election.Options{Name: leaseName, Namespace: leaseNamespace}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := election.New(&rest.Config{Host: "https://localhost"}, c.options); err == nil {
				t.Error("New accepted an election with no name")
			}
		})
	}
}

// Start answers a lead that may act, or ErrStopped when the stop comes
// first.
func TestStartAnswersALeadOrTheStop(t *testing.T) {
	cases := []struct {
		name   string
		holder string
		err    error
	}{
		{name: "a free Lease", err: nil},
		{name: "a Lease another copy holds", holder: "an-operator-b_0badc0de", err: election.ErrStopped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newServer()
				if c.holder != "" {
					server.HoldAs(c.holder)
				}
				stop, cancel := context.WithTimeout(t.Context(), election.RetryPeriod)
				defer cancel()

				lead, err := election.Start(stop, server.Config(t), election.Options{
					Name: leaseName, Namespace: leaseNamespace, Pod: "an-operator-a",
				})

				if !errors.Is(err, c.err) || (lead != nil) != (c.err == nil) {
					t.Fatalf("Start = %v, %v; want the error %v", lead, err, c.err)
				}
				if lead != nil {
					lead.StepDown()
				}
			})
		})
	}
}

func TestStartRefusesAnElectionWithoutAName(t *testing.T) {
	if _, err := election.Start(t.Context(), &rest.Config{Host: "https://localhost"}, election.Options{}); err == nil {
		t.Error("Start elected with no name")
	}
}
