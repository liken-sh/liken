package election_test

// These tests cover what the package adds to client-go's election.
// They run in synctest bubbles against the fake API server of
// electiontest.

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

// nextTry is the longest interval between two reads of the Lease by a
// waiting replica: one retry period plus client-go's jitter of up to
// 1.2 retry periods.
var nextTry = election.RetryPeriod +
	time.Duration(leaderelection.JitterFactor*float64(election.RetryPeriod))

func newServer() *electiontest.Server {
	return electiontest.New(leaseName, leaseNamespace)
}

// candidate is one operator process under test. exitCode is the code
// that the process passed to Exit, or -1 before it exits, and exits
// closes when it exits.
type candidate struct {
	*election.Lead
	exitCode atomic.Int32
	exits    chan struct{}
}

func (c *candidate) exited() bool { return c.exitCode.Load() >= 0 }

func newCandidate(t *testing.T, server *electiontest.Server, pod string) *candidate {
	t.Helper()
	c := &candidate{exits: make(chan struct{})}
	c.exitCode.Store(-1)
	// Report lines are kept and logged in the cleanup, because
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
	})
	if err != nil {
		t.Fatalf("creating the election: %v", err)
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

// leadsAtOnce reports whether the candidate holds the Lease after the
// election has done all it can without the fake clock advancing.
func leadsAtOnce(c *candidate) bool {
	synctest.Wait()
	return c.Leads()
}

// awaitWithin runs Await with a timeout, and reports whether the
// candidate took the Lease within it.
func awaitWithin(c *candidate, limit time.Duration) bool {
	stop, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return c.Await(stop)
}

// A step down releases the Lease and does not count as a loss.
func TestAStepDownReleasesTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		leader := newCandidate(t, server, "an-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the replica did not take a Lease that nobody holds")
		}

		leader.StepDown()

		if server.Holder() != "" {
			t.Errorf("holder after the step down = %q, want the Lease released", server.Holder())
		}
		if leader.exited() {
			t.Errorf("a step down exited the process with code %d, want no exit", leader.exitCode.Load())
		}
		if err := leader.MayWrite(); err == nil {
			t.Error("MayWrite allows a write after the step down, want ErrNotLeading")
		}
	})
}

// The API server can commit a renewal after client-go's release has
// read the Lease, and then the release's update conflicts. The step down still
// releases the Lease.
func TestAStepDownReleasesTheLeaseAfterALateRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		leader := newCandidate(t, server, "an-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the replica did not take a Lease that nobody holds")
		}

		server.RewriteAfterNextRead()
		leader.StepDown()

		if server.Holder() != "" {
			t.Errorf("holder after the step down = %q, want the Lease released", server.Holder())
		}
	})
}

// A waiting replica takes a released Lease on its next read, well
// inside the Lease duration, so a rolling update does not wait out the
// duration.
func TestAWaitingReplicaTakesAReleasedLeaseOnItsNextRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		old := newCandidate(t, server, "an-operator-old")
		if !leadsAtOnce(old) {
			t.Fatal("the old replica did not take a Lease that nobody holds")
		}
		replacement := newCandidate(t, server, "an-operator-new")
		time.Sleep(2 * election.Duration)
		synctest.Wait()
		if replacement.Leads() {
			t.Fatal("the new replica took a Lease the old replica holds")
		}

		old.StepDown()

		if !awaitWithin(replacement, nextTry) {
			t.Fatalf("the new replica did not take the released Lease within %s, its longest wait between tries", nextTry)
		}
	})
}

func TestALeaderWhoseLeaseAnotherProcessTookExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		leader := newCandidate(t, server, "an-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the replica did not take a Lease that nobody holds")
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

// A stop while the replica waits ends the wait. The replica takes no
// Lease, leaves the holder's Lease alone, and does not call Exit.
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

// The leader renews from the resourceVersion it wrote last, so a
// steady leader sends one update each retry period and does not read
// the Lease first.
func TestASteadyLeaderRenewsWithNoRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newServer()
		leader := newCandidate(t, server, "an-operator-a")
		if !leadsAtOnce(leader) {
			t.Fatal("the replica did not take a Lease that nobody holds")
		}
		reads, updates := server.Count(http.MethodGet), server.Count(http.MethodPut)

		time.Sleep(5 * election.RetryPeriod)
		synctest.Wait()

		if sent, read := server.Count(http.MethodPut)-updates, server.Count(http.MethodGet)-reads; sent != 5 || read != 0 {
			t.Errorf("%d updates and %d reads in five retry periods of the leader, want 5 updates and no read", sent, read)
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

func TestTheWriteGuardAllowsOnlyAFreshLeader(t *testing.T) {
	cases := []struct {
		name      string
		leading   bool
		stepping  bool
		renewedAt time.Duration
		allowed   bool
	}{
		{name: "a waiting replica", leading: false, renewedAt: 0, allowed: false},
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

// When the API server refuses the release, a step down leaves this
// replica as the holder, and still returns.
func TestAStepDownWhoseReleaseIsRefusedLeavesTheLeaseToExpire(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newServer()
				leader := newCandidate(t, server, "an-operator-a")
				if !leadsAtOnce(leader) {
					t.Fatal("the replica did not take a Lease that nobody holds")
				}
				server.Refuse(method, http.StatusInternalServerError)

				leader.StepDown()

				if server.Holder() != leader.Identity() || leader.exited() {
					t.Errorf("holder = %q, exit = %d, want the Lease still held and no exit",
						server.Holder(), leader.exitCode.Load())
				}
			})
		})
	}
}

// A restarted replica takes the Lease of an earlier process of its pod
// as soon as the last renewal is one Lease duration old. When another
// pod holds the Lease, or the renewal is newer, client-go waits one
// whole duration from the replica's first read. So one second before
// the duration ends, the replica has taken only an expired Lease of its
// own pod.
func TestARestartedReplicaTakesAnExpiredLeaseOfAnEarlierProcessOfItsPodWithoutWaitingTheDuration(t *testing.T) {
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
		// The Lease expires half a duration after the start,
		// and the replica takes it on its next read after that.
		{name: "this pod, expiring while this process waits", holder: "an-operator-a_0badc0de", renewed: election.Duration / 2, taken: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newServer()
				server.HoldAsRenewedAt(c.holder, time.Now().Add(-c.renewed))

				restarted := newCandidate(t, server, "an-operator-a")

				if taken := awaitWithin(restarted, election.Duration-time.Second); taken != c.taken {
					t.Errorf("the Lease taken one second before its duration ends = %v, want %v", taken, c.taken)
				}
			})
		})
	}
}

// The release clears an expired Lease of an earlier process of this
// pod, which the elector has not taken yet, so a waiting replica in
// another pod does not wait out the duration.
func TestTheReleaseClearsAnExpiredLeaseOfAnEarlierProcessOfThisPod(t *testing.T) {
	cases := []struct {
		name    string
		holder  string
		renewed time.Duration
		free    bool
	}{
		{name: "this pod, renewed long ago", holder: "an-operator-a_0badc0de", renewed: 3 * election.Duration, free: true},
		{name: "this pod, renewed within the duration", holder: "an-operator-a_0badc0de", renewed: 0, free: false},
		{name: "another pod, renewed long ago", holder: "an-operator-b_0badc0de", renewed: 3 * election.Duration, free: false},
		{name: "a pod whose name starts with this pod's", holder: "an-operator-a-2_0badc0de", renewed: 3 * election.Duration, free: false},
		{name: "no holder stays free", holder: "", renewed: 3 * election.Duration, free: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := newServer()
			server.HoldAsRenewedAt(c.holder, time.Now().Add(-c.renewed))
			stopped, err := election.New(server.Config(t), election.Options{
				Name: leaseName, Namespace: leaseNamespace, Pod: "an-operator-a",
			})
			if err != nil {
				t.Fatalf("creating the election: %v", err)
			}

			election.ClearIfHeld(stopped, context.Background())

			if free := server.Holder() == ""; free != c.free {
				t.Errorf("holder after the release = %q, want a Lease with no holder %v", server.Holder(), c.free)
			}
		})
	}
}

// A release writes nothing when the Lease does not exist or the read of
// it fails.
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
				t.Fatalf("creating the election: %v", err)
			}

			election.ClearIfHeld(stopped, context.Background())

			if writes := server.Count(http.MethodPut); writes != 0 {
				t.Errorf("%d writes of the Lease, want none", writes)
			}
		})
	}
}

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
				t.Error("New returned no error for options with no name, namespace, or pod, want one")
			}
		})
	}
}

func TestStartReturnsALeadOrErrStopped(t *testing.T) {
	cases := []struct {
		name   string
		holder string
		err    error
	}{
		{name: "a free Lease", err: nil},
		{name: "a Lease another replica holds", holder: "an-operator-b_0badc0de", err: election.ErrStopped},
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
					t.Fatalf("Start = %v, %v, want the error %v", lead, err, c.err)
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
		t.Error("Start returned no error for options with no name, namespace, or pod, want one")
	}
}
