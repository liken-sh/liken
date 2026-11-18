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

// fakeCard returns a regular file to open in place of the card node.
// Every DRM ioctl on it fails, so a read that passes the gate fails at
// its first request to the card.
func fakeCard(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "card1")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("writing the fake card file: %v", err)
	}
	return path
}

// gateFixture is a cardGate on a fake card, with a chosen connection
// state and DROP_MASTER result. drops counts the DROP_MASTER calls.
type gateFixture struct {
	gate  *cardGate
	drops int
}

func newGateFixture(t *testing.T, serving bool, drop error) *gateFixture {
	t.Helper()
	fixture := &gateFixture{}
	fixture.gate = newCardGate(fakeCard(t), fakeProc(t, map[string]string{
		"1":  "/usr/bin/display-operator",
		"14": westonBinary,
	}))
	fixture.gate.live = func() bool { return serving }
	fixture.gate.dropMaster = func(int) error {
		fixture.drops++
		return drop
	}
	return fixture
}

var gateReads = []struct {
	name string
	read func(*cardGate) error
}{
	{"current modes", func(g *cardGate) error { _, err := g.currentModes(); return err }},
	{"connector modes", func(g *cardGate) error { _, err := g.connectorModes(); return err }},
}

// An open while no compositor has opened the card would take DRM
// master, and the compositor that opens the card next could never get
// it.
func TestTheGateOpensNothingWhileNoCompositorServes(t *testing.T) {
	for _, read := range gateReads {
		t.Run(read.name, func(t *testing.T) {
			fixture := newGateFixture(t, false, unix.EACCES)

			if err := read.read(fixture.gate); !errors.Is(err, errCompositorAbsent) {
				t.Errorf("the read returned %v, want %v", err, errCompositorAbsent)
			}
			if fixture.drops != 0 {
				t.Errorf("the gate opened the card %d times with no compositor serving", fixture.drops)
			}
		})
	}
}

// DROP_MASTER returns EACCES for a file that was never master, which
// is every open while the compositor is master. The read goes on to the
// card.
func TestTheGateReadsWhileACompositorServes(t *testing.T) {
	for _, read := range gateReads {
		t.Run(read.name, func(t *testing.T) {
			fixture := newGateFixture(t, true, unix.EACCES)

			err := read.read(fixture.gate)
			if err == nil || !strings.Contains(err.Error(), "counting the card's resources") {
				t.Errorf("the read returned %v, want the first request to the card to fail", err)
			}
			if fixture.drops != 1 {
				t.Errorf("the read dropped master %d times, want 1", fixture.drops)
			}
			if len(fixture.gate.masterless) != 0 {
				t.Error("a file that was never master reported a compositor with no master")
			}
		})
	}
}

// A drop that succeeds means the operator's file was master, so the
// running compositor is not. The read reports the compositor's pid and
// goes on.
func TestADropThatSucceedsReportsTheCompositor(t *testing.T) {
	for _, read := range gateReads {
		t.Run(read.name, func(t *testing.T) {
			fixture := newGateFixture(t, true, nil)

			err := read.read(fixture.gate)
			if err == nil || !strings.Contains(err.Error(), "counting the card's resources") {
				t.Errorf("the read returned %v, want it to go on to the card", err)
			}
			select {
			case pid := <-fixture.gate.masterless:
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
			fixture := newGateFixture(t, true, unix.EINVAL)

			err := read.read(fixture.gate)
			if err == nil || !strings.Contains(err.Error(), "invalid argument") {
				t.Errorf("the read returned %v, want it to include the errno", err)
			}
			if strings.Contains(err.Error(), "counting the card's resources") {
				t.Error("the read went on to the card after the drop failed")
			}
		})
	}
}

// A file that is not a card fails the DROP_MASTER ioctl with ENOTTY.
func TestTheDefaultDropMasterIsTheKernelIoctl(t *testing.T) {
	gate := newCardGate(fakeCard(t), t.TempDir())
	gate.live = func() bool { return true }

	_, err := gate.currentModes()
	if err == nil || !strings.Contains(err.Error(), "inappropriate ioctl for device") {
		t.Errorf("the read returned %v, want the kernel's ENOTTY", err)
	}
}

func TestAGateWithNoWatchOpensNothing(t *testing.T) {
	gate := newCardGate(fakeCard(t), t.TempDir())

	if _, err := gate.currentModes(); !errors.Is(err, errCompositorAbsent) {
		t.Errorf("the read returned %v, want %v", err, errCompositorAbsent)
	}
}

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
				restartRequests: restartRequests(t.TempDir()),
				metrics:         readings,
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

// masterlessReports returns a closed channel that holds the given
// reports, so restartMasterless returns once it has handled them.
func masterlessReports(pids ...int) chan int {
	reports := make(chan int, len(pids))
	for _, pid := range pids {
		reports <- pid
	}
	close(reports)
	return reports
}

// Weston closes the card before it closes its client connections, so a
// compositor that the operator already ended can be reported while it
// exits. The restart record has that compositor, so the report does
// not restart it or count a restart.
func TestACompositorTheOperatorEndedIsNotRestartedAgain(t *testing.T) {
	cases := []struct {
		name string
		end  func(*draPlugin) error
	}{
		{"a restart that repairs the canvases", (*draPlugin).restartCompositor},
		{"a hung compositor", (*draPlugin).killHungCompositor},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ended []int
			readings := newMetrics(componentName, "dev")
			plugin := &draPlugin{
				card:            "card1",
				compositors:     func() []int { return []int{14} },
				signal:          func(int, syscall.Signal) error { return nil },
				restartRequests: restartRequests(t.TempDir()),
				metrics:         readings,
			}
			if err := c.end(plugin); err != nil {
				t.Fatalf("ending the compositor: %v", err)
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

// startProcess starts a process to signal in place of a compositor.
func startProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	process := exec.Command("sleep", "60")
	if err := process.Start(); err != nil {
		t.Fatalf("starting the stand-in process: %v", err)
	}
	t.Cleanup(func() {
		_ = process.Process.Kill()
		_ = process.Wait()
	})
	return process
}

func TestEndingAMasterlessCompositorSendsItSIGTERM(t *testing.T) {
	process := startProcess(t)

	if err := signalProcess(process.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatalf("signaling the process: %v", err)
	}

	err := process.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Errorf("the process ended with %v, want SIGTERM", err)
	}
}

// A new process that the kernel gave the reaped pid to before the
// signal would receive the SIGTERM. The kernel assigns pids in rising
// order, so that reuse needs the pid space to wrap around first.
func TestEndingACompositorThatHasExitedSignalsNothing(t *testing.T) {
	process := startProcess(t)
	pid := process.Process.Pid
	_ = process.Process.Kill()
	_ = process.Wait()

	if err := signalProcess(pid, syscall.SIGTERM); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("signalProcess returned %v, want %v", err, os.ErrProcessDone)
	}
}
