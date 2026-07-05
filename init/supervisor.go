package main

// Supervising k3s.
//
// The entire service-management story of this OS is this file: start
// one process, and when it dies, start it again. Everything a
// traditional init manages above that line — orderings, dependencies,
// sockets, timers — belongs to Kubernetes, which is the process being
// supervised.
//
// There's one genuinely subtle problem here, and it's worth
// understanding because it's the classic bug in every homemade PID 1.
// As PID 1 we must reap *every* dead process on the machine (orphans
// reparent to us), so somewhere a loop calls wait(-1), "collect any
// corpse". But the supervisor also needs the exit status of the
// specific child it started — and wait(-1) in one goroutine races
// wait(pid) in another: whoever collects the corpse first destroys it,
// and the loser gets an error instead of a status. The fix is a single
// authority: only the reaper ever waits, and everyone else subscribes.
// The reaper posts every death it collects; deaths nobody has claimed
// yet are parked (a child can die before its parent even asks), and
// awaitDeath picks up parked statuses or blocks until one arrives.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// lineWriter forwards each complete line it receives to the console
// with a prefix. Child processes write in arbitrary chunks; buffering
// to line boundaries keeps their output and liken's narration from
// interleaving mid-sentence.
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
// ask — what environment did children inherit, and do the tools they
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

// superviseK3s runs k3s forever. It never returns: a machine whose
// k3s has exited is a machine whose k3s is about to start again —
// with backoff, so a fast crash loop doesn't melt the console.
//
// The liken.oneshot boot parameter turns the forever off: k3s runs
// once and its death powers the machine down. That makes a k3s
// failure observable from outside — QEMU exits, the console is a
// complete record — which is what debugging and automated runs need
// from a machine with no shell.
func superviseK3s() {
	backoff := time.Second
	for {
		started := time.Now()
		if err := runK3sOnce(); err != nil {
			fmt.Fprintf(os.Stderr, "liken: k3s: %v\n", err)
		}
		if bootParam("liken.oneshot") {
			postMortem()
			fmt.Println("liken: one-shot boot, not restarting k3s; powering off")
			powerOff()
			return
		}

		// A k3s that ran for a while earned a fresh backoff; one that
		// died immediately doubles its penalty box, capped so a truly
		// broken configuration still retries every half minute.
		if time.Since(started) > time.Minute {
			backoff = time.Second
		} else if backoff < 30*time.Second {
			backoff *= 2
		}
		fmt.Printf("liken: restarting k3s in %s\n", backoff)
		time.Sleep(backoff)
	}
}

func runK3sOnce() error {
	// k3s's words go two places: a file, and the console — live,
	// line-buffered, and prefixed so they read as quotation rather
	// than liken's own voice. On a machine with no shell, the console
	// is the only log reader there is.
	logf, err := os.OpenFile(k3sLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()

	// k3s reads /etc/rancher/k3s/config.yaml on its own; the empty
	// argument list is the configuration story working as intended.
	cmd := exec.Command(k3sBinary, "server")
	cmd.Stdout = io.MultiWriter(logf, &lineWriter{prefix: "k3s | "})
	cmd.Stderr = io.MultiWriter(logf, &lineWriter{prefix: "k3s | "})
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting k3s: %w", err)
	}
	fmt.Printf("liken: k3s server started (pid %d), logs in %s\n", cmd.Process.Pid, k3sLog)

	status := awaitDeath(cmd.Process.Pid)
	// Release, not Wait: the reaper already collected the status —
	// that's the single-authority rule — so Wait would error; Release
	// just lets go of the process handle.
	_ = cmd.Process.Release()

	fmt.Printf("liken: k3s exited (%s)\n", describeExit(status))
	return nil
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

// reportWhenReady watches, from a safe distance, for the moment the
// machine is actually a Kubernetes node: it polls `k3s kubectl get
// nodes` (which reads the admin kubeconfig k3s writes once its API is
// serving) and narrates the node's status as it changes — registering,
// NotReady, and finally Ready, the moment this machine is a working
// Kubernetes node.
func reportWhenReady() {
	last := ""
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
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
			reportPods()
			return
		}
	}
	fmt.Println("liken: gave up waiting for the node to be Ready (k3s may still get there)")
}

// reportPods narrates the system pods coming to life after the node
// goes Ready, then goes quiet once everything is Running — the console
// equivalent of watching `kubectl get pods -A` settle.
func reportPods() {
	last := ""
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
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
	fmt.Println("liken: system pods have not settled; the narration above is the state of things")
}

// containsReady looks for the word Ready as a status, not as part of
// NotReady — the difference between arriving and almost.
func containsReady(out string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "Ready" {
			return true
		}
	}
	return false
}

// runNarrated executes a command with its output quoted live on the
// console, k3s-style, and reports whether it exited cleanly — for
// commands whose words matter to someone watching a boot (mke2fs
// announcing a new filesystem), where run's captured-output shape
// would swallow them.
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
// the reaper (see the file comment — nobody but the reaper calls
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
