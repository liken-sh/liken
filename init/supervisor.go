package main

// Supervising k3s.
//
// This file is the entire service management of this OS: start one
// process, and when it dies, start it again. Everything a traditional
// init manages above that line (orderings, dependencies, sockets,
// timers) belongs to Kubernetes, which is the process being
// supervised.
//
// There's one genuinely subtle problem here, and it's worth
// understanding because it's the classic bug in every homemade PID 1.
// As PID 1 we must reap *every* dead process on the machine (orphans
// reparent to us), so somewhere a loop calls wait(-1), "collect any
// exited child". But the supervisor also needs the exit status of the
// specific child it started, and wait(-1) in one goroutine races
// wait(pid) in another: whichever call collects the status first
// consumes it, and the loser gets an error instead. The fix is a
// single authority: only the reaper ever waits, and everyone else
// subscribes. The reaper posts every exit it collects; exits nobody
// has claimed yet are parked (a child can die before its parent even
// asks), and awaitDeath picks up parked statuses or blocks until one
// arrives.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

// lineWriter forwards each complete line it receives to its
// destination with a prefix. Child processes write in arbitrary
// chunks; buffering to line boundaries keeps their output and
// liken's own messages from interleaving mid-line. The destination
// is the raw console, not init's kmsg-routed stdout: k3s's volume
// would churn the kernel's small ring buffer in seconds, and its
// lines already reach the cluster from the log file the k3s log
// relay tails, so buffering the echo would ship everything twice.
type lineWriter struct {
	dest   io.Writer
	prefix string
	buf    bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// No newline yet: put the partial line back and wait.
			w.buf.WriteString(line)
			break
		}
		fmt.Fprintf(w.dest, "%s%s", w.prefix, line)
	}
	return len(p), nil
}

// reap collects the exit status of any child process for as long as
// the machine plane runs. The plane only stops at shutdown, after
// every process it might collect has already been stopped. SIGCHLD
// arrives whenever a child dies; because signal coalescing can fold
// many deaths into one delivery, each wakeup collects every exited
// child, not just one. This loop is the only place in liken that
// calls wait; every exit status it collects is posted to the death
// registry below, where whoever started the process can claim it.
// (Go note: signal.Notify registers a handler with the runtime and
// forwards deliveries onto a channel, turning an async interrupt into
// an ordinary receive loop, and satisfying the "PID 1 must install
// handlers" rule from main.go's header comment.)
func reap(ctx context.Context) error {
	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, unix.SIGCHLD)
	defer signal.Stop(sigchld)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sigchld:
		}
		for {
			// -1 means "any child"; WNOHANG means "don't block if none
			// have exited", in which case Wait4 returns pid 0.
			var status unix.WaitStatus
			pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			if pid <= 0 || err != nil {
				break
			}
			deaths.record(pid, status)
		}
	}
}

// deathRegistry connects the reaper (the only place wait() happens)
// to everyone awaiting an exit: the reaper records each death, and a
// waiter either picks up a status already parked or leaves a channel
// to be filled. One value with methods, mirroring how machinePlane
// encapsulates the other half of init's shared state.
type deathRegistry struct {
	mu        sync.Mutex
	waiters   map[int]chan unix.WaitStatus
	unclaimed map[int]unix.WaitStatus
}

var deaths = &deathRegistry{
	waiters:   map[int]chan unix.WaitStatus{},
	unclaimed: map[int]unix.WaitStatus{},
}

// record is called by the reaper as it collects each exit.
func (d *deathRegistry) record(pid int, status unix.WaitStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ch, ok := d.waiters[pid]; ok {
		ch <- status
		delete(d.waiters, pid)
	} else {
		d.unclaimed[pid] = status
	}
}

// await blocks until the reaper has collected the given pid.
func (d *deathRegistry) await(pid int) unix.WaitStatus {
	d.mu.Lock()
	if status, ok := d.unclaimed[pid]; ok {
		delete(d.unclaimed, pid)
		d.mu.Unlock()
		return status
	}
	ch := make(chan unix.WaitStatus, 1)
	d.waiters[pid] = ch
	d.mu.Unlock()
	return <-ch
}

const (
	k3sBinary = "/bin/k3s"

	// The k3s log lives on clusterState, in a directory that is
	// plainly liken's so it can never be mistaken for a file k3s
	// manages. Landing on the persistent disk (when the machine has
	// one) is what lets a log survive the boot that wrote it, and
	// what makes the log relay's mount a stable path. containerd's
	// log is k3s's own choice of path on the same filesystem; init
	// only ever touches it at rotation time (logrotate.go).
	likenLogDir   = "/var/lib/rancher/k3s/liken"
	k3sLog        = likenLogDir + "/k3s.log"
	containerdLog = "/var/lib/rancher/k3s/agent/containerd/containerd.log"
)

// postMortem is the end of a one-shot boot: with no shell to
// investigate from, init answers the questions an investigator would
// ask. What environment did children inherit, and do the tools they
// need actually resolve and run?
func postMortem() {
	fmt.Printf("liken: post-mortem: init PATH=%s\n", os.Getenv("PATH"))
	resolved, err := filepath.EvalSymlinks("/sbin/iptables")
	if err != nil {
		fmt.Printf("liken: post-mortem: /sbin/iptables: %v\n", err)
	} else {
		fmt.Printf("liken: post-mortem: /sbin/iptables -> %s\n", resolved)
	}
	if out, ok := run("iptables", "-V"); ok {
		fmt.Printf("liken: post-mortem: iptables -V: %s\n", out)
	} else {
		fmt.Printf("liken: post-mortem: iptables -V failed: %q\n", out)
	}
}

// superviseK3s runs k3s forever, with two interruptions it honors:
// a reboot request from the operator, and a restart request, the
// lighter disruption that applies staged restart-class changes
// (cluster/changes.go) by bouncing the k3s child in place. It never
// returns otherwise: whenever k3s exits on its own, it gets
// restarted, with backoff, so a fast crash loop doesn't flood the
// console.
//
// The intent channels are selected in *both* of the supervisor's
// states: while k3s runs, and during the backoff sleep between
// restarts. That second select is what makes a request unable to
// race the restart decision: they're alternatives of one select in
// one goroutine, so an intent that arrives while k3s is crash-looping
// neither waits out the sleep nor collides with a restart.
//
// A deliberate bounce is not a crash. applyRestart runs while k3s
// still serves (all the re-rendering happens before any downtime)
// and answers whether anything was actually applied; only then is
// k3s stopped, gracefully, and started again immediately — skipping
// the oneshot check and the backoff entirely, whose subjects are
// k3s failures, not liken decisions. Stopping k3s does not stop its
// containers (the containerd shims hold them), which is the entire
// premise of the restart tier: the machine and its pods stay up
// while k3s reloads the configuration it only reads at start.
//
// The liken.oneshot boot parameter disables the crash restart: k3s
// runs once and its exit powers the machine down. That makes a k3s
// failure observable from outside (QEMU exits, the console is a
// complete record), which is what debugging and automated runs need
// from a machine with no shell. A reboot intent is honored even in
// oneshot: under QEMU's -no-reboot the restart is a clean exit,
// which is exactly what a bounded harness run wants.
func superviseK3s(role api.Role, reboot <-chan machine.RebootIntent,
	restarts <-chan machine.RestartIntent, applyRestart func(machine.RestartIntent) bool) {
	backoff := time.Second
	for {
		started := time.Now()
		bounced := false
		cmd, logf, err := startK3s(role)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: k3s: %v\n", err)
		} else {
			// The reaper is the only waiter (see the file comment);
			// this goroutine just carries its answer into the select.
			died := make(chan unix.WaitStatus, 1)
			go func() { died <- deaths.await(cmd.Process.Pid) }()

		running:
			for {
				select {
				case status := <-died:
					_ = cmd.Process.Release()
					logf.Close()
					fmt.Printf("liken: k3s exited (%s)\n", describeExit(status))
					break running
				case intent := <-reboot:
					stopK3s(cmd.Process.Pid, died)
					_ = cmd.Process.Release()
					// This close is part of the shutdown ordering, not
					// tidiness: the log lives on clusterState, which
					// rebootMachine is about to unmount, and an open
					// handle there would make that unmount fail busy.
					logf.Close()
					rebootMachine(intent) // never returns
				case intent := <-restarts:
					// A stale or duplicate intent applies nothing, and
					// a running k3s is not disturbed over it.
					if !applyRestart(intent) {
						continue running
					}
					fmt.Println("liken: restarting k3s to apply the staged changes")
					stopK3s(cmd.Process.Pid, died)
					_ = cmd.Process.Release()
					logf.Close()
					bounced = true
					break running
				}
			}
		}
		if bounced {
			continue // straight back to startK3s: a bounce is not a crash
		}
		if bootParam("liken.oneshot") {
			postMortem()
			fmt.Println("liken: one-shot boot, not restarting k3s; powering off")
			powerOff()
			return
		}

		// A k3s that ran for a while resets the backoff; one that died
		// immediately doubles it, capped so a truly broken
		// configuration still retries every half minute.
		if time.Since(started) > time.Minute {
			backoff = time.Second
		} else if backoff < 30*time.Second {
			backoff *= 2
		}
		delay := withJitter(backoff)
		fmt.Printf("liken: restarting k3s in %s\n", delay.Round(time.Millisecond))
		select {
		case <-time.After(delay):
		case intent := <-reboot:
			rebootMachine(intent) // k3s is already dead; nothing to stop
		case intent := <-restarts:
			// k3s is already down: apply the staged changes now and
			// skip the rest of the delay, since the next start will
			// read them either way.
			_ = applyRestart(intent)
		}
	}
}

// k3sRuntimeEnv is the Go runtime discipline init imposes on k3s,
// derived from the machine's memory and from what the cluster asks
// k3s to carry. Go's collector left alone lets a process's heap grow
// to twice its live data before collecting, which is the right trade
// on a machine with memory to spare and the wrong one on the small
// machines liken targets, where k3s is the dominant resident and
// every uncollected megabyte comes out of the workloads' budget.
//
// GOMEMLIMIT is a soft ceiling on everything the runtime manages
// (heap, stacks, its own metadata): approaching it, the collector
// runs harder rather than growing; past it, the runtime caps GC at
// half the process's CPU and lets the heap grow anyway, so a genuine
// spike degrades into slowness, never a heap-exhaustion crash. The
// ceiling scales with the features the cluster declares, because
// they are what the heap holds: a minimum viable control plane fits
// comfortably under a quarter of the machine, while the helm feature
// (and everything that requires it, like traefik) brings the chart
// renderer and Traefik's CRDs into the process and gets seven
// sixteenths — the budget that leaves a 1GB machine room for the
// container runtime, the pods themselves, the kernel's own caches,
// and free headroom for the next convergence. GOGC=50 sets the
// everyday pace under either ceiling: collect at fifty percent heap
// growth instead of a hundred, trading a little CPU all the time so
// the process's resting size stays near its live data.
//
// containerd and the shims inherit this environment from k3s. That
// is deliberate and cheap: they are Go programs a fraction of the
// limit's size, so the ceiling never constrains them and the GC pace
// keeps them lean too. Workload processes inherit nothing; their
// environments come from their pod specs.
func k3sRuntimeEnv(memoryBytes uint64, helm bool) []string {
	limit := memoryBytes / 4
	if helm {
		limit = memoryBytes / 16 * 7
	}
	return []string{
		fmt.Sprintf("GOMEMLIMIT=%dMiB", limit/(1<<20)),
		"GOGC=50",
	}
}

// k3sMemoryDiscipline is the runtime environment startK3s gives every
// k3s it launches. writeK3sBootConfig derives it beside the boot
// drop-in — at boot and again on every applied restart — so a
// restart that changes the cluster's features re-scales the ceiling
// on the same bounce that reconfigures k3s.
var k3sMemoryDiscipline []string

// startK3s launches k3s in the machine's role and hands back the
// running command and its log file (which must stay open as long as
// the process writes; the console copy flows through an in-process
// pipe).
func startK3s(role api.Role) (*exec.Cmd, io.Closer, error) {
	// k3s's output goes two places: a file, and the console, where it
	// arrives live, line-buffered, and prefixed so it's
	// distinguishable from liken's own messages. On a machine with no
	// shell, the console is the only way to read a log; the file is
	// what the k3s log relay tails into the cluster. The file writer
	// caps a boot's log so a chatty k3s can't fill the filesystem it
	// shares with etcd (logrotate.go).
	logf, err := openCappedLog(k3sLog, k3sLogCap)
	if err != nil {
		return nil, nil, err
	}

	// This is the one place liken's role vocabulary meets k3s's: a
	// leader runs `k3s server`, a follower runs `k3s agent`
	// (cluster/cluster.go). Configuration lives in files, not flags:
	// a leader's k3s reads /etc/rancher/k3s/config.yaml on its own,
	// and a follower's is pointed at its own file (whose leader-only
	// sibling would otherwise be misread as unknown flags). Both were
	// joined with this boot's derived drop-in by k3s.go before we got
	// here.
	args := []string{"server"}
	if role == api.RoleFollower {
		args = []string{"agent", "--config", k3sAgentConfig}
	}
	cmd := exec.Command(k3sBinary, args...)
	if len(k3sMemoryDiscipline) > 0 {
		cmd.Env = append(os.Environ(), k3sMemoryDiscipline...)
	}
	cmd.Stdout = io.MultiWriter(logf, &lineWriter{dest: console, prefix: "k3s | "})
	cmd.Stderr = io.MultiWriter(logf, &lineWriter{dest: console, prefix: "k3s | "})
	if err := cmd.Start(); err != nil {
		logf.Close()
		return nil, nil, fmt.Errorf("starting k3s: %w", err)
	}
	fmt.Printf("liken: k3s %s started (pid %d), logs in %s\n", role, cmd.Process.Pid, k3sLog)
	return cmd, logf, nil
}

// stopK3s asks k3s to exit and waits for the reaper's confirmation,
// escalating to SIGKILL if it takes too long. It only signals and receives;
// the reaper stays the sole authority on wait (the file comment's one
// rule).
func stopK3s(pid int, died <-chan unix.WaitStatus) {
	fmt.Printf("liken: stopping k3s (pid %d)\n", pid)
	_ = unix.Kill(pid, unix.SIGTERM)
	select {
	case status := <-died:
		fmt.Printf("liken: k3s exited (%s)\n", describeExit(status))
	case <-time.After(30 * time.Second):
		fmt.Fprintln(os.Stderr, "liken: k3s ignored SIGTERM for 30s; killing it")
		_ = unix.Kill(pid, unix.SIGKILL)
		fmt.Printf("liken: k3s exited (%s)\n", describeExit(<-died))
	}
}

func describeExit(status unix.WaitStatus) string {
	switch {
	case status.Exited():
		return fmt.Sprintf("status %d", status.ExitStatus())
	case status.Signaled():
		return fmt.Sprintf("signal %s", status.Signal())
	default:
		return fmt.Sprintf("wait status %#x", uint32(status))
	}
}

// reportWhenReady watches for the moment the machine becomes a
// working Kubernetes node: it polls `k3s kubectl get nodes` (which
// reads the admin kubeconfig k3s writes once its API is serving) and
// prints the node's status as it changes: registering, NotReady, and
// finally Ready. It's a machine-plane component whose work completes
// once the report is finished, so every exit path returns nil.
//
// This reporter (and reportPods below) is the one resident of the
// machine plane that k3s does not depend on, which the two-planes
// rule (components.go) would normally send into the cluster. It
// stays by deliberate exception: its whole subject is the window
// before the cluster can speak for itself, when k3s is starting and
// the operator pod doesn't exist yet, and on a shell-less machine
// the console is the only place that story can be told. The operator
// takes over the reporting the moment it runs; this is the bridge to
// that moment.
func reportWhenReady(ctx context.Context) error {
	fetch := func() (string, bool) {
		return run(k3sBinary, "kubectl", "get", "nodes", "--no-headers")
	}
	if pollAndReport(ctx, 3*time.Second, 5*time.Minute, "node", fetch, containsReady) {
		fmt.Println("liken: kubernetes is up")
		reportPods(ctx)
	} else if ctx.Err() == nil {
		fmt.Println("liken: gave up waiting for the node to be Ready (k3s may still get there)")
	}
	return nil
}

// reportPods prints the system pods starting up after the node goes
// Ready, then goes quiet once everything is Running: the console
// equivalent of watching `kubectl get pods -A` settle.
func reportPods(ctx context.Context) {
	fetch := func() (string, bool) {
		return run(k3sBinary, "kubectl", "get", "pods", "-A", "--no-headers")
	}
	if pollAndReport(ctx, 5*time.Second, 5*time.Minute, "pod", fetch, podsSettled) {
		fmt.Println("liken: all system pods are settled")
	} else if ctx.Err() == nil {
		fmt.Println("liken: system pods have not settled; see the pod status lines above")
	}
}

// pollAndReport is the shape both reporters share: fetch a kubectl
// table on an interval, print it under the prefix whenever it
// changes, and answer true the moment it satisfies settled — or false
// when patience runs out or the plane shuts down. Printing only
// changes is what keeps the console readable: a table that sits
// unchanged for a minute produces no lines at all.
func pollAndReport(ctx context.Context, interval, patience time.Duration, prefix string,
	fetch func() (string, bool), settled func(string) bool) bool {
	last := ""
	deadline := time.Now().Add(patience)
	for time.Now().Before(deadline) {
		if !sleepUnlessCancelled(ctx, interval) {
			return false
		}
		out, ok := fetch()
		if !ok || out == "" {
			continue
		}
		if out != last {
			last = out
			for line := range strings.SplitSeq(out, "\n") {
				fmt.Printf("liken: %s: %s\n", prefix, line)
			}
		}
		if settled(out) {
			return true
		}
	}
	return false
}

// containsReady looks for the word Ready as a whole status field, so
// NotReady doesn't match.
func containsReady(out string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "Ready" {
			return true
		}
	}
	return false
}

// podsSettled reports whether every pod in the table has reached
// Running or Completed, the status field being kubectl's fourth
// column.
func podsSettled(out string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[3] != "Running" && fields[3] != "Completed" {
			return false
		}
	}
	return true
}

// runNarrated executes a command with its output echoed live to the
// console, prefixed like k3s's, and reports whether it exited
// cleanly. It's for commands whose output matters to someone watching
// a boot (mke2fs reporting on the filesystem it's making), where run's
// captured-output shape would hide it.
func runNarrated(prefix, path string, args ...string) bool {
	cmd := exec.Command(path, args...)
	w := &lineWriter{dest: console, prefix: prefix}
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "liken: starting %s: %v\n", path, err)
		return false
	}
	status := deaths.await(cmd.Process.Pid)
	_ = cmd.Process.Release()
	return status.Exited() && status.ExitStatus() == 0
}

// run executes a command and returns its output, waiting for it via
// the reaper (see the file comment: nobody but the reaper calls
// wait). Reading the pipe to EOF tells us the process is done writing;
// the reaper tells us how it died.
func run(path string, args ...string) (string, bool) {
	cmd := exec.Command(path, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", false
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return "", false
	}
	// Reading the pipe to EOF is how we know the process is done
	// writing; the reaper tells us how it died.
	buf, _ := io.ReadAll(out)
	status := deaths.await(cmd.Process.Pid)
	_ = cmd.Process.Release()
	return strings.TrimRight(string(buf), "\r\n"), status.Exited() && status.ExitStatus() == 0
}
