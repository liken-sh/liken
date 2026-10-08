package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"golang.org/x/sys/unix"
)

// A file that stands where the card node would. It opens read-write
// like the card, and every DRM ioctl on it fails, so a read that got
// past the gate fails on its first request to the card.
func cardStandIn(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "card1")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The gate on a stand-in card, with a compositor that serves or not,
// and a drop that answers what the test states. Drops counts the
// opens, because every open drops master before anything else.
type gateBench struct {
	gate  *cardGate
	drops int
}

func newGateBench(t *testing.T, serving bool, drop error) *gateBench {
	t.Helper()
	bench := &gateBench{}
	bench.gate = newCardGate(cardStandIn(t), fakeProc(t, map[string]string{
		"1":  "/usr/bin/display-operator",
		"14": westonBinary,
	}))
	bench.gate.live = func() bool { return serving }
	bench.gate.dropMaster = func(int) error {
		bench.drops++
		return drop
	}
	return bench
}

// The two reads the gate serves, so every test covers both.
var gateReads = []struct {
	name string
	read func(*cardGate) error
}{
	{"current modes", func(g *cardGate) error { _, err := g.currentModes(); return err }},
	{"connector modes", func(g *cardGate) error { _, err := g.connectorModes(); return err }},
}

// An open while no compositor has opened the card would take DRM master,
// and the compositor that opens the card next could never get it.
func TestTheGateOpensNothingWhileNoCompositorServes(t *testing.T) {
	for _, read := range gateReads {
		t.Run(read.name, func(t *testing.T) {
			bench := newGateBench(t, false, unix.EACCES)

			if err := read.read(bench.gate); !errors.Is(err, errCompositorAbsent) {
				t.Errorf("error = %v, want %v", err, errCompositorAbsent)
			}
			if bench.drops != 0 {
				t.Errorf("the gate opened the card %d times with no compositor serving", bench.drops)
			}
		})
	}
}

// EACCES is the answer for a file that was never master, which is
// every open while the compositor holds master. The read goes on to
// the card, and the stand-in refuses the first request.
func TestTheGateReadsWhileACompositorServes(t *testing.T) {
	for _, read := range gateReads {
		t.Run(read.name, func(t *testing.T) {
			bench := newGateBench(t, true, unix.EACCES)

			err := read.read(bench.gate)
			if err == nil || !strings.Contains(err.Error(), "counting the card's resources") {
				t.Errorf("error = %v, want the first request to the card to fail", err)
			}
			if bench.drops != 1 {
				t.Errorf("the read dropped master %d times, want 1", bench.drops)
			}
			if len(bench.gate.masterless) != 0 {
				t.Error("a file that was never master reported a compositor with no master")
			}
		})
	}
}

// A drop that succeeds means the operator's file was master, so the
// compositor that serves holds none. The read reports the
// compositor's pid and goes on.
func TestADropThatSucceedsReportsTheCompositor(t *testing.T) {
	for _, read := range gateReads {
		t.Run(read.name, func(t *testing.T) {
			bench := newGateBench(t, true, nil)

			err := read.read(bench.gate)
			if err == nil || !strings.Contains(err.Error(), "counting the card's resources") {
				t.Errorf("error = %v, want the read to go on to the card", err)
			}
			select {
			case pid := <-bench.gate.masterless:
				if pid != 14 {
					t.Errorf("the report names pid %d, want the compositor's 14", pid)
				}
			default:
				t.Error("a drop that succeeded reported nothing")
			}
		})
	}
}

// A drop that fails another way leaves the file possibly master, so
// the read stops before any request that a master's file would make.
func TestADropThatFailsOtherwiseFailsTheRead(t *testing.T) {
	for _, read := range gateReads {
		t.Run(read.name, func(t *testing.T) {
			bench := newGateBench(t, true, unix.EINVAL)

			err := read.read(bench.gate)
			if err == nil || !strings.Contains(err.Error(), "invalid argument") {
				t.Errorf("error = %v, want it to carry the errno", err)
			}
			if strings.Contains(err.Error(), "counting the card's resources") {
				t.Error("the read went on to the card after the drop failed")
			}
		})
	}
}

// The gate's own drop is the kernel's DROP_MASTER. A file that is no
// card answers it with ENOTTY, which is neither of the two answers
// a card gives, so the read fails and names the errno.
func TestTheGatesDropIsARealIoctl(t *testing.T) {
	gate := newCardGate(cardStandIn(t), t.TempDir())
	gate.live = func() bool { return true }

	_, err := gate.currentModes()
	if err == nil || !strings.Contains(err.Error(), "inappropriate ioctl for device") {
		t.Errorf("error = %v, want the kernel's ENOTTY", err)
	}
}

// A gate that no watch is wired to opens nothing.
func TestAGateWithNoWatchOpensNothing(t *testing.T) {
	gate := newCardGate(cardStandIn(t), t.TempDir())

	if _, err := gate.currentModes(); !errors.Is(err, errCompositorAbsent) {
		t.Errorf("error = %v, want %v", err, errCompositorAbsent)
	}
}

// The report arrives once per compositor. A second report about the
// same compositor restarts nothing more, a compositor that has
// already exited is not restarted, and a new compositor gets its own
// restart. The restart ends the pid in the report and no other.
func TestACompositorWithNoMasterRestartsOnce(t *testing.T) {
	cases := []struct {
		name    string
		reports []int
		running []int
		gone    bool
		want    []int
	}{
		{"one report", []int{14}, []int{14}, false, []int{14}},
		{"two reports about one compositor", []int{14, 14}, []int{14}, false, []int{14}},
		{"a compositor that has exited", []int{14}, []int{29}, false, nil},
		{"two compositors", []int{14, 29}, []int{14, 29}, false, []int{14, 29}},
		{"a compositor that exits during the restart", []int{14}, []int{14}, true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ended []int
			readings := newMetrics(componentName, "dev")
			plugin := &draPlugin{
				card:        "card1",
				compositors: func() []int { return c.running },
				signal: func(pid int, _ syscall.Signal) error {
					if c.gone {
						return os.ErrProcessDone
					}
					ended = append(ended, pid)
					return nil
				},
				orders:  restartOrders(t.TempDir()),
				metrics: readings,
			}

			plugin.restartMasterless(context.Background(), masterlessReports(c.reports...))

			if !slices.Equal(ended, c.want) {
				t.Errorf("the restart ended pids %v, want %v", ended, c.want)
			}
			got := testutil.ToFloat64(readings.compositorRestarts.WithLabelValues("masterless"))
			if got != float64(len(c.want)) {
				t.Errorf(`display_compositor_restarts_total{reason="masterless"} = %v, want %d`, got, len(c.want))
			}
		})
	}
}

// The reports the card gate sends, closed so the restart returns once
// it has handled them.
func masterlessReports(pids ...int) chan int {
	reports := make(chan int, len(pids))
	for _, pid := range pids {
		reports <- pid
	}
	close(reports)
	return reports
}

// Weston closes the card before it closes its client connections, so a
// compositor that the operator ended for another reason can be reported
// on its way out. The operator already ended it, so the report restarts
// nothing and counts nothing.
func TestACompositorTheOperatorEndedIsNotRestartedAgain(t *testing.T) {
	cases := []struct {
		name string
		end  func(*draPlugin) error
	}{
		{"a heal", (*draPlugin).restartCompositor},
		{"a hung compositor", (*draPlugin).killHungCompositor},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ended []int
			readings := newMetrics(componentName, "dev")
			plugin := &draPlugin{
				card:        "card1",
				compositors: func() []int { return []int{14} },
				signal:      func(int, syscall.Signal) error { return nil },
				orders:      restartOrders(t.TempDir()),
				metrics:     readings,
			}
			if err := c.end(plugin); err != nil {
				t.Fatal(err)
			}
			plugin.signal = func(pid int, _ syscall.Signal) error {
				ended = append(ended, pid)
				return nil
			}

			plugin.restartMasterless(context.Background(), masterlessReports(14))

			if len(ended) != 0 {
				t.Errorf("the restart ended pids %v, want none", ended)
			}
			got := testutil.ToFloat64(readings.compositorRestarts.WithLabelValues("masterless"))
			if got != 0 {
				t.Errorf(`display_compositor_restarts_total{reason="masterless"} = %v, want 0`, got)
			}
		})
	}
}

// A process that stands in for a compositor: a real child that runs
// until a signal ends it.
func startProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	process := exec.Command("sleep", "60")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = process.Process.Kill()
		_ = process.Wait()
	})
	return process
}

// The restart ends the process that the report names with SIGTERM,
// the same signal a mode switch sends.
func TestEndingAMasterlessCompositorSendsItSIGTERM(t *testing.T) {
	process := startProcess(t)

	if err := signalProcess(process.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	err := process.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Errorf("the process ended with %v, want SIGTERM", err)
	}
}

// A process that has exited and been reaped answers os.ErrProcessDone.
// The kernel could give the reaped pid to a new process before the
// signal, and that process would take the SIGTERM. The kernel hands out
// pids in rising order, so a reuse this soon needs the pid space to
// wrap around, and the risk is small.
func TestEndingACompositorThatHasExitedSignalsNothing(t *testing.T) {
	process := startProcess(t)
	pid := process.Process.Pid
	_ = process.Process.Kill()
	_ = process.Wait()

	if err := signalProcess(pid, syscall.SIGTERM); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("error = %v, want %v", err, os.ErrProcessDone)
	}
}
