package main

// Supervising k3s.
//
// This file is the complete service manager for this OS. It starts
// one process, and starts that process again when it dies. Kubernetes
// manages everything a traditional init manages above that level:
// order, dependencies, sockets, and timers. Kubernetes is the process
// under supervision here.
//
// One problem here is subtle and worth understanding, because it is
// the classic bug in every homemade PID 1. As PID 1, this program
// must reap every dead process on the machine, because orphan
// processes reparent to PID 1. So a loop somewhere calls wait(-1),
// which collects the status of any exited child. But the supervisor
// also requires the exit status of the specific child it started.
// wait(-1) in one goroutine races wait(pid) in another goroutine. The
// call that collects the status first consumes it. The other call
// gets an error instead. The fix is a single authority. Only the
// reaper calls wait. Every other part of the code subscribes to the
// reaper. The reaper posts every exit status it collects. When
// nobody has claimed an exit yet, the status stays parked, because a
// child can die before its parent asks for it. The registry's await
// method reads a parked status, or it waits until a status arrives.

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
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// lineWriter forwards each complete line it receives to its
// destination, with a prefix added. Child processes write output in
// chunks of arbitrary size. Buffering the output to line boundaries
// stops the child's output and liken's own messages from
// interleaving in the middle of a line. The destination is the raw
// console, not init's kmsg-routed stdout. k3s produces enough output
// to fill the kernel's small ring buffer within seconds. k3s's lines
// already reach the cluster through the log file that the k3s log
// relay tails, so buffering the echo there too would send everything
// twice.
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
			// No newline has arrived yet. Put the partial line back in
			// the buffer and wait.
			w.buf.WriteString(line)
			break
		}
		fmt.Fprintf(w.dest, "%s%s", w.prefix, line)
	}
	return len(p), nil
}

// reap collects the exit status of any child process for as long as
// the machine plane runs. The plane stops only at shutdown, after
// every process it might collect has already stopped. SIGCHLD
// arrives whenever a child dies. Signal coalescing can fold many
// deaths into one delivery, so each wakeup collects every exited
// child, not just one. This loop is the only place in liken that
// calls wait. It posts every exit status it collects to the death
// registry below, where whoever started the process can claim the
// status.
// (Go note: signal.Notify registers a handler with the runtime, and
// forwards deliveries onto a channel. This turns an asynchronous
// interrupt into an ordinary receive loop, and satisfies the "PID 1
// must install handlers" rule from main.go's header comment.)
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
			// -1 means "any child". WNOHANG means "do not block if none
			// have exited"; in that case, Wait4 returns pid 0.
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
// to every part of the code that awaits an exit. The reaper records
// each death, and a waiter either reads a status already parked, or
// leaves a channel open to be filled later. deathRegistry is one
// value with methods, the same pattern machinePlane uses to
// encapsulate the other half of init's shared state.
type deathRegistry struct {
	mu        sync.Mutex
	waiters   map[int]chan unix.WaitStatus
	unclaimed map[int]unix.WaitStatus
}

var deaths = &deathRegistry{
	waiters:   map[int]chan unix.WaitStatus{},
	unclaimed: map[int]unix.WaitStatus{},
}

// record stores the exit status that the reaper collects for each
// process.
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

// await waits until the reaper has collected the given pid.
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

	// The k3s log lives on clusterState, in a directory that clearly
	// belongs to liken, so nothing can mistake it for a file that k3s
	// manages. When the machine has a persistent disk, storing the
	// log there lets the log survive the boot that wrote it, and
	// gives the log relay's mount a stable path. containerd chooses
	// its own log path on the same filesystem. init touches that log
	// only at rotation time (logrotate.go).
	likenLogDir   = "/var/lib/rancher/k3s/liken"
	k3sLog        = likenLogDir + "/k3s.log"
	containerdLog = "/var/lib/rancher/k3s/agent/containerd/containerd.log"
)

// postMortem runs at the end of a one-shot boot. There is no shell to
// investigate from, so postMortem prints the facts an investigator
// would need: the environment that child processes inherited, and
// whether the tools they need resolve and run correctly.
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

// superviseK3s runs k3s forever, and honors two kinds of
// interruption: a reboot request from the operator, and a restart
// request. A restart request is the lighter disruption: it applies
// staged restart-class changes (cluster/changes.go) by bouncing the
// k3s child process in place. superviseK3s never returns for any
// other reason. Whenever k3s exits on its own, superviseK3s restarts
// it, with backoff, so a fast crash loop does not flood the console.
//
// The code selects on the intent channels in *both* states of the
// supervisor: while k3s runs, and during the backoff sleep between
// restarts. That second select stops a request from racing the
// restart decision. Both selects are alternatives within one select
// statement in one goroutine, so an intent that arrives while k3s is
// crash-looping does not wait out the sleep, and does not collide
// with a restart.
//
// A deliberate bounce is not a crash. applyRestart runs while k3s
// still serves traffic, so all the re-rendering happens before any
// downtime, and applyRestart reports whether it actually applied
// anything. Only then does superviseK3s stop k3s gracefully and
// start it again immediately. This skips the oneshot check and the
// backoff entirely, because both apply to k3s failures, not to liken
// decisions. Stopping k3s does not stop its containers, because the
// containerd shims hold them. This is the entire basis for the
// restart tier: the machine and its pods stay up while k3s reloads
// the configuration that k3s only reads at start.
//
// The liken.oneshot boot parameter disables the crash restart. k3s
// runs once, and its exit powers the machine down. This makes a k3s
// failure visible from outside the machine: QEMU exits, and the
// console holds a complete record. Debugging and automated test runs
// need this visibility on a machine with no shell. superviseK3s still
// honors a reboot intent in oneshot mode. Under QEMU's -no-reboot
// flag, the restart is a clean exit, exactly what a bounded harness
// run needs.
// afterStop runs each time a restart has stopped k3s, before the
// next start. It exists for the retractions that k3s must never
// see happen: a janitor-teardown feature's seeded manifests are
// removed here, in the window where k3s is down, so the addon
// machinery never deletes the feature's objects itself
// (retractFeatureManifests explains why that deletion would be
// dangerous for flux).
func superviseK3s(role api.Role, reboot <-chan machine.RebootIntent,
	restarts <-chan machine.RestartIntent, applyRestart func(machine.RestartIntent) bool,
	afterStop func()) {
	backoff := time.Second
	for {
		started := time.Now()
		bounced := false
		cmd, logf, err := startK3s(role)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: k3s: %v\n", err)
		} else {
			// The reaper is the only waiter (see the file comment).
			// This goroutine only carries the reaper's answer into
			// the select statement.
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
					// This close is part of the shutdown order, not
					// cleanup. The log lives on clusterState, which
					// rebootMachine is about to unmount. An open file
					// handle there would make that unmount fail as busy.
					logf.Close()
					rebootMachine(intent) // never returns
				case intent := <-restarts:
					// A stale or duplicate intent applies nothing. A
					// running k3s is not disturbed because of it.
					if !applyRestart(intent) {
						continue running
					}
					fmt.Println("liken: restarting k3s to apply the staged changes")
					stopK3s(cmd.Process.Pid, died)
					afterStop()
					_ = cmd.Process.Release()
					logf.Close()
					bounced = true
					break running
				}
			}
		}
		if bounced {
			continue // goes straight back to startK3s: a bounce is not a crash
		}
		if bootParam("liken.oneshot") {
			postMortem()
			fmt.Println("liken: one-shot boot, not restarting k3s; powering off")
			powerOff()
			return
		}

		// A k3s that ran for a while resets the backoff. One that died
		// immediately doubles the backoff. The backoff is capped, so a
		// truly broken configuration still retries every half minute.
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
			// k3s is already down. Apply the staged changes now, and
			// skip the rest of the delay, because the next start
			// reads the changes either way.
			_ = applyRestart(intent)
			afterStop()
		}
	}
}

// k3sRuntimeEnv is the Go runtime discipline that init imposes on
// k3s. The cluster document's spec.runtime.k3s section sets it, and
// init resolves that section against this machine's memory and the
// helm feature (cluster/runtime.go). Left alone, Go's collector lets
// a process's heap grow to twice its live data before it collects
// garbage. That trade is right on a machine with memory to spare. It
// is the wrong trade on the small machines liken targets, where k3s
// is the dominant resident process, and every uncollected megabyte
// reduces the workloads' memory budget.
//
// GOMEMLIMIT is a soft ceiling on everything the runtime manages:
// heap, stacks, and its own metadata. As memory use approaches the
// ceiling, the collector runs harder instead of letting the heap
// grow. Past the ceiling, the runtime caps garbage collection at half
// the process's CPU and lets the heap grow anyway. So a genuine
// memory spike degrades into slowness, and never causes a
// heap-exhaustion crash. Left unset, the ceiling scales with the
// features the cluster declares, because those features are what fill
// the heap. A minimum viable control plane fits comfortably under a
// quarter of the machine's memory. The helm feature, and everything
// that requires it, such as traefik, brings the chart renderer and
// Traefik's CRDs into the process, and needs seven sixteenths of the
// machine's memory. That budget leaves a 1GB machine room for the
// container runtime, the pods themselves, the kernel's own caches,
// and free headroom for the next convergence. GOGC sets the everyday
// pace under the ceiling, 50 by default: it collects garbage at fifty
// percent heap growth instead of Go's own hundred percent. This
// trades a little CPU all the time, so the process's resting size
// stays near its live data size.
//
// The two knobs fail in opposite directions, and reading the symptom
// tells which way an experiment went wrong. A ceiling turned off, or
// set too high, leaves the collector nothing to push against, so the
// heap grows until the kernel's OOM killer is the only backstop, and
// the process dies outright under a spike. A ceiling set too tight
// makes the collector run against a wall it cannot clear: it collects
// again and again to hold the line, burning CPU on collection with
// little real work done. The symptom is high pure-user CPU on the k3s
// process with no matching workload, and the control plane slows to a
// crawl without ever crashing. The healthy setting sits between
// these, and the defaults are that setting for the fleets liken
// targets.
//
// containerd and the shims inherit this environment from k3s. This
// is deliberate and cheap: they are Go programs a fraction of the
// size of the limit, so the ceiling never constrains them, and the
// GC pace keeps them lean too. Workload processes inherit nothing.
// Their environments come from their pod specs.
func k3sRuntimeEnv(spec cluster.K3sRuntimeSpec, memoryBytes uint64, helm bool) []string {
	var env []string
	if limit, off, err := spec.GoMemoryLimitBytes(memoryBytes, helm); err == nil && !off {
		env = append(env, fmt.Sprintf("GOMEMLIMIT=%dMiB", limit/(1<<20)))
	}
	env = append(env, fmt.Sprintf("GOGC=%d", spec.GoGCPercent()))
	return env
}

// k3sMemoryDiscipline is the runtime environment that startK3s gives
// every k3s process it launches. writeK3sBootConfig derives this
// environment beside the boot drop-in, at boot and again on every
// applied restart. So a restart that changes the cluster's features
// re-scales the ceiling on the same bounce that reconfigures k3s.
var k3sMemoryDiscipline []string

// startK3s launches k3s in the machine's role, and returns the
// running command and its log file. The log file must stay open as
// long as the process writes to it. The console copy flows through
// an in-process pipe.
func startK3s(role api.Role) (*exec.Cmd, io.Closer, error) {
	// k3s's output goes to two places: a file, and the console. On
	// the console, the output arrives live, line-buffered, and
	// prefixed, so a reader can tell it apart from liken's own
	// messages. On a machine with no shell, the console is the only
	// way to read a log. The file is what the k3s log relay tails
	// into the cluster. The file writer caps a boot's log, so a k3s
	// that writes a lot of output cannot fill the filesystem it
	// shares with etcd (logrotate.go).
	logf, err := openCappedLog(k3sLog, k3sLogCap)
	if err != nil {
		return nil, nil, err
	}

	// This is the one place where liken's role vocabulary meets
	// k3s's own vocabulary: a leader runs `k3s server`, and a
	// follower runs `k3s agent` (cluster/cluster.go). Configuration
	// lives in files, not in flags. A leader's k3s reads
	// /etc/rancher/k3s/config.yaml on its own. A follower's k3s is
	// pointed at its own file, because the leader-only config file
	// would otherwise be misread as unknown flags. k3s.go joins both
	// config files with this boot's derived drop-in before this code
	// runs.
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

// stopK3s asks k3s to exit, and waits for the reaper to confirm the
// exit. If k3s takes too long to exit, stopK3s escalates to SIGKILL.
// stopK3s only sends the signal and receives the confirmation. The
// reaper stays the sole authority on calling wait (the file comment's
// one rule).
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
// working Kubernetes node. It polls `k3s kubectl get nodes`, which
// reads the admin kubeconfig that k3s writes once its API starts
// serving. reportWhenReady prints the node's status as the status
// changes: registering, NotReady, and finally Ready. reportWhenReady
// is a machine-plane component. Its work completes once it prints
// the report, so every exit path returns nil.
//
// This reporter, and reportPods below, is the one resident of the
// machine plane that k3s does not depend on. The two-planes rule
// (components.go) would normally send a component like this into the
// cluster. It stays in the machine plane by deliberate exception. Its
// entire subject is the window before the cluster can report on
// itself: while k3s is starting, and before the operator pod exists.
// On a machine with no shell, the console is the only place that can
// show this information. The operator takes over reporting the
// moment the operator runs. reportWhenReady is the bridge to that
// moment.
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

// reportPods prints the system pods as they start, after the node
// goes Ready. It stops printing once every pod reaches Running. This
// is the console equivalent of watching `kubectl get pods -A` until
// the output settles.
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

// pollAndReport is the pattern both reporters share: fetch a kubectl
// table on an interval, print the table under the prefix whenever it
// changes, and return true the moment the table satisfies settled.
// pollAndReport returns false when patience runs out, or when the
// plane shuts down. Printing only the changes keeps the console
// readable: a table that stays unchanged for a minute produces no
// lines at all.
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

// containsReady looks for the word Ready as a whole status field.
// This means NotReady does not match.
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
// Running or Completed. The status field is kubectl's fourth column.
func podsSettled(out string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[3] != "Running" && fields[3] != "Completed" {
			return false
		}
	}
	return true
}

// runNarrated executes a command, echoes its output live to the
// console with a prefix like k3s's output, and reports whether the
// command exited cleanly. Use runNarrated for commands whose output
// matters to someone watching a boot, such as mke2fs reporting on the
// filesystem it creates. run's captured-output shape would hide that
// output instead.
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

// run executes a command and returns its output. It waits for the
// command through the reaper (see the file comment: nobody but the
// reaper calls wait). Reading the pipe to EOF shows that the process
// finished writing. The reaper reports how the process died.
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
	// Reading the pipe to EOF shows that the process finished
	// writing. The reaper reports how the process died.
	buf, _ := io.ReadAll(out)
	status := deaths.await(cmd.Process.Pid)
	_ = cmd.Process.Release()
	return strings.TrimRight(string(buf), "\r\n"), status.Exited() && status.ExitStatus() == 0
}
