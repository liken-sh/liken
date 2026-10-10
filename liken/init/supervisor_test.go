package main

// Tests for the supervisor's internal mechanics: exit narration,
// output prefixing, and the reaper's registry. Tests that start and
// stop k3s itself run separately, under QEMU.

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

// The runtime section is an opt-in. An unset section adds no variable,
// so an unset spec produces an empty environment, and k3s runs on Go's
// own defaults. Each set field contributes only its own variable: a
// memory limit gives GOMEMLIMIT alone, a collector pace gives GOGC
// alone, and a section that sets both gives both.
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
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// Wait statuses use the kernel's packed format: an exit code occupies
// the second byte, and a terminating signal occupies the low seven
// bits.
func TestDescribeExitForACleanExit(t *testing.T) {
	if got := describeExit(unix.WaitStatus(0)); got != "status 0" {
		t.Errorf("got %q", got)
	}
}

func TestDescribeExitForAFailure(t *testing.T) {
	if got := describeExit(unix.WaitStatus(1 << 8)); got != "status 1" {
		t.Errorf("got %q", got)
	}
}

func TestDescribeExitForASignal(t *testing.T) {
	if got := describeExit(unix.WaitStatus(unix.SIGKILL)); got != "signal killed" {
		t.Errorf("got %q", got)
	}
}

func TestContainsReadyMatchesTheWholeField(t *testing.T) {
	out := "NAME     STATUS   ROLES\nnode-1   Ready    control-plane"
	if !containsReady(out) {
		t.Error("a Ready node should match")
	}
}

func TestContainsReadyRejectsNotReady(t *testing.T) {
	out := "NAME     STATUS     ROLES\nnode-1   NotReady   control-plane"
	if containsReady(out) {
		t.Error("NotReady must not read as Ready")
	}
}

func TestLineWriterBuffersPartialLines(t *testing.T) {
	var dest bytes.Buffer
	w := &lineWriter{dest: &dest, prefix: "k3s | "}
	if n, err := w.Write([]byte("partial")); n != 7 || err != nil {
		t.Fatalf("got %d, %v", n, err)
	}
	if w.buf.String() != "partial" {
		t.Errorf("a partial line waits in the buffer: %q", w.buf.String())
	}
	if _, err := w.Write([]byte(" line\nnext")); err != nil {
		t.Fatal(err)
	}
	if w.buf.String() != "next" {
		t.Errorf("completed lines flush, the remainder waits: %q", w.buf.String())
	}
	if dest.String() != "k3s | partial line\n" {
		t.Errorf("the complete line lands on the destination: %q", dest.String())
	}
}

func newDeathRegistry() *deathRegistry {
	return &deathRegistry{
		waiters:   map[int]chan unix.WaitStatus{},
		unclaimed: map[int]unix.WaitStatus{},
		expected:  map[int]bool{},
	}
}

func TestDeathRegistryParksAnUnclaimedDeath(t *testing.T) {
	d := newDeathRegistry()
	d.expect(42)
	d.record(42, unix.WaitStatus(0))
	if got := d.await(42); got != unix.WaitStatus(0) {
		t.Errorf("got %v", got)
	}
	if len(d.unclaimed) != 0 || len(d.expected) != 0 {
		t.Error("a claimed death should leave the registry")
	}
}

func TestDeathRegistryDropsTheDeathOfAnAdoptedOrphan(t *testing.T) {
	// PID 1 adopts every orphan on the machine, such as a containerd
	// shim, and the reaper collects it. Nobody in init awaits it, so
	// a parked status would stay forever, and a later child that
	// reuses the pid would read it as its own death.
	d := newDeathRegistry()
	d.record(42, unix.WaitStatus(9))
	if len(d.unclaimed) != 0 {
		t.Errorf("the death of a process init did not start was parked: %v", d.unclaimed)
	}
}

func TestDeathRegistryStartExpectsTheChild(t *testing.T) {
	d := newDeathRegistry()
	cmd := exec.Command("true")
	if err := d.start(cmd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Wait() })
	if !d.expected[cmd.Process.Pid] {
		t.Errorf("the registry does not expect the death of child %d", cmd.Process.Pid)
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
		t.Errorf("the registry kept state for a delivered death: expected=%v unclaimed=%v", d.expected, d.unclaimed)
	}
}

func TestDeathRegistryWakesAWaiter(t *testing.T) {
	d := newDeathRegistry()
	got := make(chan unix.WaitStatus, 1)
	go func() { got <- d.await(42) }()
	// The waiter parks first; the recorded death must find the
	// waiter. await registers the waiter under the lock, so looping
	// until the waiter appears carries no race condition.
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
		t.Errorf("got %v", status)
	}
}

func TestStopK3sNarratesTheExitTheReaperReports(t *testing.T) {
	// stopK3s only signals and receives; the death arrives on the
	// channel the way the reaper would post it. A test of a real k3s
	// stop runs under QEMU. What this test checks is that a posted
	// status ends the wait, without escalation to SIGKILL.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	died := make(chan unix.WaitStatus, 1)
	died <- unix.WaitStatus(0)
	stopK3s(cmd.Process, died)
}

func TestDescribeExitForAStoppedProcess(t *testing.T) {
	// 0x7f is the kernel's packed value for a stopped process: it is
	// neither an exit nor a termination signal, so the description
	// falls back to raw hex.
	if got := describeExit(unix.WaitStatus(0x7f)); got != "wait status 0x7f" {
		t.Errorf("got %q", got)
	}
}

// scriptedFetch replays a sequence of kubectl tables, and repeats the
// last table forever. This simulates a cluster converging over time.
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
		t.Error("a Ready node settles the report")
	}
}

func TestPollAndReportGivesUpAtTheDeadline(t *testing.T) {
	fetch := scriptedFetch("node-1   NotReady   control-plane   1s   v1.33")
	settled := pollAndReport(t.Context(), time.Millisecond, 20*time.Millisecond, "node", fetch, containsReady)
	if settled {
		t.Error("a table that never settles must give up, not report success")
	}
}

func TestPollAndReportSkipsFailedFetches(t *testing.T) {
	// A fetch that fails, because k3s is not serving yet, produces
	// no lines and no verdict. The loop tries again.
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
		t.Errorf("failed fetches are retried, not fatal: settled=%v after %d fetches", settled, failures)
	}
}

func TestPollAndReportReturnsWhenThePlaneShutsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	fetch := scriptedFetch("node-1   Ready")
	if pollAndReport(ctx, time.Millisecond, time.Second, "node", fetch, containsReady) {
		t.Error("a cancelled plane means no verdict")
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
		// passes a positive one.
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

func TestPollAndReportBoundsEachFetchByThePatienceLeft(t *testing.T) {
	var timeouts []time.Duration
	fetch := func(timeout time.Duration) (string, bool) {
		timeouts = append(timeouts, timeout)
		return "node-1   NotReady", true
	}
	pollAndReport(t.Context(), time.Millisecond, 20*time.Millisecond, "node", fetch, containsReady)
	if len(timeouts) == 0 {
		t.Fatal("the loop made no fetch")
	}
	if longest := slices.Max(timeouts); longest > 20*time.Millisecond {
		t.Errorf("fetch timeout %s is longer than the 20ms patience", longest)
	}
	if shortest := slices.Min(timeouts); shortest <= 0 {
		t.Errorf("fetch timeout %s is not positive", shortest)
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
		t.Errorf("fetch timeout = %s, want %s", got, kubectlCallTimeout)
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
				t.Errorf("podsSettled = %v, want %v", got, c.settled)
			}
		})
	}
}

// reapForTest runs init's reaper for one test, because runWithin
// learns of its child's exit only through the death registry. No init
// test runs in parallel, so the reaper collects only this test's
// children.
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

// awaitZombie waits until a child has exited and waits to be
// collected, which /proc shows as state Z.
func awaitZombie(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err == nil && strings.Contains(string(stat), ") Z ") {
			return
		}
	}
	t.Fatalf("child %d never exited", pid)
}

func TestReapCollectsAChildThatExitedBeforeTheReaperStarted(t *testing.T) {
	// The child's SIGCHLD arrives before the reaper subscribes, so
	// only the reaper's first look at its children can collect it.
	cmd := exec.Command("true")
	if err := deaths.start(cmd); err != nil {
		t.Fatal(err)
	}
	awaitZombie(t, cmd.Process.Pid)
	reapForTest(t)
	got := make(chan unix.WaitStatus, 1)
	go func() { got <- deaths.await(cmd.Process.Pid) }()
	select {
	case status := <-got:
		if !status.Exited() || status.ExitStatus() != 0 {
			t.Errorf("status = %v, want a clean exit", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the reaper never collected a child that exited before it started")
	}
}

func TestRunWithinReturnsTheOutputOfACommandThatFinishes(t *testing.T) {
	reapForTest(t)
	out, ok := runWithin(10*time.Second, "echo", "node-1   Ready")
	if !ok || out != "node-1   Ready" {
		t.Errorf("runWithin = %q, %v; want the output and success", out, ok)
	}
}

// openFiles counts the descriptors this process holds open.
func openFiles(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// init is PID 1 and runs for the life of the machine, so a command
// it runs must leave no descriptor behind. The collector is off
// because the finalizer of an unreachable pipe would close it and
// hide the leak.
func TestRunLeavesNoDescriptorOpen(t *testing.T) {
	reapForTest(t)
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	before := openFiles(t)

	for range 5 {
		run("echo", "iptables v1.8.11 (legacy)")
	}

	if after := openFiles(t); after != before {
		t.Errorf("open descriptors went from %d to %d after five runs", before, after)
	}
}

func TestRunWithinKillsACommandThatOutlivesItsTimeout(t *testing.T) {
	reapForTest(t)
	started := time.Now()
	_, ok := runWithin(200*time.Millisecond, "sleep", "60")
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("runWithin returned after %s; the 200ms timeout did not end the command", elapsed)
	}
	if ok {
		t.Error("a killed command is not a success")
	}
}
