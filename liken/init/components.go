package main

// The machine plane.
//
// liken has exactly two planes. Machine-plane concerns are the loops
// a machine needs to function as a machine: reaping, watching for
// reboot intents, keeping the clock synchronized. These loops run as
// goroutines inside init, registered here. Workload-plane software
// runs as processes under k3s, and k3s itself is the only child
// process init ever supervises. A traditional distribution keeps a
// middle tier of system daemons, for example a time daemon, a device
// daemon, and a log daemon, under a service manager. liken has none
// of these daemons on purpose, which is why this file replaces most
// of what a service manager does.
//
// A concern belongs on the machine plane only when k3s depends on it
// to exist. Anything the cluster could host for itself belongs in
// the cluster, where Kubernetes is the supervisor. Time qualifies for
// the machine plane because a machine with a skewed clock fails TLS
// and cannot join the cluster, so the cluster can never correct the
// clock for it. A concern that k3s does not depend on runs in the
// cluster instead of in init.
//
// Running everything in one process is a real trade-off. It buys
// simplicity: dependency ordering follows program order, restart
// policy is a for loop, state uses shared structs instead of IPC, and
// the whole machine plane ships and upgrades as one binary. It costs
// isolation: there is no privilege separation, because every
// goroutine runs as PID 1, with root privileges, and fault isolation
// is imperfect. recover catches a panic, but a fatal runtime error,
// for example concurrent map misuse or an out-of-memory condition,
// ends all of PID 1, and the kernel answers that with a panic of its
// own. liken accepts this cost because it is crash-only by design:
// the root filesystem is RAM, the code proves manifests before it
// trusts them, and a reboot lands the machine in a known-good state.
//
// The rule has one exception. A component becomes a child process
// only when it parses untrusted network input, needs fewer
// privileges than PID 1, or must not take the machine down when it
// fails fatally. The child process is this same binary, re-executed
// with an argv verb (the multi-call pattern), so the OS stays one
// artifact. The NTP responder, which reads unauthenticated UDP, runs
// as a goroutine today. It is the first candidate for promotion to
// its own supervised process.
//
// The contract with a component is small: a component is a function
// of a context, and it runs until its work is done or the context is
// cancelled. An error return means the component failed and the code
// should restart it. Returning nil means the work is complete, and a
// component that reports on a boot milestone simply returns when it
// reaches the milestone. The code logs a panic with its stack and
// restarts the component with backoff, instead of letting the panic
// unwind PID 1 into a kernel panic. Shutdown runs the dependency
// stack in reverse: k3s and its containers stop first, then the code
// cancels this plane, then filesystems unmount. The wait is bounded,
// so one stuck loop cannot stall a reboot.

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
)

// plane is the boot's one machine plane. It is package-level, the
// same as the reaper's registry, because init is a single program
// and these are its loops. Tests construct their own planes; this
// variable holds the real boot's plane.
var plane = newMachinePlane()

type machinePlane struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Restart pacing, matching the k3s supervisor's pacing: a
	// component that keeps failing waits twice as long each time.
	// The wait is capped, so a truly broken loop still retries every
	// half minute, and a component that ran for a while before
	// failing starts over at the initial delay.
	backoff    time.Duration
	maxBackoff time.Duration

	// The names still running, so that a shutdown that times out can
	// name which component ignored it. On a machine with no shell,
	// the console message is the only place this fact can appear.
	mu      sync.Mutex
	running map[string]bool
}

func newMachinePlane() *machinePlane {
	ctx, cancel := context.WithCancel(context.Background())
	return &machinePlane{
		ctx:        ctx,
		cancel:     cancel,
		backoff:    time.Second,
		maxBackoff: 30 * time.Second,
		running:    map[string]bool{},
	}
}

// start registers a component and runs it until it finishes or the
// plane shuts down, and restarts it on failure. start returns
// immediately, and there is no way to await a component; this is by
// design. Nothing in a boot sequences on another component's
// progress, and main calls anything that must happen before k3s
// starts synchronously instead.
func (p *machinePlane) start(name string, run func(context.Context) error) {
	p.mu.Lock()
	p.running[name] = true
	p.mu.Unlock()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() {
			p.mu.Lock()
			delete(p.running, name)
			p.mu.Unlock()
		}()

		backoff := p.backoff
		for {
			started := time.Now()
			err := runComponent(p.ctx, run)
			if p.ctx.Err() != nil || err == nil {
				return
			}
			fmt.Fprintf(os.Stderr, "liken: %s: %v\n", name, err)

			if time.Since(started) > time.Minute {
				backoff = p.backoff
			} else if backoff < p.maxBackoff {
				backoff *= 2
			}
			delay := withJitter(backoff)
			fmt.Printf("liken: restarting %s in %s\n", name, delay.Round(time.Millisecond))
			select {
			case <-time.After(delay):
			case <-p.ctx.Done():
				return
			}
		}
	}()
}

// runComponent is the recovery boundary: a panicking component
// surfaces as an ordinary error, with its stack attached. Without
// this boundary, any panic anywhere in the machine plane would
// unwind PID 1 and panic the kernel. runComponent cannot catch
// everything; a fatal runtime error still ends the process. This is
// the imperfect fault isolation the header comment describes.
func runComponent(ctx context.Context, run func(context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panicked: %v\n%s", r, debug.Stack())
		}
	}()
	return run(ctx)
}

// withJitter randomizes a delay upward by as much as half again. The
// point is fleet behavior, not this one machine's behavior. A power
// event reboots every machine together, and their components fail
// together too, for example because the network is not up yet or a
// leader is not back yet. Identical backoff would have every machine
// retry at the same instants. A random share spreads the retries
// out, so recovery load arrives gradually rather than all at once.
func withJitter(d time.Duration) time.Duration {
	return d + rand.N(d/2)
}

// sleepUnlessCancelled is the pause a polling component takes between
// looks. It reports false when the plane is shutting down, which
// tells the component to return instead of polling again. Plain
// time.Sleep is off limits inside a component for exactly this
// reason: a loop blocked in time.Sleep cannot observe its
// cancellation.
func sleepUnlessCancelled(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// shutdown cancels every component and waits for them, but only for
// a bounded time. The plane shuts down because the machine is going
// down, and a component that ignores its context must not be able
// to block a reboot. shutdown names any component still running at
// the timeout on the console and leaves it behind. This is safe,
// because the machine kills every process and reboots moments
// later.
func (p *machinePlane) shutdown(timeout time.Duration) {
	p.cancel()
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		p.mu.Lock()
		names := make([]string, 0, len(p.running))
		for name := range p.running {
			names = append(names, name)
		}
		p.mu.Unlock()
		slices.Sort(names)
		fmt.Fprintf(os.Stderr, "liken: shutdown proceeding without: %s\n", strings.Join(names, ", "))
	}
}
