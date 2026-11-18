package main

// The shutdown stops each wpa_supplicant before reboot.go sends SIGTERM
// and SIGKILL to every process with kill(-1), so the supplicant can
// deauthenticate. Each stop has a time limit, so a supplicant or a
// supervision loop that does not respond cannot hold up the shutdown.

import (
	"context"
	"os"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"
)

// wirelessSignalled is a fake supplicant process. It records each
// signal it receives, and exits on the fatal one by sending a status on
// died, as the reaper does.
type wirelessSignalled struct {
	fatal os.Signal
	sent  []os.Signal
	died  chan unix.WaitStatus
}

func wirelessDiesOn(fatal os.Signal) *wirelessSignalled {
	return &wirelessSignalled{fatal: fatal, died: make(chan unix.WaitStatus, 1)}
}

func (w *wirelessSignalled) handle() runningSupplicant {
	return runningSupplicant{pid: 1_000_100, signal: func(sig os.Signal) error {
		w.sent = append(w.sent, sig)
		if sig == w.fatal {
			w.died <- 0
		}
		return nil
	}}
}

// A supplicant that ignores SIGTERM must receive SIGKILL after five
// seconds, or the shutdown would wait for it forever.
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

// clearSupplicantsAfterTest clears the supplicant list and
// supplicantsStopping when the test ends, so the next test can register
// supplicants.
func clearSupplicantsAfterTest(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		supplicantsMu.Lock()
		supplicantsStopping = false
		supplicants = nil
		supplicantsMu.Unlock()
	})
}

// wirelessLoop registers a supervision loop. A cooperative loop ends
// when it is told to stop, and a stuck one never ends.
func wirelessLoop(t *testing.T, ifname string, cooperative bool) *supplicantProcess {
	t.Helper()
	p := &supplicantProcess{ifname: ifname, stop: make(chan struct{}), done: make(chan struct{})}
	if !registerSupplicant(p) {
		t.Fatalf("registerSupplicant refused %s before any shutdown, want it registered", ifname)
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
// registration. Otherwise a radio that appears during a reboot would
// start a supplicant that nothing stops.
func TestStopSupplicantsEndsEveryLoopWithinItsBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clearSupplicantsAfterTest(t)
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
				t.Errorf("the loop on %s was not told to stop, want its stop channel closed", p.ifname)
			}
		}
		late := &supplicantProcess{ifname: "wlan2", stop: make(chan struct{}), done: make(chan struct{})}
		if registerSupplicant(late) {
			t.Error("registering a supplicant after the shutdown returned true, want false")
		}
		if got := len(trackedSupplicants()); got != 0 {
			t.Errorf("the list has %d loops after the shutdown, want 0", got)
		}
	})
}

// wirelessDetachedLoop returns a loop whose supplicant never creates
// its control socket, so each attach waits for the timeout and then fails.
func wirelessDetachedLoop(t *testing.T) *supplicantProcess {
	t.Helper()
	return &supplicantProcess{
		ifname: "wlan0",
		stop:   make(chan struct{}), done: make(chan struct{}),
		backoff: 10 * time.Second, maxBackoff: 30 * time.Second,
		control: &wpaControl{
			socket:  t.TempDir() + "/ctrl/wlan0",
			local:   t.TempDir() + "/client",
			out:     make(chan wpaEvent, 8),
			timeout: time.Second,
		},
	}
}

// While the loop retries attaching the event stream, the process
// dying, the loop being told to stop, and the context being cancelled
// must each end the wait immediately. Only the stop and the
// cancellation send a signal, because a process that already died has
// nothing to signal.
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
			name:    "the context is cancelled",
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
				// The watch returns without waiting for an attach that is
				// still running, and that attach ends only when the timeout
				// runs out. synctest requires every goroutine of the
				// bubble to end before the test returns.
				time.Sleep(2 * p.control.timeout)
			})
		})
	}
}

// Cancelling the context must send SIGTERM to a supplicant whose event
// stream is attached, as the shutdown does, so the supplicant
// deauthenticates before kill(-1).
func TestWatchStopsAnAttachedSupplicantWhenTheContextIsCancelled(t *testing.T) {
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
