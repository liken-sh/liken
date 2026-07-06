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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
)

// lineWriter forwards each complete line it receives to the console
// with a prefix. Child processes write in arbitrary chunks; buffering
// to line boundaries keeps their output and liken's own messages from
// interleaving mid-line.
type lineWriter struct {
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
		fmt.Printf("%s%s", w.prefix, line)
	}
	return len(p), nil
}

var (
	deathsMu  sync.Mutex
	waiters   = map[int]chan unix.WaitStatus{}
	unclaimed = map[int]unix.WaitStatus{}
)

// recordDeath is called by the reaper, the only place wait() happens.
func recordDeath(pid int, status unix.WaitStatus) {
	deathsMu.Lock()
	defer deathsMu.Unlock()
	if ch, ok := waiters[pid]; ok {
		ch <- status
		delete(waiters, pid)
	} else {
		unclaimed[pid] = status
	}
}

// awaitDeath blocks until the reaper has collected the given pid.
func awaitDeath(pid int) unix.WaitStatus {
	deathsMu.Lock()
	if status, ok := unclaimed[pid]; ok {
		delete(unclaimed, pid)
		deathsMu.Unlock()
		return status
	}
	ch := make(chan unix.WaitStatus, 1)
	waiters[pid] = ch
	deathsMu.Unlock()
	return <-ch
}

const (
	k3sBinary = "/bin/k3s"
	k3sLog    = "/var/log/k3s.log"
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

// superviseK3s runs k3s forever, with one interruption it honors: a
// reboot request from the operator. It never returns otherwise:
// whenever k3s exits, it gets restarted, with backoff, so a fast
// crash loop doesn't flood the console.
//
// The reboot channel is selected in *both* of the supervisor's
// states: while k3s runs, and during the backoff sleep between
// restarts. That second select is what makes the request unable to
// race the restart decision: they're alternatives of one select in
// one goroutine, so an intent that arrives while k3s is crash-looping
// neither waits out the sleep nor collides with a restart.
//
// The liken.oneshot boot parameter disables the restart: k3s runs
// once and its exit powers the machine down. That makes a k3s failure
// observable from outside (QEMU exits, the console is a complete
// record), which is what debugging and automated runs need from a
// machine with no shell. A reboot intent is honored even in oneshot:
// under QEMU's -no-reboot the restart is a clean exit, which is
// exactly what a bounded harness run wants.
func superviseK3s(role machine.Role, reboot <-chan machine.RebootIntent) {
	backoff := time.Second
	for {
		started := time.Now()
		cmd, logf, err := startK3s(role)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: k3s: %v\n", err)
		} else {
			// The reaper is the only waiter (see the file comment);
			// this goroutine just carries its answer into the select.
			died := make(chan unix.WaitStatus, 1)
			go func() { died <- awaitDeath(cmd.Process.Pid) }()

			select {
			case status := <-died:
				_ = cmd.Process.Release()
				logf.Close()
				fmt.Printf("liken: k3s exited (%s)\n", describeExit(status))
			case intent := <-reboot:
				stopK3s(cmd.Process.Pid, died)
				_ = cmd.Process.Release()
				logf.Close()
				rebootMachine(intent) // never returns
			}
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
		fmt.Printf("liken: restarting k3s in %s\n", backoff)
		select {
		case <-time.After(backoff):
		case intent := <-reboot:
			rebootMachine(intent) // k3s is already dead; nothing to stop
		}
	}
}

// startK3s launches k3s in the machine's role and hands back the
// running command and its log file (which must stay open as long as
// the process writes; the console copy flows through an in-process
// pipe).
func startK3s(role machine.Role) (*exec.Cmd, *os.File, error) {
	// k3s's output goes two places: a file, and the console, where it
	// arrives live, line-buffered, and prefixed so it's
	// distinguishable from liken's own messages. On a machine with no
	// shell, the console is the only way to read a log.
	logf, err := os.OpenFile(k3sLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}

	// This is the one place liken's role vocabulary meets k3s's: a
	// leader runs `k3s server`, a follower runs `k3s agent`
	// (machine/cluster.go). Configuration lives in files, not flags:
	// a leader's k3s reads /etc/rancher/k3s/config.yaml on its own,
	// and a follower's is pointed at its own file (whose leader-only
	// sibling would otherwise be misread as unknown flags). Both were
	// joined with this boot's derived drop-in by k3s.go before we got
	// here.
	args := []string{"server"}
	if role == machine.RoleFollower {
		args = []string{"agent", "--config", k3sAgentConfig}
	}
	cmd := exec.Command(k3sBinary, args...)
	cmd.Stdout = io.MultiWriter(logf, &lineWriter{prefix: "k3s | "})
	cmd.Stderr = io.MultiWriter(logf, &lineWriter{prefix: "k3s | "})
	if err := cmd.Start(); err != nil {
		logf.Close()
		return nil, nil, fmt.Errorf("starting k3s: %w", err)
	}
	fmt.Printf("liken: k3s %s started (pid %d), logs in %s\n", role, cmd.Process.Pid, k3sLog)
	return cmd, logf, nil
}

// stopK3s asks k3s to exit and waits for the reaper's confirmation,
// escalating to SIGKILL if it dawdles. It only signals and receives;
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
// when the story has been told, so every exit path returns nil.
func reportWhenReady(ctx context.Context) error {
	last := ""
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		if !sleepUnlessCancelled(ctx, 3*time.Second) {
			return nil
		}
		out, ok := run(k3sBinary, "kubectl", "get", "nodes", "--no-headers")
		if !ok || out == "" {
			continue
		}
		if out != last {
			last = out
			for line := range strings.SplitSeq(out, "\n") {
				fmt.Printf("liken: node: %s\n", line)
			}
		}
		if containsReady(out) {
			fmt.Println("liken: kubernetes is up")
			reportPods(ctx)
			return nil
		}
	}
	fmt.Println("liken: gave up waiting for the node to be Ready (k3s may still get there)")
	return nil
}

// reportPods prints the system pods starting up after the node goes
// Ready, then goes quiet once everything is Running: the console
// equivalent of watching `kubectl get pods -A` settle.
func reportPods(ctx context.Context) {
	last := ""
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		if !sleepUnlessCancelled(ctx, 5*time.Second) {
			return
		}
		out, ok := run(k3sBinary, "kubectl", "get", "pods", "-A", "--no-headers")
		if !ok || out == "" {
			continue
		}
		if out != last {
			last = out
			for line := range strings.SplitSeq(out, "\n") {
				fmt.Printf("liken: pod: %s\n", line)
			}
		}
		settled := true
		for line := range strings.SplitSeq(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 4 && fields[3] != "Running" && fields[3] != "Completed" {
				settled = false
			}
		}
		if settled {
			fmt.Println("liken: all system pods are settled")
			return
		}
	}
	fmt.Println("liken: system pods have not settled; see the pod status lines above")
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

// runNarrated executes a command with its output echoed live to the
// console, prefixed like k3s's, and reports whether it exited
// cleanly. It's for commands whose output matters to someone watching
// a boot (mke2fs describing the filesystem it's making), where run's
// captured-output shape would hide it.
func runNarrated(prefix, path string, args ...string) bool {
	cmd := exec.Command(path, args...)
	w := &lineWriter{prefix: prefix}
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "liken: starting %s: %v\n", path, err)
		return false
	}
	status := awaitDeath(cmd.Process.Pid)
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
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := out.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	status := awaitDeath(cmd.Process.Pid)
	_ = cmd.Process.Release()
	return strings.TrimRight(string(buf), "\r\n"), status.Exited() && status.ExitStatus() == 0
}
