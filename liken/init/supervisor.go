package main

// This file runs k3s and starts it again when it exits. Kubernetes
// orders and restarts everything above k3s, so init needs no service
// manager.
//
// As PID 1, init must collect the exit status of every process that
// exits, because the kernel reparents orphans to it. The code that
// starts a child also needs that child's status. When one goroutine
// calls wait(-1) and another calls wait(pid), whichever returns first
// takes the status and the other gets ECHILD. So only the reaper calls
// wait, and it records each status in the death registry, where the
// code that started the child reads it.

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

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// lineWriter writes each complete line to dest with prefix in front,
// and buffers a partial line until its newline arrives. A child writes
// in chunks of any size, so without the buffer its output would break
// into init's own messages in the middle of a line. dest is usually
// `console`, which bypasses kmsg (console.go gives the reason).
type lineWriter struct {
	dest   io.Writer
	prefix string
	buf    bytes.Buffer
}

// teeOutput sends a command's stdout and stderr to its log file and to
// the console. Both get the same writer because the capped log file
// has no lock. os/exec starts a copying goroutine for each distinct
// writer, and a rotation in one goroutine would close the file while
// the other writes to it. With one writer, os/exec uses one pipe and
// one goroutine.
func teeOutput(cmd *exec.Cmd, logf, console io.Writer) {
	out := io.MultiWriter(logf, &lineWriter{dest: console, prefix: "k3s | "})
	cmd.Stdout = out
	cmd.Stderr = out
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// ReadString consumed the partial line. Put it back.
			w.buf.WriteString(line)
			break
		}
		fmt.Fprintf(w.dest, "%s%s", w.prefix, line)
	}
	return len(p), nil
}

// reap collects the exit status of every child that exits, until ctx
// is cancelled, and records each one in the death registry. It is the
// only caller of wait in init.
//
// Several SIGCHLDs can arrive as one, so each wakeup collects every
// exited child. reap subscribes to SIGCHLD before its first
// collection, so a child that exited before the subscription is
// collected by that first pass.
func reap(ctx context.Context) error {
	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, unix.SIGCHLD)
	defer signal.Stop(sigchld)
	for {
		for {
			// With WNOHANG, Wait4 returns pid 0 when no child has exited.
			var status unix.WaitStatus
			pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			if pid <= 0 || err != nil {
				break
			}
			deaths.record(pid, status)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-sigchld:
		}
	}
}

// deathRegistry passes exit statuses from the reaper to the code that
// started each process. The reaper calls `record`. `await` returns a
// status that `record` already stored, or waits for `record` to send
// it.
//
// `record` stores a status only for a pid that init started with
// `start` or marked with `expect`. The reaper also collects orphans
// such as containerd shims. Nobody awaits an orphan, so its status
// would stay in the registry, and a later child that reuses the pid
// would read it as its own.
type deathRegistry struct {
	mu        sync.Mutex
	waiters   map[int]chan unix.WaitStatus
	unclaimed map[int]unix.WaitStatus
	expected  map[int]bool
}

var deaths = &deathRegistry{
	waiters:   map[int]chan unix.WaitStatus{},
	unclaimed: map[int]unix.WaitStatus{},
	expected:  map[int]bool{},
}

// start starts cmd and marks its pid as expected. It holds the lock
// across cmd.Start because the child can exit and be collected before
// Start returns. `record` then waits for the lock, and finds the pid
// expected when it gets it.
func (d *deathRegistry) start(cmd *exec.Cmd) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := cmd.Start(); err != nil {
		return err
	}
	d.expected[cmd.Process.Pid] = true
	return nil
}

// expect marks a pid as expected, for a process that `start` did not
// start.
func (d *deathRegistry) expect(pid int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.expected[pid] = true
}

// record sends the status to a waiting `await`, stores it for an
// expected pid, or drops it for any other pid.
func (d *deathRegistry) record(pid int, status unix.WaitStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ch, ok := d.waiters[pid]; ok {
		ch <- status
		delete(d.waiters, pid)
		delete(d.expected, pid)
	} else if d.expected[pid] {
		d.unclaimed[pid] = status
	}
}

// await returns the exit status of pid, and blocks until the reaper
// collects it. pid must come from `start` or `expect`. For any other
// pid that has already exited, `record` dropped the status and await
// blocks forever.
func (d *deathRegistry) await(pid int) unix.WaitStatus {
	d.mu.Lock()
	if status, ok := d.unclaimed[pid]; ok {
		delete(d.unclaimed, pid)
		delete(d.expected, pid)
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

	// The k3s log is on clusterState, in a directory apart from the
	// files that k3s writes. On a machine with a persistent disk, the
	// log survives a reboot, and the log relay mounts the directory at
	// a fixed path. containerd chooses the path of its own log, and
	// init only rotates it (logrotate.go).
	likenLogDir   = "/var/lib/rancher/k3s/liken"
	k3sLog        = likenLogDir + "/k3s.log"
	containerdLog = "/var/lib/rancher/k3s/agent/containerd/containerd.log"
)

// postMortem runs at the end of a one-shot boot. The machine has no
// shell, so it prints what a person would check by hand: the PATH that
// children inherit, and whether `iptables` resolves and runs.
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
		fmt.Printf("liken: post-mortem: iptables -V did not succeed, output %q\n", out)
	}
}

// superviseK3s runs k3s and starts it again whenever it exits. While
// it waits, it also handles reboot requests from the operator and
// restart requests. A restart request applies staged changes that k3s
// reads only at start (cluster/changes.go): superviseK3s stops k3s
// with SIGTERM and starts it again while the machine stays up. The
// containerd shims keep the containers running while k3s is down, so
// the pods stay up too. superviseK3s returns only on a one-shot boot.
//
// `applyRestart` runs while k3s still serves, so the new configuration
// is written before k3s stops. It returns false for a request that
// changes nothing, such as a duplicate, and k3s keeps running. A
// restart starts k3s again at once, with no backoff and no one-shot
// check, because those exist for k3s failures.
//
// `afterStop` runs after a restart stops k3s and before it starts
// again. main passes removeQueuedRetractions, which deletes the
// queued manifest files of a removed feature while k3s cannot act on
// the deletion (retractFeatureManifests gives the reason).
//
// When k3s exits on its own, superviseK3s starts it again after a
// backoff, so a k3s that crashes at once does not flood the console.
// The backoff waits in the same select as the request channels, so a
// request during the wait takes effect at once.
//
// With the `liken.oneshot` boot parameter, the first exit of k3s
// powers the machine off, so QEMU exits with the whole boot on the
// console, for debugging and for automated test runs. A reboot request
// still works, and under QEMU's -no-reboot flag it is a clean exit.
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
			// The goroutine turns the blocking await into a channel for
			// the select below.
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
					stopK3s(cmd.Process, died)
					_ = cmd.Process.Release()
					// rebootMachine unmounts clusterState, and the open
					// log file there would make the unmount fail with
					// EBUSY.
					logf.Close()
					rebootMachine(intent) // never returns
				case intent := <-restarts:
					if !applyRestart(intent) {
						continue running
					}
					fmt.Println("liken: restarting k3s to apply the staged changes")
					stopK3s(cmd.Process, died)
					afterStop()
					_ = cmd.Process.Release()
					logf.Close()
					bounced = true
					break running
				}
			}
		}
		if bounced {
			continue // skips the one-shot check and the backoff
		}
		if bootParam("liken.oneshot") {
			postMortem()
			fmt.Println("liken: one-shot boot, not restarting k3s; powering off")
			powerOff()
			return
		}

		// A k3s that ran for more than a minute resets the backoff to one
		// second. Otherwise the backoff doubles up to 32 seconds, so a
		// k3s with a broken configuration still retries about twice a
		// minute.
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
			rebootMachine(intent) // k3s has already exited
		case intent := <-restarts:
			// k3s is already down, so apply the changes and start it
			// without waiting out the backoff.
			_ = applyRestart(intent)
			afterStop()
		}
	}
}

// k3sRuntimeEnv returns the GOMEMLIMIT and GOGC variables for k3s,
// from the cluster's spec.runtime.k3s and this machine's memory
// (cluster/runtime.go). A field that is unset or "off" adds no
// variable, and k3s uses Go's default for it.
//
// GOMEMLIMIT is a soft limit. Near it, the Go collector runs more
// often. Above it, the collector uses at most half of the process's
// CPU time and the heap keeps growing, so a memory spike makes k3s slow
// and does not crash it. GOGC sets how far the heap grows, as a
// percent, before a collection below the limit.
//
// Go reads both variables only at startup. containerd and the shims
// that k3s starts inherit them, and are far below the limit. Workload
// containers do not inherit them, because their environment comes from
// the pod spec.
func k3sRuntimeEnv(spec cluster.K3sRuntimeSpec, memoryBytes uint64) []string {
	var env []string
	if limit, off, err := spec.GoMemoryLimitBytes(memoryBytes); err == nil && !off {
		env = append(env, fmt.Sprintf("GOMEMLIMIT=%dMiB", limit/(1<<20)))
	}
	if gc, ok := spec.GoGCPercent(); ok {
		env = append(env, fmt.Sprintf("GOGC=%d", gc))
	}
	return env
}

// k3sMemoryDiscipline is the output of k3sRuntimeEnv, which startK3s
// adds to the environment of each k3s it starts. writeK3sBootConfig
// sets it at boot and on each applied restart, so a restart that
// changes spec.runtime.k3s takes effect.
var k3sMemoryDiscipline []string

// startK3s starts k3s for the machine's role and returns the command
// and its log file. The caller closes the file after k3s exits.
func startK3s(role api.Role) (*exec.Cmd, io.Closer, error) {
	// The log relay tails this file into the cluster. The cap keeps a
	// verbose k3s from filling the filesystem that etcd also uses
	// (logrotate.go).
	logf, err := openCappedLog(k3sLog, k3sLogCap)
	if err != nil {
		return nil, nil, err
	}

	// A leader runs `k3s server`, which reads
	// /etc/rancher/k3s/config.yaml by default. A follower runs
	// `k3s agent` with its own file, because the agent would also read
	// the default file and reject the server's keys as unknown flags.
	// k3s.go writes both files before startK3s runs.
	args := []string{"server"}
	if role == api.RoleFollower {
		args = []string{"agent", "--config", k3sAgentConfig}
	}
	cmd := exec.Command(k3sBinary, args...)
	if len(k3sMemoryDiscipline) > 0 {
		cmd.Env = append(os.Environ(), k3sMemoryDiscipline...)
	}
	teeOutput(cmd, logf, console)
	if err := deaths.start(cmd); err != nil {
		logf.Close()
		return nil, nil, fmt.Errorf("starting k3s: %w", err)
	}
	fmt.Printf("liken: k3s %s started (pid %d), logs in %s\n", role, cmd.Process.Pid, k3sLog)
	return cmd, logf, nil
}

// stopK3s sends SIGTERM to k3s and waits for the reaper's status on
// died. After 30 seconds it sends SIGKILL.
//
// It signals through os.Process, which holds a pidfd, and not through
// the pid. After the reaper collects the process, a signal through the
// pidfd returns os.ErrProcessDone. The pid can already belong to
// another process by then.
func stopK3s(p *os.Process, died <-chan unix.WaitStatus) {
	fmt.Printf("liken: stopping k3s (pid %d)\n", p.Pid)
	_ = p.Signal(unix.SIGTERM)
	select {
	case status := <-died:
		fmt.Printf("liken: k3s exited (%s)\n", describeExit(status))
	case <-time.After(30 * time.Second):
		fmt.Fprintln(os.Stderr, "liken: k3s ignored SIGTERM for 30s; killing it")
		_ = p.Kill()
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

// reportWhenReady prints the node's status on the console each time it
// changes, until the node is Ready, and then calls reportPods. It
// polls `k3s kubectl get nodes`, which fails until k3s has written the
// admin kubeconfig and serves its API. It returns nil in every case,
// and has an error result only to match machinePlane.start.
//
// k3s does not depend on this report, so by the rule in components.go
// it would run in the cluster. It runs on the machine plane because it
// reports the boot before the operator pod exists, and on a machine
// with no shell the console is the only place to show that.
func reportWhenReady(ctx context.Context) error {
	fetch := func(timeout time.Duration) (string, bool) {
		return runWithin(timeout, k3sBinary, kubectlGet(timeout, "nodes")...)
	}
	if pollAndReport(ctx, 3*time.Second, 5*time.Minute, "node", fetch, containsReady) {
		fmt.Println("liken: the node is Ready")
		reportPods(ctx)
	} else if ctx.Err() == nil {
		fmt.Println("liken: the node was not Ready after 5 minutes, and k3s may still make it Ready")
	}
	return nil
}

// reportPods prints the pod table each time it changes, until every
// pod is Running or Completed, or for at most five minutes.
func reportPods(ctx context.Context) {
	fetch := func(timeout time.Duration) (string, bool) {
		return runWithin(timeout, k3sBinary, kubectlGet(timeout, "pods", "-A")...)
	}
	if pollAndReport(ctx, 5*time.Second, 5*time.Minute, "pod", fetch, podsSettled) {
		fmt.Println("liken: all pods are Running or Completed")
	} else if ctx.Err() == nil {
		fmt.Println("liken: not all pods were Running or Completed after 5 minutes, see the pod status lines above")
	}
}

// kubectlCallTimeout limits one kubectl call of the boot reports.
// kubectl has no request timeout by default, so an API server that
// accepts the connection and never replies would block the report
// until its timeout runs out. A call that times out skips one table.
const kubectlCallTimeout = 10 * time.Second

// kubectlGet returns the arguments for `k3s kubectl get` with
// --request-timeout. That flag limits each HTTP request, not the whole
// run. With no discovery cache, kubectl retries discovery several
// times and can wait the full timeout on each, so `runWithin` limits
// the run. The flag still ends a hung request early, so that a retry
// can reach a server that has started to reply. The timeout is at
// least one millisecond because kubectl reads zero as no timeout.
func kubectlGet(timeout time.Duration, args ...string) []string {
	timeout = max(timeout.Round(time.Millisecond), time.Millisecond)
	out := []string{"kubectl", "get"}
	out = append(out, args...)
	return append(out, "--no-headers", "--request-timeout="+timeout.String())
}

// pollAndReport fetches a kubectl table every interval and prints it,
// with prefix on each line, when it differs from the last table. It
// returns true when settled accepts a table, and false when the timeout
// runs out or ctx is cancelled.
//
// Each fetch gets kubectlCallTimeout or the timeout that is left,
// whichever is shorter. So a server that never replies delays the
// return by at most the timeout plus one interval.
func pollAndReport(ctx context.Context, interval, timeout time.Duration, prefix string,
	fetch func(timeout time.Duration) (string, bool), settled func(string) bool) bool {
	last := ""
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !sleepUnlessCancelled(ctx, interval) {
			return false
		}
		left := time.Until(deadline)
		if left <= 0 {
			return false
		}
		out, ok := fetch(min(kubectlCallTimeout, left))
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

// containsReady reports whether the second column of any line is
// exactly Ready, so NotReady does not match.
func containsReady(out string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "Ready" {
			return true
		}
	}
	return false
}

// podsSettled reports whether every pod in the table is Running or
// Completed. The status is kubectl's fourth column.
func podsSettled(out string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[3] != "Running" && fields[3] != "Completed" {
			return false
		}
	}
	return true
}

// runWithin runs a command like `run`, and kills it when the timeout
// passes. The kill goes through os.Process for the reason that stopK3s
// gives. A goroutine reads the output while runWithin waits, because a
// command that fills the pipe blocks until someone reads it, and would
// never exit.
func runWithin(timeout time.Duration, path string, args ...string) (string, bool) {
	cmd := exec.Command(path, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", false
	}
	cmd.Stderr = nil
	if err := deaths.start(cmd); err != nil {
		return "", false
	}
	pid := cmd.Process.Pid
	died := make(chan unix.WaitStatus, 1)
	go func() { died <- deaths.await(pid) }()
	output := make(chan []byte, 1)
	go func() {
		buf, _ := io.ReadAll(out)
		_ = out.Close()
		output <- buf
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var status unix.WaitStatus
	select {
	case status = <-died:
	case <-timer.C:
		if cmd.Process.Kill() == nil {
			fmt.Fprintf(os.Stderr, "liken: %s did not finish within %s; killed it\n", path, timeout)
		}
		status = <-died
	}
	_ = cmd.Process.Release()
	buf := <-output
	return strings.TrimRight(string(buf), "\r\n"), status.Exited() && status.ExitStatus() == 0
}

// runNarrated runs a command, writes its output to the console as it
// arrives, with prefix on each line, and reports whether it exited with
// status 0. It is for commands whose output a person watching the boot
// should see, such as mke2fs.
func runNarrated(prefix, path string, args ...string) bool {
	cmd := exec.Command(path, args...)
	w := &lineWriter{dest: console, prefix: prefix}
	cmd.Stdout = w
	cmd.Stderr = w
	if err := deaths.start(cmd); err != nil {
		fmt.Fprintf(os.Stderr, "liken: starting %s: %v\n", path, err)
		return false
	}
	status := deaths.await(cmd.Process.Pid)
	_ = cmd.Process.Release()
	return status.Exited() && status.ExitStatus() == 0
}

// run runs a command and returns its stdout and whether it exited with
// status 0.
func run(path string, args ...string) (string, bool) {
	cmd := exec.Command(path, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", false
	}
	cmd.Stderr = nil
	if err := deaths.start(cmd); err != nil {
		return "", false
	}
	// cmd.Wait would close the pipe, but only the reaper waits, so run
	// closes it.
	buf, _ := io.ReadAll(out)
	_ = out.Close()
	status := deaths.await(cmd.Process.Pid)
	_ = cmd.Process.Release()
	return strings.TrimRight(string(buf), "\r\n"), status.Exited() && status.ExitStatus() == 0
}
