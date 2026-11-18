package main

// These tests cover the supervisor's helpers: exit descriptions, line
// prefixing, the death registry, and the boot reporters. A test that
// starts and stops k3s itself runs under QEMU.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/cluster"
)

// Each field of the runtime section adds only its own variable, and an
// unset section adds none, so k3s runs on Go's defaults.
func TestK3sRuntimeEnv(t *testing.T) {
	gib := uint64(1024 * 1024 * 1024)
	gogc := func(n int) *int { return &n }
	cases := map[string]struct {
		spec cluster.K3sRuntimeSpec
		mem  uint64
		want []string
	}{
		"absent":     {cluster.K3sRuntimeSpec{}, gib, nil},
		"percent":    {cluster.K3sRuntimeSpec{GoMemoryLimit: "25%"}, gib, []string{"GOMEMLIMIT=256MiB"}},
		"absolute":   {cluster.K3sRuntimeSpec{GoMemoryLimit: "448Mi"}, gib, []string{"GOMEMLIMIT=448MiB"}},
		"off":        {cluster.K3sRuntimeSpec{GoMemoryLimit: "off"}, gib, nil},
		"customGoGC": {cluster.K3sRuntimeSpec{GoGC: gogc(80)}, gib, []string{"GOGC=80"}},
		"both":       {cluster.K3sRuntimeSpec{GoMemoryLimit: "512Mi", GoGC: gogc(50)}, gib, []string{"GOMEMLIMIT=512MiB", "GOGC=50"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := k3sRuntimeEnv(tc.spec, tc.mem); !slices.Equal(got, tc.want) {
				t.Errorf("the environment is %v, want %v", got, tc.want)
			}
		})
	}
}

// A wait status is the kernel's packed value: the exit code is in the
// second byte and the terminating signal is in the low seven bits.
func TestDescribeExitForACleanExit(t *testing.T) {
	if got := describeExit(unix.WaitStatus(0)); got != "status 0" {
		t.Errorf("the description is %q, want %q", got, "status 0")
	}
}

func TestDescribeExitForAFailure(t *testing.T) {
	if got := describeExit(unix.WaitStatus(1 << 8)); got != "status 1" {
		t.Errorf("the description is %q, want %q", got, "status 1")
	}
}

func TestDescribeExitForASignal(t *testing.T) {
	if got := describeExit(unix.WaitStatus(unix.SIGKILL)); got != "signal killed" {
		t.Errorf("the description is %q, want %q", got, "signal killed")
	}
}

func TestContainsReadyMatchesTheWholeField(t *testing.T) {
	out := "NAME     STATUS   ROLES\nnode-1   Ready    control-plane"
	if !containsReady(out) {
		t.Error("containsReady is false for a Ready node, want true")
	}
}

func TestContainsReadyRejectsNotReady(t *testing.T) {
	out := "NAME     STATUS     ROLES\nnode-1   NotReady   control-plane"
	if containsReady(out) {
		t.Error("containsReady is true for a NotReady node, want false")
	}
}

func TestLineWriterBuffersPartialLines(t *testing.T) {
	var dest bytes.Buffer
	w := &lineWriter{dest: &dest, prefix: "k3s | "}
	if n, err := w.Write([]byte("partial")); n != 7 || err != nil {
		t.Fatalf("Write returned %d, %v, want 7, nil", n, err)
	}
	if w.buf.String() != "partial" {
		t.Errorf("the buffer holds %q, want %q", w.buf.String(), "partial")
	}
	if _, err := w.Write([]byte(" line\nnext")); err != nil {
		t.Fatalf("writing the rest of the line: %v", err)
	}
	if w.buf.String() != "next" {
		t.Errorf("the buffer holds %q, want %q", w.buf.String(), "next")
	}
	if dest.String() != "k3s | partial line\n" {
		t.Errorf("the destination holds %q, want %q", dest.String(), "k3s | partial line\n")
	}
}

func newDeathRegistry() *deathRegistry {
	return &deathRegistry{
		waiters:   map[int]chan unix.WaitStatus{},
		unclaimed: map[int]unix.WaitStatus{},
		expected:  map[int]bool{},
	}
}

func TestDeathRegistryStoresAStatusUntilAwaited(t *testing.T) {
	d := newDeathRegistry()
	d.expect(42)
	d.record(42, unix.WaitStatus(0))
	if got := d.await(42); got != unix.WaitStatus(0) {
		t.Errorf("await returned %v, want %v", got, unix.WaitStatus(0))
	}
	if len(d.unclaimed) != 0 || len(d.expected) != 0 {
		t.Error("the registry still holds the claimed death, want it empty")
	}
}

func TestDeathRegistryDropsTheStatusOfAnOrphan(t *testing.T) {
	d := newDeathRegistry()
	d.record(42, unix.WaitStatus(9))
	if len(d.unclaimed) != 0 {
		t.Errorf("the registry keeps %v for a process init did not start, want nothing", d.unclaimed)
	}
}

func TestDeathRegistryStartExpectsTheChild(t *testing.T) {
	d := newDeathRegistry()
	cmd := exec.Command("true")
	if err := d.start(cmd); err != nil {
		t.Fatalf("starting the child: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Wait() })
	if !d.expected[cmd.Process.Pid] {
		t.Errorf("the registry does not expect the death of child %d, want it to", cmd.Process.Pid)
	}
}

func TestDeathRegistryAWokenWaiterLeavesNoExpectation(t *testing.T) {
	d := newDeathRegistry()
	d.expect(42)
	got := make(chan unix.WaitStatus, 1)
	go func() { got <- d.await(42) }()
	for {
		d.mu.Lock()
		waiting := len(d.waiters) == 1
		d.mu.Unlock()
		if waiting {
			break
		}
	}
	d.record(42, unix.WaitStatus(0))
	<-got
	if len(d.expected) != 0 || len(d.unclaimed) != 0 {
		t.Errorf("after the death was delivered, expected=%v and unclaimed=%v, want both empty", d.expected, d.unclaimed)
	}
}

func TestDeathRegistryWakesAWaiter(t *testing.T) {
	d := newDeathRegistry()
	got := make(chan unix.WaitStatus, 1)
	go func() { got <- d.await(42) }()
	// await adds its waiter under the lock, so once the loop sees the
	// waiter, record finds it.
	for {
		d.mu.Lock()
		waiting := len(d.waiters) == 1
		d.mu.Unlock()
		if waiting {
			break
		}
	}
	d.record(42, unix.WaitStatus(9))
	if status := <-got; status != unix.WaitStatus(9) {
		t.Errorf("await returned %v, want %v", status, unix.WaitStatus(9))
	}
}

func TestStopK3sReturnsOnTheReapersStatus(t *testing.T) {
	// The status is already on died, so stopK3s must return without
	// waiting out the 30 seconds before SIGKILL.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	died := make(chan unix.WaitStatus, 1)
	died <- unix.WaitStatus(0)
	stopK3s(cmd.Process, died)
}

func TestDescribeExitForAStoppedProcess(t *testing.T) {
	// 0x7f is the kernel's value for a stopped process. It is neither
	// an exit nor a signal, so describeExit prints it as hex.
	if got := describeExit(unix.WaitStatus(0x7f)); got != "wait status 0x7f" {
		t.Errorf("the description is %q, want %q", got, "wait status 0x7f")
	}
}

// scriptedFetch returns each of outputs in turn, and then the last one
// on every later call, like a cluster that converges.
func scriptedFetch(outputs ...string) func(time.Duration) (string, bool) {
	i := 0
	return func(time.Duration) (string, bool) {
		out := outputs[min(i, len(outputs)-1)]
		i++
		return out, true
	}
}

func TestPollAndReportStopsWhenTheTableSettles(t *testing.T) {
	fetch := scriptedFetch(
		"node-1   NotReady   control-plane   1s   v1.33",
		"node-1   Ready      control-plane   9s   v1.33",
	)
	settled := pollAndReport(t.Context(), time.Millisecond, time.Second, "node", fetch, containsReady)
	if !settled {
		t.Error("pollAndReport returned false for a Ready node, want true")
	}
}

func TestPollAndReportGivesUpAtTheDeadline(t *testing.T) {
	fetch := scriptedFetch("node-1   NotReady   control-plane   1s   v1.33")
	settled := pollAndReport(t.Context(), time.Millisecond, 20*time.Millisecond, "node", fetch, containsReady)
	if settled {
		t.Error("pollAndReport returned true for a table that never settles, want false at the deadline")
	}
}

func TestPollAndReportSkipsFailedFetches(t *testing.T) {
	// A fetch fails until k3s serves its API.
	failures := 0
	fetch := func(time.Duration) (string, bool) {
		failures++
		if failures < 3 {
			return "", false
		}
		return "node-1   Ready", true
	}
	settled := pollAndReport(t.Context(), time.Millisecond, time.Second, "node", fetch, containsReady)
	if !settled || failures < 3 {
		t.Errorf("pollAndReport returned %v after %d fetches, want true after at least 3", settled, failures)
	}
}

func TestPollAndReportReturnsFalseWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	fetch := scriptedFetch("node-1   Ready")
	if pollAndReport(ctx, time.Millisecond, time.Second, "node", fetch, containsReady) {
		t.Error("pollAndReport returned true for a cancelled context, want false")
	}
}

func TestKubectlGetPassesTheRequestTimeout(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		args    []string
		want    []string
	}{
		{"nodes", 10 * time.Second, []string{"nodes"},
			[]string{"kubectl", "get", "nodes", "--no-headers", "--request-timeout=10s"}},
		{"pods", 2500 * time.Millisecond, []string{"pods", "-A"},
			[]string{"kubectl", "get", "pods", "-A", "--no-headers", "--request-timeout=2.5s"}},
		// kubectl reads zero as no timeout, so a spent budget still
		// passes a positive timeout.
		{"spent budget", 0, []string{"nodes"},
			[]string{"kubectl", "get", "nodes", "--no-headers", "--request-timeout=1ms"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := kubectlGet(c.timeout, c.args...); !slices.Equal(got, c.want) {
				t.Errorf("kubectlGet = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPollAndReportBoundsEachFetchByTheTimeLeft(t *testing.T) {
	var timeouts []time.Duration
	fetch := func(timeout time.Duration) (string, bool) {
		timeouts = append(timeouts, timeout)
		return "node-1   NotReady", true
	}
	pollAndReport(t.Context(), time.Millisecond, 20*time.Millisecond, "node", fetch, containsReady)
	if len(timeouts) == 0 {
		t.Fatal("pollAndReport made no fetch, want at least one")
	}
	if longest := slices.Max(timeouts); longest > 20*time.Millisecond {
		t.Errorf("the longest fetch timeout is %s, want at most 20ms", longest)
	}
	if shortest := slices.Min(timeouts); shortest <= 0 {
		t.Errorf("the shortest fetch timeout is %s, want more than 0", shortest)
	}
}

func TestPollAndReportCapsEachFetchAtTheCallTimeout(t *testing.T) {
	var got time.Duration
	fetch := func(timeout time.Duration) (string, bool) {
		got = timeout
		return "node-1   Ready", true
	}
	pollAndReport(t.Context(), time.Millisecond, time.Hour, "node", fetch, containsReady)
	if got != kubectlCallTimeout {
		t.Errorf("the fetch timeout is %s, want %s", got, kubectlCallTimeout)
	}
}

func TestPodsSettled(t *testing.T) {
	cases := []struct {
		name    string
		table   string
		settled bool
	}{
		{"all running", "kube-system   coredns-abc   1/1   Running   0   1m", true},
		{"completed jobs count", "kube-system   helm-install-xyz   0/1   Completed   0   1m", true},
		{"still creating", "kube-system   coredns-abc   0/1   ContainerCreating   0   2s", false},
		{"mixed", "a   p1   1/1   Running   0   1m\nb   p2   0/1   Pending   0   1s", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := podsSettled(c.table); got != c.settled {
				t.Errorf("podsSettled returned %v, want %v", got, c.settled)
			}
		})
	}
}

// reapForTest runs the reaper for one test, because `run` and
// `runWithin` read exit statuses only from the death registry. No test
// in this package runs in parallel, so the reaper collects only this
// test's children.
func reapForTest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_ = reap(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// awaitZombie waits until the child pid has exited and not yet been
// collected, which /proc shows as state Z.
func awaitZombie(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err == nil && strings.Contains(string(stat), ") Z ") {
			return
		}
	}
	t.Fatalf("child %d did not become a zombie within 5 seconds, want it to exit", pid)
}

func TestReapCollectsAChildThatExitedBeforeTheReaperStarted(t *testing.T) {
	// The child's SIGCHLD arrives before the reaper subscribes, so only
	// the reaper's first collection can collect it.
	cmd := exec.Command("true")
	if err := deaths.start(cmd); err != nil {
		t.Fatalf("starting the child: %v", err)
	}
	awaitZombie(t, cmd.Process.Pid)
	reapForTest(t)
	got := make(chan unix.WaitStatus, 1)
	go func() { got <- deaths.await(cmd.Process.Pid) }()
	select {
	case status := <-got:
		if !status.Exited() || status.ExitStatus() != 0 {
			t.Errorf("the child ended with %v, want exit status 0", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("within 5 seconds, the reaper did not collect a child that exited before the reaper started, want it collected")
	}
}

func TestRunWithinReturnsTheOutputOfACommandThatFinishes(t *testing.T) {
	reapForTest(t)
	out, ok := runWithin(10*time.Second, "echo", "node-1   Ready")
	if !ok || out != "node-1   Ready" {
		t.Errorf("runWithin returned %q, %v, want %q, true", out, ok, "node-1   Ready")
	}
}

func openFiles(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("reading /proc/self/fd: %v", err)
	}
	return len(entries)
}

// init runs until the machine stops, so a command that leaks a
// descriptor eventually exhausts them. The garbage collector is off
// because a finalizer would close an unreachable pipe and hide the
// leak.
func TestRunLeavesNoDescriptorOpen(t *testing.T) {
	reapForTest(t)
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	before := openFiles(t)

	for range 5 {
		run("echo", "iptables v1.8.11 (legacy)")
	}

	if after := openFiles(t); after != before {
		t.Errorf("the open descriptors went from %d to %d after five runs, want no change", before, after)
	}
}

func TestRunWithinKillsACommandThatOutlivesItsTimeout(t *testing.T) {
	reapForTest(t)
	started := time.Now()
	_, ok := runWithin(200*time.Millisecond, "sleep", "60")
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("runWithin returned after %s, want at most 5s with a 200ms timeout", elapsed)
	}
	if ok {
		t.Error("runWithin returned true for a command that the timeout killed, want false")
	}
}
