package main

// A supplicant is stopped on purpose, never left to the shutdown's
// kill(-1), and the stop has to end even when the process or its loop
// does not cooperate. These tests hold each stop to its bound on the
// fake clock of a synctest bubble.

import (
	"context"
	"os"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"
)

// wirelessSignalled is a supplicant that records each signal it gets
// and dies on the fatal one, with the death reported on died the way
// the reaper reports it.
type wirelessSignalled struct {
	fatal os.Signal
	sent  []os.Signal
	died  chan unix.WaitStatus
}

// wirelessDiesOn builds the stand-in process.
func wirelessDiesOn(fatal os.Signal) *wirelessSignalled {
	return &wirelessSignalled{fatal: fatal, died: make(chan unix.WaitStatus, 1)}
}

// handle is the process as the supervision code holds it.
func (w *wirelessSignalled) handle() runningSupplicant {
	return runningSupplicant{pid: 1_000_100, signal: func(sig os.Signal) error {
		w.sent = append(w.sent, sig)
		if sig == w.fatal {
			w.died <- 0
		}
		return nil
	}}
}

// A supplicant that ignores SIGTERM must get SIGKILL after five
// seconds, or the shutdown would wait on it forever.
func TestStopSupplicantKillsAProcessThatIgnoresSIGTERM(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		proc := wirelessDiesOn(unix.SIGKILL)

		begin := time.Now()
		stopSupplicant("wlan0", proc.handle(), proc.died)

		if got := time.Since(begin); got != 5*time.Second {
			t.Errorf("the stop took %s, want the 5s SIGTERM allowance", got)
		}
		if !slices.Equal(proc.sent, []os.Signal{unix.SIGTERM, unix.SIGKILL}) {
			t.Errorf("signals %v, want SIGTERM then SIGKILL", proc.sent)
		}
	})
}

// wirelessReopensSupplicants empties the supplicant list and reopens
// its latch when the test ends, so the next test can register again.
func wirelessReopensSupplicants(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		supplicantsMu.Lock()
		supplicantsStopping = false
		supplicants = nil
		supplicantsMu.Unlock()
	})
}

// wirelessLoop is a registered supervision loop. A cooperative loop
// ends when it is told to stop; a stuck one never ends.
func wirelessLoop(t *testing.T, ifname string, cooperative bool) *supplicantProcess {
	t.Helper()
	p := &supplicantProcess{ifname: ifname, stop: make(chan struct{}), done: make(chan struct{})}
	if !registerSupplicant(p) {
		t.Fatalf("the list refused %s before any shutdown", ifname)
	}
	if cooperative {
		go func() {
			<-p.stop
			close(p.done)
		}()
	}
	return p
}

// The shutdown must tell every loop to stop, wait at most ten seconds
// for a loop that does not end, and then refuse every later
// supplicant, so a radio that settles during a reboot leaves no
// process behind.
func TestStopSupplicantsEndsEveryLoopWithinItsBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wirelessReopensSupplicants(t)
		quick := wirelessLoop(t, "wlan0", true)
		stuck := wirelessLoop(t, "wlan1", false)

		begin := time.Now()
		stopSupplicants()

		if got := time.Since(begin); got != 10*time.Second {
			t.Errorf("the stop took %s, want the 10s bound on the stuck loop", got)
		}
		for _, p := range []*supplicantProcess{quick, stuck} {
			select {
			case <-p.stop:
			default:
				t.Errorf("the loop on %s was never told to stop", p.ifname)
			}
		}
		late := &supplicantProcess{ifname: "wlan2", stop: make(chan struct{}), done: make(chan struct{})}
		if registerSupplicant(late) {
			t.Error("a supplicant registered after the shutdown")
		}
		if got := len(trackedSupplicants()); got != 0 {
			t.Errorf("the list still holds %d loops", got)
		}
	})
}

// wirelessDetachedLoop is a loop whose event stream is detached, and
// whose supplicant never creates its socket, so each attach waits out
// its one-second patience and fails.
func wirelessDetachedLoop(t *testing.T) *supplicantProcess {
	t.Helper()
	return &supplicantProcess{
		ifname: "wlan0",
		stop:   make(chan struct{}), done: make(chan struct{}),
		backoff: 10 * time.Second, maxBackoff: 30 * time.Second,
		control: &wpaControl{
			socket:   t.TempDir() + "/ctrl/wlan0",
			local:    t.TempDir() + "/client",
			out:      make(chan wpaEvent, 8),
			patience: time.Second,
		},
	}
}

// While the loop retries a detached event stream, a death, a stop, or
// a cancelled plane must each end the wait at once, and only the stop
// and the cancel may signal the process, because a process that died
// on its own has nothing left to signal.
func TestWatchEndsAReattachWhenTheProcessDiesOrTheLoopEnds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		after   time.Duration
		end     func(p *supplicantProcess, cancel context.CancelFunc, died chan unix.WaitStatus)
		want    supplicantOutcome
		signals []os.Signal
	}{
		{
			name: "the process dies while an attach is out",
			end: func(_ *supplicantProcess, _ context.CancelFunc, died chan unix.WaitStatus) {
				died <- 0
			},
			want: supplicantDied,
		},
		{
			name:  "the process dies during the backoff after a failed attach",
			after: 1500 * time.Millisecond,
			end: func(_ *supplicantProcess, _ context.CancelFunc, died chan unix.WaitStatus) {
				died <- 0
			},
			want: supplicantDied,
		},
		{
			name:    "the loop is told to stop",
			end:     func(p *supplicantProcess, _ context.CancelFunc, _ chan unix.WaitStatus) { close(p.stop) },
			want:    supplicantEnded,
			signals: []os.Signal{unix.SIGTERM},
		},
		{
			name:    "the plane is cancelled",
			end:     func(_ *supplicantProcess, cancel context.CancelFunc, _ chan unix.WaitStatus) { cancel() },
			want:    supplicantEnded,
			signals: []os.Signal{unix.SIGTERM},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := wirelessDetachedLoop(t)
				proc := wirelessDiesOn(unix.SIGTERM)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				go func() {
					time.Sleep(tc.after)
					tc.end(p, cancel, proc.died)
				}()

				begin := time.Now()
				got := p.watch(ctx, proc.handle(), proc.died, false)

				if got != tc.want {
					t.Errorf("outcome %d, want %d", got, tc.want)
				}
				if elapsed := time.Since(begin); elapsed != tc.after {
					t.Errorf("the wait ended after %s, want %s", elapsed, tc.after)
				}
				if !slices.Equal(proc.sent, tc.signals) {
					t.Errorf("signals %v, want %v", proc.sent, tc.signals)
				}
				// The watch abandons an attach that is still out, and
				// that attach ends only when its patience runs out. The
				// bubble must see it end before the test returns.
				time.Sleep(2 * p.control.patience)
			})
		})
	}
}

// A cancelled plane must stop a supplicant whose event stream is
// attached, the same as a stop from the shutdown, so the supplicant
// deauthenticates instead of meeting kill(-1).
func TestWatchStopsAnAttachedSupplicantWhenThePlaneIsCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := wirelessDetachedLoop(t)
		proc := wirelessDiesOn(unix.SIGTERM)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		got := p.watch(ctx, proc.handle(), proc.died, true)

		if got != supplicantEnded {
			t.Errorf("outcome %d, want supplicantEnded", got)
		}
		if !slices.Equal(proc.sent, []os.Signal{unix.SIGTERM}) {
			t.Errorf("signals %v, want SIGTERM", proc.sent)
		}
	})
}
