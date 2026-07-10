package main

// The machine plane.
//
// liken has exactly two planes. Machine-plane concerns are the loops
// a machine needs in order to function as a machine: reaping,
// watching for reboot intents, keeping the clock synchronized. They
// run as goroutines inside init, registered here. Workload-plane
// software runs as processes under k3s, and k3s itself is the only
// child process init ever supervises. A traditional distro keeps a
// middle tier of system daemons (a time daemon, a device daemon, a
// log daemon) under a service manager; liken deliberately has none,
// which is why this file replaces most of what a service manager
// does.
//
// A concern belongs on the machine plane only when k3s depends on it
// to exist. Anything the cluster could host for itself belongs in
// the cluster, where Kubernetes is the supervisor. Time qualifies
// because a machine with a skewed clock fails TLS and can't join the
// cluster, so the cluster can never correct the clock for it. A
// concern that k3s doesn't depend on runs in the cluster instead of
// in init.
//
// Running everything in one process is a real trade-off. It buys
// simplicity: dependency ordering is program order, restart policy
// is a for loop, state is shared structs instead of IPC, and the
// whole machine plane ships and upgrades as one binary. It costs
// isolation: there is no privilege separation (every goroutine is
// PID 1, all root), and fault isolation is imperfect. recover
// catches a panic, but a fatal runtime error (concurrent map misuse,
// out of memory) ends all of PID 1, which the kernel answers with a
// panic of its own. liken accepts that because it is crash-only by
// design: the root is RAM, manifests are proven before they're
// trusted, and a reboot lands the machine in a known-good state.
//
// The rule includes an escape hatch. A component is promoted to a
// child process only when it parses untrusted network input, needs
// fewer privileges than PID 1, or must not take the machine down
// when it fatals. The child is this same binary re-exec'd with an
// argv verb (the multi-call pattern), so the OS stays one artifact.
// The NTP responder, which reads unauthenticated UDP, runs as a
// goroutine and is the first candidate for promotion to its own
// supervised process.
//
// The contract with a component is small: it's a function of a
// context, and it runs until its work is done or the context is
// cancelled. An error return means the component failed and should
// be restarted; returning nil means the work is complete, and a
// component that reports on a boot milestone simply returns when
// the milestone is reached. A panic is logged with its stack and
// restarted with backoff, instead of unwinding PID 1 into a kernel
// panic. Shutdown runs the dependency stack in reverse: k3s and its
// containers stop first, then this plane is cancelled, then
// filesystems unmount. The wait is bounded, so one stuck loop can't
// stall a reboot.

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

// plane is the boot's one machine plane, package-level the way the
// reaper's registry is: init is a single program and these are its
// loops. Tests construct their own planes; this is the real boot's.
var plane = newMachinePlane()

type machinePlane struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Restart pacing, mirroring the k3s supervisor's: a component
	// that keeps failing waits twice as long each time, capped so a
	// truly broken loop still retries every half minute, and a
	// component that ran a while before failing starts over at the
	// initial delay.
	backoff    time.Duration
	maxBackoff time.Duration

	// The names still running, so a shutdown that times out can say
	// *which* component ignored it. On a machine with no shell, the
	// console message is the only place that fact can surface.
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
// plane shuts down, restarting it on failure. start returns
// immediately, and there is deliberately no way to await a component:
// nothing in a boot sequences on another component's progress, and
// anything that must happen before k3s starts is called
// synchronously from main instead.
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
// surfaces as an ordinary error, stack attached. Without it, any
// panic anywhere in the machine plane unwinds PID 1 and the kernel
// panics. It cannot catch everything — a fatal runtime error still
// ends the process, the imperfect fault isolation the header
// comment describes.
func runComponent(ctx context.Context, run func(context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panicked: %v\n%s", r, debug.Stack())
		}
	}()
	return run(ctx)
}

// withJitter randomizes a delay upward by as much as half again.
// The point is fleet behavior, not this machine: a power event
// reboots every machine together, their components fail together
// (the network isn't up, a leader isn't back), and identical backoff
// would have every machine retrying at the same instants. A random
// share spreads the retries out, so recovery load arrives gradually
// rather than all at once.
func withJitter(d time.Duration) time.Duration {
	return d + rand.N(d/2)
}

// sleepUnlessCancelled is the pause a polling component takes between
// looks: it reports false when the plane is shutting down, the
// signal to return instead of polling again. Plain time.Sleep is off
// limits inside a component for exactly this reason: a loop blocked
// in time.Sleep cannot observe its cancellation.
func sleepUnlessCancelled(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// shutdown cancels every component and waits for them, but only so
// long: the plane is shut down because the machine is going down,
// and a component that ignores its context must not be able to
// wedge a reboot. Any component still running at the timeout is
// named on the console and left behind, which is safe here: the
// machine kills every process and reboots moments later.
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
