package main

// The heartbeat's timer, the check of the sysctls, and the backstop,
// through the loop in a synctest bubble.

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// leaseLog records when the heartbeat lease was written, and whether
// the lease or the Machine's status was written first.
type leaseLog struct {
	next  http.Handler
	mu    sync.Mutex
	at    []time.Time
	first string
}

func (h *leaseLog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.mu.Lock()
		switch {
		case strings.Contains(r.URL.Path, "/leases"):
			h.at = append(h.at, time.Now())
			if h.first == "" {
				h.first = "lease"
			}
		case strings.HasSuffix(r.URL.Path, "/status") && h.first == "":
			h.first = "status"
		}
		h.mu.Unlock()
	}
	h.next.ServeHTTP(w, r)
}

// writesBetween counts the lease writes in (from, to].
func (h *leaseLog) writesBetween(from, to time.Time) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, at := range h.at {
		if at.After(from) && !at.After(to) {
			n++
		}
	}
	return n
}

// longestGap answers the longest time between two lease writes, or
// between from or to and the nearest write.
func (h *leaseLog) longestGap(from, to time.Time) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	longest, last := time.Duration(0), from
	for _, at := range append(append([]time.Time{}, h.at...), to) {
		longest = max(longest, at.Sub(last))
		last = at
	}
	return longest
}

func (h *leaseLog) firstWrite() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.first
}

// A loop that stays busy past stuckAfter stops renewing the lease,
// fails /healthz, and ends the process, so the kubelet starts it again.
// Until the limit, the lease still renews, because a slow pass is not a
// stuck one. Here the facts
// watch's Sync blocks, the way a call that never answers would.
func TestAStuckLoopStopsTheLeaseAndFailsTheCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
		api := &leaseLog{next: newPassAPI()}
		l, _ := testLoop(t, api)
		wake, block := make(chan struct{}, 1), make(chan struct{})
		var syncs atomic.Int64
		l.watchFactsTree = func(context.Context) (*factsWatch, error) {
			return &factsWatch{wake: wake, sync: func() error {
				if syncs.Add(1) == 2 {
					<-block
				}
				return nil
			}}, nil
		}
		var exited atomic.Int64
		var exitedAt atomic.Pointer[time.Time]
		l.live.exit = func(int) {
			if exited.Add(1) == 1 {
				now := time.Now()
				exitedAt.Store(&now)
			}
		}
		startLoop(t, l)
		synctest.Wait()

		wake <- struct{}{}
		synctest.Wait()
		blocked := time.Now()
		time.Sleep(stuckAfter + kubernetes.HeartbeatStaleAfter)
		check := l.live.check()
		whileSlow := api.writesBetween(blocked, blocked.Add(stuckAfter-renewEvery))
		afterLimit := api.writesBetween(blocked.Add(stuckAfter+renewEvery), time.Now())
		close(block)
		synctest.Wait()

		if whileSlow == 0 || afterLimit != 0 || !errors.Is(check, errStuck) {
			t.Errorf("the lease was written %d times before the limit and %d after it, and the check said %v; want some, none, and stuck",
				whileSlow, afterLimit, check)
		}
		if at := exitedAt.Load(); exited.Load() != 1 || at == nil || at.Sub(blocked) <= stuckAfter || at.Sub(blocked) > stuckAfter+renewEvery {
			t.Errorf("the process was ended %d times, at %v after the loop stuck; want once, within %s after %s", exited.Load(), at, renewEvery, stuckAfter)
		}
	})
}

// A parameter that another process changes wakes a pass within
// sysctlCheckEvery, and the pass writes it back. A parameter that reads
// back in another spelling of the same values wakes no pass.
func TestTheSysctlCheckWakesAPassOnlyForAChangedValue(t *testing.T) {
	name := slices.Sorted(maps.Keys(machine.OSSysctls))[0]
	value := machine.OSSysctls[name]
	cases := []struct {
		name    string
		changed string
		passes  int64
		want    string
	}{
		{"another value", value + "0", 2, value},
		{"the same value with a trailing tab", value + "\t", 1, value + "\t"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				isolatePass(t)
				seedSysctls(t)
				path := filepath.Join(sysctlRoot, strings.ReplaceAll(name, ".", "/"))
				facts := &fakeFactsWatch{wake: make(chan struct{}, 1)}
				l, _ := testLoop(t, newPassAPI(), facts)
				startLoop(t, l)
				synctest.Wait()

				writeSysctl(t, path, c.changed)
				time.Sleep(sysctlCheckEvery + time.Second)
				synctest.Wait()

				got, _ := os.ReadFile(path)
				if facts.syncs.Load() != c.passes || strings.TrimRight(string(got), "\n") != c.want {
					t.Errorf("%d passes ran and %s reads %q, want %d and %q", facts.syncs.Load(), name, got, c.passes, c.want)
				}
			})
		})
	}
}

func writeSysctl(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A backstop pass that writes anything found a wake the code does not
// send, and reports it: a BackstopRepaired Warning Event that names the
// step, and the counter by step. Here the hosts file changes with no
// inotify event, as if the hosts watch had missed it.
func TestABackstopPassThatRepairsReportsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
		client, recorder, recorded := recordingClient(t, newPassAPI())
		l, _ := testLoop(t, http.NotFoundHandler(), &fakeFactsWatch{wake: make(chan struct{}, 1)})
		l.objects = &reader{client: client, recorder: recorder}
		layer, scrape := observer(t, &fetcher{})
		l.layer = layer
		startLoop(t, l)
		synctest.Wait()
		quiet := posted(recorded)

		if err := os.WriteFile(hostsPath, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(backstopEvery + time.Second)

		repaired := posted(recorded)[len(quiet):]
		if len(repaired) != 1 || !strings.HasPrefix(repaired[0], "Warning BackstopRepaired: ") || !strings.HasSuffix(repaired[0], ": writing the host entries") {
			t.Errorf("the backstop pass posted %q, want one BackstopRepaired Warning that names the host entries", repaired)
		}
		if !strings.Contains(scrape(), `liken_machine_backstop_repairs_total{step="writing the host entries"} 1`) {
			t.Errorf("the repair was not counted:\n%s", scrape())
		}
	})
}

// A backstop pass on a settled machine writes nothing and reports
// nothing.
func TestABackstopPassOnASettledMachineReportsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
		client, recorder, recorded := recordingClient(t, newPassAPI())
		l, _ := testLoop(t, http.NotFoundHandler(), &fakeFactsWatch{wake: make(chan struct{}, 1)})
		l.objects = &reader{client: client, recorder: recorder}
		startLoop(t, l)
		synctest.Wait()
		quiet := len(posted(recorded))

		time.Sleep(3*backstopEvery + time.Second)

		if got := posted(recorded)[quiet:]; len(got) != 0 {
			t.Errorf("the backstop passes posted %q, want nothing", got)
		}
	})
}

// The check reads the loop's busy mark: a loop that waits, or one busy
// for less than stuckAfter, passes, and one busy for longer fails.
func TestTheLivenessCheckFailsOnlyPastTheLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		live := newLiveness()
		waiting := live.check()
		live.markBusy()
		time.Sleep(stuckAfter - time.Second)
		slow := live.check()
		time.Sleep(2 * time.Second)
		stuck := live.check()

		if waiting != nil || slow != nil || !errors.Is(stuck, errStuck) {
			t.Errorf("waiting = %v, busy %s = %v, busy %s = %v; want nil, nil, stuck",
				waiting, stuckAfter-time.Second, slow, stuckAfter+time.Second, stuck)
		}
	})
}

// A backstop that fires while another cause is ready gives the pass to
// that cause, so a write it makes is not reported as a missed wake.
func TestABackstopGivesThePassToACauseThatIsReady(t *testing.T) {
	dir := t.TempDir()
	saved := sysctlRoot
	t.Cleanup(func() { sysctlRoot = saved })
	sysctlRoot = dir
	writeSysctl(t, filepath.Join(dir, "kernel", "test_value"), "1")
	cases := []struct {
		name    string
		pending bool
		applied string
		want    string
	}{
		{"nothing else ready", false, "1", causeBackstop},
		{"a watch's wake waiting", true, "1", causeWatch},
		{"a sysctl that drifted", false, "0", causeSysctls},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wakes := make(chan struct{}, 1)
			if c.pending {
				wakes <- struct{}{}
			}
			l := &loop{wakes: wakes}

			got := l.backstopCause(waitSources{}, sysctlCheck{applied: map[string]string{"kernel.test_value": c.applied}})

			if got != c.want {
				t.Errorf("the pass's cause is %q, want %q", got, c.want)
			}
		})
	}
}

// refusingStatusWith answers every status write with one status.
type refusingStatusWith struct {
	next   http.Handler
	status int
}

func (h refusingStatusWith) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/status") {
		http.Error(w, "refused", h.status)
		return
	}
	h.next.ServeHTTP(w, r)
}

// While a retry is due, no backstop pass runs: the retry's pass does the
// same work, and a write it makes once somebody fixes the cause, such as
// an RBAC grant for a 403, is the retry's, not a missed wake.
func TestNoBackstopRunsWhileARetryIsDue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
		l, _ := testLoop(t, refusingStatusWith{next: newPassAPI(), status: http.StatusForbidden}, &fakeFactsWatch{wake: make(chan struct{}, 1)})
		// The retry's ceiling of five minutes, with its most jitter,
		// comes after the backstop's five minutes with none, so a
		// backstop armed beside the retry would fire first.
		l.retry.jitter = func() float64 { return 0.99 }
		layer, scrape := observer(t, &fetcher{})
		l.layer = layer

		runLoop(t, l, 30*time.Minute)

		if strings.Contains(scrape(), `liken_machine_passes_total{cause="backstop"}`) || !strings.Contains(scrape(), `liken_machine_passes_total{cause="retry"}`) {
			t.Errorf("want retry passes and no backstop pass:\n%s", scrape())
		}
	})
}

// A Sync of the facts watch that fails leaves a subtree with no watch,
// so the pass records the failure and the retry runs Sync again within
// a second or two.
func TestAFailedSyncOfTheFactsWatchIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
		l, _ := testLoop(t, newPassAPI())
		var syncs atomic.Int64
		l.watchFactsTree = func(context.Context) (*factsWatch, error) {
			return &factsWatch{wake: make(chan struct{}, 1), sync: func() error {
				if syncs.Add(1) == 1 {
					return errors.New("no space for another watch")
				}
				return nil
			}}, nil
		}

		runLoop(t, l, 2*time.Second)

		if got := syncs.Load(); got != 2 {
			t.Errorf("Sync ran %d times in two seconds after a failure, want 2", got)
		}
	})
}
