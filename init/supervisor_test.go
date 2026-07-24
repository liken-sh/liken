package main

// Tests for the supervisor's internal mechanics: exit narration,
// output prefixing, and the reaper's registry. Tests that start and
// stop k3s itself run separately, under QEMU.

import (
	"bytes"
	"context"
	"os/exec"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/cluster"
)

// The default discipline scales with the machine's memory and with
// what the cluster asks k3s to hold: a quarter of memory for the
// minimum viable control plane, and seven sixteenths once the helm
// feature brings the chart renderer and its CRDs into the process. An
// unset spec.runtime.k3s must produce exactly that, byte for byte.
func TestK3sRuntimeEnv(t *testing.T) {
	gib := uint64(1024 * 1024 * 1024)
	gogc := func(n int) *int { return &n }
	cases := map[string]struct {
		spec cluster.K3sRuntimeSpec
		mem  uint64
		helm bool
		want []string
	}{
		"absentLean":     {cluster.K3sRuntimeSpec{}, gib, false, []string{"GOMEMLIMIT=256MiB", "GOGC=50"}},
		"absentHelm":     {cluster.K3sRuntimeSpec{}, gib, true, []string{"GOMEMLIMIT=448MiB", "GOGC=50"}},
		"percent":        {cluster.K3sRuntimeSpec{GoMemoryLimit: "25%"}, gib, false, []string{"GOMEMLIMIT=256MiB", "GOGC=50"}},
		"percentIgnores": {cluster.K3sRuntimeSpec{GoMemoryLimit: "50%"}, gib, true, []string{"GOMEMLIMIT=512MiB", "GOGC=50"}},
		"absolute":       {cluster.K3sRuntimeSpec{GoMemoryLimit: "448Mi"}, gib, false, []string{"GOMEMLIMIT=448MiB", "GOGC=50"}},
		"off":            {cluster.K3sRuntimeSpec{GoMemoryLimit: "off"}, gib, true, []string{"GOGC=50"}},
		"customGoGC":     {cluster.K3sRuntimeSpec{GoGC: gogc(80)}, gib, false, []string{"GOMEMLIMIT=256MiB", "GOGC=80"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := k3sRuntimeEnv(tc.spec, tc.mem, tc.helm); !slices.Equal(got, tc.want) {
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

func TestDeathRegistryParksAnUnclaimedDeath(t *testing.T) {
	d := &deathRegistry{
		waiters:   map[int]chan unix.WaitStatus{},
		unclaimed: map[int]unix.WaitStatus{},
	}
	d.record(42, unix.WaitStatus(0))
	if got := d.await(42); got != unix.WaitStatus(0) {
		t.Errorf("got %v", got)
	}
	if len(d.unclaimed) != 0 {
		t.Error("a claimed death should leave the registry")
	}
}

func TestDeathRegistryWakesAWaiter(t *testing.T) {
	d := &deathRegistry{
		waiters:   map[int]chan unix.WaitStatus{},
		unclaimed: map[int]unix.WaitStatus{},
	}
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
	stopK3s(cmd.Process.Pid, died)
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
func scriptedFetch(outputs ...string) func() (string, bool) {
	i := 0
	return func() (string, bool) {
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
	fetch := func() (string, bool) {
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
