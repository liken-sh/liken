package main

// Tests for the supervisor's plumbing: exit narration, output
// prefixing, and the reaper's registry. Starting and stopping k3s
// itself is QEMU territory.

import (
	"bytes"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

// Wait statuses use the kernel's packing: an exit code rides in the
// second byte, a terminating signal in the low seven bits.
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
	// The waiter parks first; the recorded death must find it. Await
	// registers under the lock, so looping until the waiter appears
	// is race-free.
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
	// channel the way the reaper would post it. A real k3s stop is
	// QEMU territory; what's pinned here is that a posted status ends
	// the wait without escalation.
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
	// 0x7f is the kernel's "stopped" packing: neither an exit nor a
	// termination signal, so the description falls back to raw hex.
	if got := describeExit(unix.WaitStatus(0x7f)); got != "wait status 0x7f" {
		t.Errorf("got %q", got)
	}
}
