package main

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

// leaseLog records the time of each heartbeat lease write, and whether
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

// writesBetween counts the lease writes after from, up to and
// including to.
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

// longestGap returns the longest time between two consecutive lease
// writes, counting from and to as the ends of the interval.
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

// A loop that stays busy for longer than stuckAfter stops renewing the
// lease, fails /healthz, and exits the process so the kubelet starts it
// again. Before that limit the lease still renews, because a slow pass
// is not a stuck one. The test blocks the facts watch's sync during a
// pass, as a call that never returns would.
func TestAStuckLoopStopsTheLeaseAndFailsTheCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
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

// When another process changes a sysctl, a pass runs within
// sysctlCheckEvery and writes the value back. A sysctl that reads back
// with the same values in a different spelling does not start a pass.
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
				writeAppliedSysctls(t)
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
		t.Fatalf("creating the directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// When a backstop pass writes anything, some change sent no wake
// signal, so the loop reports it with a BackstopRepaired Warning Event
// that names the step, and with a counter for each step. The test
// changes the hosts file without an inotify event, as if the hosts
// watch had missed it.
func TestABackstopPassThatRepairsReportsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		client, recorder, recorded := recordingClient(t, newPassAPI())
		l, _ := testLoop(t, http.NotFoundHandler(), &fakeFactsWatch{wake: make(chan struct{}, 1)})
		l.objects = &reader{client: client, recorder: recorder}
		layer, scrape := observer(t, &fetcher{})
		l.layer = layer
		startLoop(t, l)
		synctest.Wait()
		quiet := posted(recorded)

		if err := os.WriteFile(hostsPath, nil, 0o644); err != nil {
			t.Fatalf("emptying %s: %v", hostsPath, err)
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

// A backstop pass on a machine that is already in its declared state
// writes nothing and reports nothing.
func TestABackstopPassOnASettledMachineReportsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
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

// The check passes for a loop that is waiting or has been busy for less
// than stuckAfter, and fails for a loop busy for longer.
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

// When the backstop timer fires while another cause for a pass is
// ready, the pass runs for that cause, so a write it makes is not
// reported as a missing wake signal.
func TestABackstopTimerYieldsToACauseThatIsReady(t *testing.T) {
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

// refusingStatusWith responds to every status write with the same
// status code.
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

// While a retry is pending, no backstop pass runs. The retry's pass
// does the same work, and a write it makes after someone fixes the
// cause, such as an RBAC grant after a 403, belongs to the retry and
// is not a missing wake signal.
func TestNoBackstopRunsWhileARetryIsDue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		l, _ := testLoop(t, refusingStatusWith{next: newPassAPI(), status: http.StatusForbidden}, &fakeFactsWatch{wake: make(chan struct{}, 1)})
		// With jitter near its maximum, a retry at its five-minute limit
		// fires after backstopEvery, so a backstop timer armed beside
		// the retry would fire first.
		l.retry.jitter = func() float64 { return 0.99 }
		layer, scrape := observer(t, &fetcher{})
		l.layer = layer

		runLoop(t, l, 30*time.Minute)

		if strings.Contains(scrape(), `liken_machine_passes_total{cause="backstop"}`) || !strings.Contains(scrape(), `liken_machine_passes_total{cause="retry"}`) {
			t.Errorf("want retry passes and no backstop pass:\n%s", scrape())
		}
	})
}

// When the facts watch's sync fails, a subtree has no watch, so the
// pass records the failure and the retry calls sync again within a
// second or two.
func TestAFailedSyncOfTheFactsWatchIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
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
