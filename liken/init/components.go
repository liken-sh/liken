package main

// The machine plane is the set of loops that a machine needs before
// it can be a cluster node, such as reaping orphaned processes,
// watching for reboot intents, and keeping the clock correct. They
// run as goroutines inside init, which main registers with
// plane.start. Workloads run under k3s. Other distributions run time,
// device, and log daemons under a service manager. liken has none of
// them, so this file does the part of a service manager's work that
// the machine plane needs.
//
// A loop belongs on the machine plane only when k3s depends on it to
// start. Kubernetes supervises everything else. The clock is on the
// machine plane because a machine with a wrong clock fails TLS and
// cannot join the cluster.
//
// Each loop is a component: a function that takes a context and runs
// until its work is done or the context is cancelled. It returns an
// error when it fails, and init restarts it. It returns nil when its
// work is complete, and init does not restart it. At shutdown, k3s
// and its containers stop first, then init cancels the machine plane,
// then the filesystems unmount.
//
// In one process, startup order is program order, a restart policy
// is a for loop, loops share structs with no IPC, and the machine
// plane ships as one binary. The cost is that every goroutine runs as
// PID 1 with root privileges, and that a fatal runtime error, such as
// concurrent map access or running out of memory, ends PID 1 and the
// kernel panics. recover catches only a panic. This is acceptable
// because the root filesystem is in RAM, init verifies manifests
// before it trusts them, and a reboot returns the machine to a known
// state.
//
// Move a component into a child process when it parses untrusted
// network input, needs fewer privileges than PID 1, or must not take
// the machine down when it fails. The child would be this same
// binary, started with a verb in argv, so that the OS stays one
// artifact. The NTP responder reads unauthenticated UDP and still
// runs as a goroutine.

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

// plane is the machine plane of this boot. It is a package variable
// because init is one program and main registers every loop on it.
// Tests create their own planes with newMachinePlane.
var plane = newMachinePlane()

type machinePlane struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// running is the names of the components that have not returned,
	// which shutdown prints when it times out.
	mu      sync.Mutex
	running map[string]bool
}

// The delay before a restart doubles after each failure, and stops
// doubling once it reaches componentMaxBackoff. A component that ran
// for more than a minute before it failed restarts after
// componentBackoff.
const (
	componentBackoff    = time.Second
	componentMaxBackoff = 30 * time.Second
)

func newMachinePlane() *machinePlane {
	ctx, cancel := context.WithCancel(context.Background())
	return &machinePlane{
		ctx:     ctx,
		cancel:  cancel,
		running: map[string]bool{},
	}
}

// start runs a component in a goroutine until it returns nil or the
// plane shuts down, and restarts it after a failure. It returns at
// once. main runs a step that must finish before k3s starts as an
// ordinary call, not as a component.
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

		backoff := componentBackoff
		for {
			started := time.Now()
			err := runComponent(p.ctx, run)
			if p.ctx.Err() != nil || err == nil {
				return
			}
			fmt.Fprintf(os.Stderr, "liken: %s: %v\n", name, err)

			if time.Since(started) > time.Minute {
				backoff = componentBackoff
			} else if backoff < componentMaxBackoff {
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

// runComponent runs a component and turns a panic into an error that
// includes the stack. Without it, a panic in any component would end
// PID 1 and panic the kernel.
func runComponent(ctx context.Context, run func(context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the component panicked: %v\n%s", r, debug.Stack())
		}
	}()
	return run(ctx)
}

// withJitter adds a random delay of up to half of d. After a power
// outage, every machine reboots at once and its components fail for
// the same reason, such as a leader that is not back yet. Without
// jitter, every machine would retry at the same moments.
func withJitter(d time.Duration) time.Duration {
	return d + rand.N(d/2)
}

// sleepUnlessCancelled waits for d and returns true, or returns false
// as soon as ctx is cancelled. A component uses it in place of
// time.Sleep so that shutdown does not wait for the sleep.
func sleepUnlessCancelled(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// shutdown cancels every component and waits up to timeout for them
// to return, so that a component that ignores its context cannot
// block a reboot. After the timeout it prints the names of the
// components still running and returns without them, which is safe
// because the machine reboots next.
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
		fmt.Fprintf(os.Stderr, "liken: shutdown timed out with these components still running: %s\n", strings.Join(names, ", "))
	}
}
