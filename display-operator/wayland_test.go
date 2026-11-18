package main

import (
	"context"
	"errors"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// The tests copy the opcodes from the protocol definition instead of
// using the operator's constants, so a wrong number in the operator
// fails them.
const (
	wlDisplaySync          uint16 = 0
	wlDisplayGetRegistry   uint16 = 1
	wlDisplayDeleteID      uint16 = 1
	wlRegistryBind         uint16 = 0
	wlRegistryGlobal       uint16 = 0
	wlRegistryGlobalRemove uint16 = 1
	wlCallbackDone         uint16 = 0
	wlOutputGeometry       uint16 = 0
	wlOutputMode           uint16 = 1
	wlOutputDone           uint16 = 2
	wlOutputScale          uint16 = 3
	wlOutputName           uint16 = 4
	wlOutputDescription    uint16 = 5
)

// westonBench is a fake compositor on a Unix socket. It handles
// get_registry, sync, and bind, and a test chooses which outputs exist
// when a connection starts, which are added and removed later, and
// when the connection ends.
type westonBench struct {
	t       *testing.T
	path    string
	version uint32
	// listener lets a test stop the compositor and leave its socket
	// file behind.
	listener *net.UnixListener

	mu      sync.Mutex
	outputs map[uint32]westonOutput
	arrived chan *compositorSession
}

// Each mode is flags, width, height, and a refresh in millihertz. Flag
// 1 marks the current mode and flag 2 the monitor's preferred mode. An
// output with no make and model gets the default test monitor.
type westonOutput struct {
	connector string
	vendor    string
	model     string
	modes     [][]uint32
}

// labWestonModes returns a current and preferred mode, and a second
// mode that the output offers but does not run.
func labWestonModes() [][]uint32 {
	return [][]uint32{{3, 3840, 1600, 59997}, {0, 1920, 1080, 60000}}
}

func newWestonBench(t *testing.T, connectors map[uint32]string) *westonBench {
	t.Helper()
	return westonBenchOn(t, filepath.Join(t.TempDir(), socketName), connectors)
}

// westonBenchOn starts the compositor on a socket path that the caller
// chooses.
func westonBenchOn(t *testing.T, path string, connectors map[uint32]string) *westonBench {
	t.Helper()
	outputs := map[uint32]westonOutput{}
	for global, connector := range connectors {
		outputs[global] = westonOutput{connector: connector, modes: labWestonModes()}
	}
	server := &westonBench{
		t:       t,
		path:    path,
		version: outputVersion,
		outputs: outputs,
		arrived: make(chan *compositorSession, 8),
	}
	listener := listenOnSocket(t, server.path)
	server.listener = listener
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			session := &compositorSession{server: server, wire: newWaylandClient(connection)}
			go session.serveLoop()
		}
	}()
	return server
}

// client returns the next connection once the compositor has sent
// the done event of that connection's first sync. The socket delivers
// messages in order, so an output that the test adds or removes after
// client returns reaches the watch after its baseline.
func (f *westonBench) client() *compositorSession {
	f.t.Helper()
	select {
	case session := <-f.arrived:
		return session
	case <-time.After(5 * time.Second):
		f.t.Fatal("no client connected to the compositor within 5 seconds")
		return nil
	}
}

func (f *westonBench) output(global uint32) westonOutput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.outputs[global]
}

func (f *westonBench) snapshot() map[uint32]westonOutput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.outputs)
}

// addLabOutput adds an output with labWestonModes.
func (f *westonBench) addLabOutput(session *compositorSession, global uint32, connector string) {
	f.addOutput(session, global, westonOutput{connector: connector, modes: labWestonModes()})
}

func (f *westonBench) addOutput(session *compositorSession, global uint32, output westonOutput) {
	f.mu.Lock()
	f.outputs[global] = output
	f.mu.Unlock()
	session.sendGlobal(global)
}

func (f *westonBench) removeOutput(session *compositorSession, global uint32) {
	f.mu.Lock()
	delete(f.outputs, global)
	f.mu.Unlock()
	var words waylandWords
	words.putUint(global)
	session.send(session.registryID(), wlRegistryGlobalRemove, words)
}

// changeModes sends a new batch of modes for an output that the client
// has bound, as weston does when the mode changes and the output
// stays.
func (f *westonBench) changeModes(session *compositorSession, global uint32, modes [][]uint32) {
	f.mu.Lock()
	output := f.outputs[global]
	output.modes = modes
	f.outputs[global] = output
	f.mu.Unlock()
	session.sendBatch(global, modes)
}

// end closes the connection, as a compositor restart does.
func (f *westonBench) end(session *compositorSession) {
	_ = session.wire.socket.Close()
}

// stop closes the listener, as when the compositor process exits. The
// socket file stays on disk.
func (f *westonBench) stop() {
	f.t.Helper()
	if err := f.listener.Close(); err != nil {
		f.t.Fatalf("closing the compositor socket: %v", err)
	}
}

type compositorSession struct {
	server *westonBench
	wire   *waylandClient

	mu       sync.Mutex
	registry uint32
	// bound maps each global to the object id the client bound it to.
	bound map[uint32]uint32
}

func (s *compositorSession) registryID() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registry
}

// send holds the lock so that messages from the test goroutine and the
// serving goroutine do not interleave.
func (s *compositorSession) send(object uint32, opcode uint16, words waylandWords) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.wire.request(object, opcode, words)
}

func (s *compositorSession) sendGlobal(global uint32) {
	var words waylandWords
	words.putUint(global)
	words.putText(outputInterface)
	words.putUint(s.server.version)
	s.send(s.registryID(), wlRegistryGlobal, words)
}

func (s *compositorSession) serveLoop() {
	replied := false
	for {
		message, err := s.wire.event()
		if err != nil {
			return
		}
		switch {
		case message.object == displayObject && message.opcode == wlDisplayGetRegistry:
			registry := message.fields.uint()
			s.mu.Lock()
			s.registry = registry
			s.mu.Unlock()
			for global := range s.server.snapshot() {
				s.sendGlobal(global)
			}
		case message.object == displayObject && message.opcode == wlDisplaySync:
			callback := message.fields.uint()
			var done waylandWords
			done.putUint(0)
			s.send(callback, wlCallbackDone, done)
			var deleted waylandWords
			deleted.putUint(callback)
			s.send(displayObject, wlDisplayDeleteID, deleted)
			if !replied {
				replied = true
				s.server.arrived <- s
			}
		case message.object == s.registryID() && message.opcode == wlRegistryBind:
			global := message.fields.uint()
			_ = message.fields.text()
			version := message.fields.uint()
			id := message.fields.uint()
			s.mu.Lock()
			if s.bound == nil {
				s.bound = map[uint32]uint32{}
			}
			s.bound[global] = id
			s.mu.Unlock()
			s.sendBindEvents(id, global, version)
		}
	}
}

// sendBindEvents sends the events of a bind in the order of weston's
// bind_output: geometry, scale, the modes, name, description, and
// done. The client must skip scale and description by their sizes.
func (s *compositorSession) sendBindEvents(id, global, version uint32) {
	output := s.server.output(global)

	vendor, model := output.vendor, output.model
	if vendor == "" && model == "" {
		vendor, model = "LGD", "LG ULTRAWIDE"
	}
	var geometry waylandWords
	for _, value := range []uint32{0, 0, 600, 340, 0} {
		geometry.putUint(value)
	}
	geometry.putText(vendor)
	geometry.putText(model)
	geometry.putUint(0)
	s.send(id, wlOutputGeometry, geometry)

	if version >= 2 {
		var scale waylandWords
		scale.putUint(1)
		s.send(id, wlOutputScale, scale)
	}
	s.sendModes(id, output.modes)
	if version >= 4 {
		var name waylandWords
		name.putText(output.connector)
		s.send(id, wlOutputName, name)
		var description waylandWords
		description.putText("LG ULTRAWIDE")
		s.send(id, wlOutputDescription, description)
	}
	if version >= 2 {
		s.send(id, wlOutputDone, waylandWords{})
	}
}

func (s *compositorSession) sendModes(id uint32, modes [][]uint32) {
	for _, mode := range modes {
		var words waylandWords
		for _, value := range mode {
			words.putUint(value)
		}
		s.send(id, wlOutputMode, words)
	}
}

func (s *compositorSession) sendBatch(global uint32, modes [][]uint32) {
	s.mu.Lock()
	id := s.bound[global]
	s.mu.Unlock()
	s.sendModes(id, modes)
	s.send(id, wlOutputDone, waylandWords{})
}

// watchBench runs an outputWatch and sends each outputsChanged call to
// reports.
type watchBench struct {
	t       *testing.T
	watch   *outputWatch
	reports chan bool
}

func newWatchBench(t *testing.T, server *westonBench) *watchBench {
	t.Helper()
	bench := &watchBench{t: t, reports: make(chan bool, 32)}
	bench.watch = newOutputWatch(server.path, func(recreated bool) { bench.reports <- recreated })
	bench.watch.retry = 10 * time.Millisecond
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go bench.watch.run(ctx)
	return bench
}

func (b *watchBench) report() bool {
	b.t.Helper()
	select {
	case recreated := <-b.reports:
		return recreated
	case <-time.After(5 * time.Second):
		b.t.Fatal("the watch reported no output change within 5 seconds")
		return false
	}
}

func (b *watchBench) reported(count int) []bool {
	b.t.Helper()
	var reports []bool
	for range count {
		reports = append(reports, b.report())
	}
	return reports
}

func (b *watchBench) expectNoReport() {
	b.t.Helper()
	select {
	case recreated := <-b.reports:
		b.t.Fatalf("the watch reported recreated=%v when no output changed, want no report", recreated)
	case <-time.After(100 * time.Millisecond):
	}
}

// awaitServed waits until the watch's modes equal want. The watch
// reports a baseline output at its bind, before the output's mode
// events, so a test has to wait for them.
func (b *watchBench) awaitServed(want map[string]string) {
	b.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var have map[string]string
	for time.Now().Before(deadline) {
		have = b.watch.served().modes
		if maps.Equal(have, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.t.Errorf("the compositor serves %v, want %v", have, want)
}

// A compositor that has just started sized every canvas for its
// outputs, so they need no restart.
func TestTheOutputsOfAFreshConnectionAreNotReportedAsRecreated(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1", 2: "DP-1"})
	bench := newWatchBench(t, server)
	server.client()

	for _, recreated := range bench.reported(2) {
		if recreated {
			t.Error("the baseline outputs of a new connection were reported as recreated, want recreated=false")
		}
	}
	bench.expectNoReport()
}

// When a monitor wakes at another mode, weston re-creates its output,
// and the canvases are sized for the old mode, so the compositor needs
// a restart.
func TestAnOutputThatComesBackAtAnotherModeIsReportedAsRecreated(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1", 2: "DP-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(2)
	// The watch compares the panel from the output's last done event,
	// so the test waits for it before it removes the output.
	bench.awaitServed(map[string]string{"HDMI-A-1": "3840x1600@60", "DP-1": "3840x1600@60"})

	server.removeOutput(session, 1)
	if bench.report() {
		t.Error("a removal with no added output was reported as recreated, want recreated=false")
	}
	server.addOutput(session, 3, westonOutput{
		connector: "HDMI-A-1",
		modes:     [][]uint32{{3, 1920, 1080, 60000}},
	})
	if !bench.report() {
		t.Error("an output back at another mode was reported as recreated=false, want true")
	}
	bench.expectNoReport()
}

// When an A/V receiver switches its input, the HDMI link renegotiates
// and weston re-creates the output with the same panel and mode. The
// canvases already have the right size, so no restart is needed.
func TestAnOutputThatComesBackAsTheSamePanelIsNotReportedAsRecreated(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1", 2: "DP-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(2)
	bench.awaitServed(map[string]string{"HDMI-A-1": "3840x1600@60", "DP-1": "3840x1600@60"})

	server.removeOutput(session, 1)
	if bench.report() {
		t.Error("a removal with no added output was reported as recreated, want recreated=false")
	}
	server.addLabOutput(session, 3, "HDMI-A-1")
	if bench.report() {
		t.Error("the same panel back at the same mode was reported as recreated, want recreated=false")
	}
	bench.expectNoReport()
}

// A different monitor on the connector needs a restart even at the
// same mode.
func TestAnOutputThatComesBackAsAnotherMonitorIsReportedAsRecreated(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(1)
	bench.awaitMode("HDMI-A-1", "3840x1600@60")

	server.removeOutput(session, 1)
	bench.report()
	server.addOutput(session, 2, westonOutput{
		connector: "HDMI-A-1",
		vendor:    "DEN",
		model:     "DENON AVR",
		modes:     labWestonModes(),
	})
	if !bench.report() {
		t.Error("another monitor on the connector was reported as recreated=false, want true")
	}
	bench.expectNoReport()
}

// Weston can add the new output before it removes the old one, while
// a page flip is pending. The watch matches the two by connector.
func TestAnOutputThatArrivesBeforeTheOldOneLeavesIsReportedAsRecreated(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(1)
	bench.awaitMode("HDMI-A-1", "3840x1600@60")

	server.addOutput(session, 2, westonOutput{
		connector: "HDMI-A-1",
		modes:     [][]uint32{{3, 1920, 1080, 60000}},
	})
	if bench.report() {
		t.Error("an output added before the old one left was reported as recreated, want recreated=false until the removal")
	}
	bench.awaitMode("HDMI-A-1", "1920x1080@60")
	server.removeOutput(session, 1)
	if !bench.report() {
		t.Error("the removal that completes the re-creation was reported as recreated=false, want true")
	}
}

// An output bound at version 1 sends no done event and no name. The
// watch reports it at the bind, and reports a removal and an addition
// as a re-creation without comparing panels.
func TestAnOutputWithNoDoneEventIsReportedWhenBound(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	server.version = 1
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(1)

	server.removeOutput(session, 1)
	if bench.report() {
		t.Error("a removal with no added output was reported as recreated, want recreated=false")
	}
	server.addLabOutput(session, 2, "HDMI-A-1")
	if !bench.report() {
		t.Error("an output that left and came back was reported as recreated=false on a version 1 output, want true")
	}
}

// A restart ends the connection, and the next connection starts a new
// baseline, so a removal before the restart and an addition after it
// are not a re-creation.
func TestACompositorThatRestartsStartsANewBaseline(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1", 2: "DP-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(2)

	server.removeOutput(session, 1)
	if bench.report() {
		t.Error("a removal with no added output was reported as recreated, want recreated=false")
	}
	server.end(session)

	next := server.client()
	if bench.report() {
		t.Error("the baseline output of a new connection was reported as recreated, want recreated=false")
	}
	server.addLabOutput(next, 3, "HDMI-A-1")
	if bench.report() {
		t.Error("an output added after a restart was reported as recreated, want recreated=false because the restart ended the earlier removal")
	}
}

func TestTheWatchRecordsModesByConnectorAndDropsThemWithTheOutput(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1", 2: "DP-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(2)
	bench.awaitServed(map[string]string{"HDMI-A-1": "3840x1600@60", "DP-1": "3840x1600@60"})

	server.removeOutput(session, 1)
	bench.report()
	bench.awaitServed(map[string]string{"DP-1": "3840x1600@60"})
}

// An output bound at version 3 sends no name event. The watch still
// reports a re-creation, and it records no modes, because modes are
// keyed by connector.
func TestAnOutputOfACompositorOlderThanTheNameEventHasNoConnectorName(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	server.version = 3
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(1)

	server.removeOutput(session, 1)
	bench.report()
	server.addLabOutput(session, 2, "HDMI-A-1")
	if !bench.report() {
		t.Error("an output that left and came back was reported as recreated=false on a version 3 output, want true")
	}
	if served := bench.watch.served().modes; len(served) != 0 {
		t.Errorf("the watch records modes %v for a version 3 output that sends no name event, want none", served)
	}
}

// readbackPlugin returns a plugin whose mode readback reads this
// watch. The fallback timer is an hour, so a readback that ends within
// the test ended on the watch's change signal.
func readbackPlugin(watch *outputWatch) *draPlugin {
	return &draPlugin{
		served:         watch.served,
		switchTimeout:  500 * time.Millisecond,
		switchFallback: time.Hour,
	}
}

// timedReadback runs the readback with the operator's timeouts in a
// synctest bubble, and returns its error and how long it waited on the
// fake clock. The watch reads a real socket, which cannot run in a
// bubble, so the readback reads a snapshot of the watch with a change
// channel made inside the bubble. Call it only when the modes will not
// change.
func timedReadback(t *testing.T, watch *outputWatch, connector, mode string, before uint64) (err error, waited time.Duration) {
	t.Helper()
	served := watch.served()
	synctest.Test(t, func(t *testing.T) {
		served.changed = make(chan struct{})
		plugin := &draPlugin{
			served:         func() servedOutputs { return served },
			switchTimeout:  modeSwitchTimeout,
			switchFallback: modeSwitchFallback,
		}
		start := time.Now()
		err = plugin.awaitMode(t.Context(), connector, mode, before)
		waited = time.Since(start)
	})
	return err, waited
}

func (b *watchBench) awaitMode(connector, want string) {
	b.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	have := ""
	for time.Now().Before(deadline) {
		have = b.watch.served().modes[connector]
		if have == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.t.Fatalf("the compositor serves %q on %s, want %q", have, connector, want)
}

func TestTheReadbackAcceptsTheModeTheCompositorServes(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	server.client()

	if err := readbackPlugin(bench.watch).awaitMode(t.Context(), "HDMI-A-1", "3840x1600@60", 0); err != nil {
		t.Errorf("awaitMode(3840x1600@60) = %v, want nil because the compositor serves that mode", err)
	}
}

func TestTheReadbackFailsAndNamesTheModeTheCompositorServes(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	server.client()
	bench.awaitMode("HDMI-A-1", "3840x1600@60")

	err, waited := timedReadback(t, bench.watch, "HDMI-A-1", "1920x1080@60", 0)
	if err == nil {
		t.Fatal("awaitMode(1920x1080@60) returned nil, want an error because the compositor serves 3840x1600@60")
	}
	if waited != modeSwitchTimeout {
		t.Errorf("awaitMode gave up after %v, want %v", waited, modeSwitchTimeout)
	}
	for _, want := range []string{"HDMI-A-1", "1920x1080@60", "3840x1600@60"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want it to contain %q", err, want)
		}
	}
}

// Weston can report the final mode in a later batch, while the
// monitor settles.
func TestTheReadbackAcceptsAModeFromALaterBatch(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.awaitMode("HDMI-A-1", "3840x1600@60")

	server.changeModes(session, 1, [][]uint32{{1, 1920, 1080, 60000}, {2, 3840, 1600, 59997}})
	if err := readbackPlugin(bench.watch).awaitMode(t.Context(), "HDMI-A-1", "1920x1080@60", 0); err != nil {
		t.Errorf("awaitMode(1920x1080@60) = %v, want nil because a later batch of modes lists it", err)
	}
}

// The timeout and the fallback timer are an hour, so only the watch's
// change signal can end the wait.
func TestTheReadbackEndsOnTheWatchsReport(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.awaitMode("HDMI-A-1", "3840x1600@60")
	plugin := readbackPlugin(bench.watch)
	plugin.switchTimeout = time.Hour
	result := make(chan error, 1)
	go func() { result <- plugin.awaitMode(t.Context(), "HDMI-A-1", "1920x1080@60", 0) }()
	time.Sleep(50 * time.Millisecond)

	server.changeModes(session, 1, [][]uint32{{1, 1920, 1080, 60000}, {2, 3840, 1600, 59997}})

	select {
	case err := <-result:
		if err != nil {
			t.Errorf("awaitMode(1920x1080@60) = %v, want nil because the watch reported that mode", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("awaitMode did not return within 5 seconds of the watch's report")
	}
}

// The modes of the compositor that the switch restarted do not count,
// so the readback waits for the next connection. When none comes, the
// error is not errModeDeclined, because no compositor refused the
// mode.
func TestTheReadbackIgnoresTheModesOfTheCompositorItRestarted(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	server.client()
	bench.awaitMode("HDMI-A-1", "3840x1600@60")

	standing := bench.watch.served().session
	err, waited := timedReadback(t, bench.watch, "HDMI-A-1", "3840x1600@60", standing)
	if err == nil {
		t.Fatal("awaitMode returned nil, want an error because only the ended compositor served the mode")
	}
	if errors.Is(err, errModeDeclined) {
		t.Errorf("awaitMode returned %v, want an error other than errModeDeclined", err)
	}
	if waited != compositorReturnLimit {
		t.Errorf("awaitMode gave up after %v, want %v", waited, compositorReturnLimit)
	}
}

func TestTheModesAreClearedWhenTheConnectionEnds(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.awaitMode("HDMI-A-1", "3840x1600@60")

	_ = os.Remove(bench.watch.socketPath)
	server.end(session)
	bench.awaitMode("HDMI-A-1", "")
}

func TestWestonModeRoundsTheRefreshToWholeHertz(t *testing.T) {
	for _, one := range []struct {
		width, height, refresh uint32
		want                   string
	}{
		{3840, 1600, 59997, "3840x1600@60"},
		{1920, 1080, 60000, "1920x1080@60"},
		{1920, 1080, 23976, "1920x1080@24"},
		{1280, 720, 0, "1280x720"},
		{0, 0, 60000, ""},
	} {
		if got := westonMode(one.width, one.height, one.refresh); got != one.want {
			t.Errorf("%dx%d at %d mHz reads as %q, want %q", one.width, one.height, one.refresh, got, one.want)
		}
	}
}

func TestAWaylandStringIsPaddedToAWholeNumberOfWords(t *testing.T) {
	var words waylandWords
	words.putUint(7)
	words.putText("HDMI-A-1")
	words.putUint(4)
	if len(words.body)%4 != 0 {
		t.Fatalf("the arguments are %d bytes, which is not a whole number of words", len(words.body))
	}

	fields := waylandFields{body: words.body}
	if got := fields.uint(); got != 7 {
		t.Errorf("the first argument reads %d, want 7", got)
	}
	if got := fields.text(); got != "HDMI-A-1" {
		t.Errorf("the string reads %q, want %q", got, "HDMI-A-1")
	}
	if got := fields.uint(); got != 4 {
		t.Errorf("the argument after the string reads %d, want 4", got)
	}
	if fields.err != nil {
		t.Errorf("reading the arguments back: %v", fields.err)
	}
}

// A header with a size above maxWaylandMessage fails the read instead
// of making the client read that many bytes.
func TestAMessageWithAnImpossibleSizeEndsTheRead(t *testing.T) {
	ours, theirs := net.Pipe()
	t.Cleanup(func() { _ = ours.Close() })
	go func() {
		defer func() { _ = theirs.Close() }()
		_, _ = theirs.Write([]byte{1, 0, 0, 0, 0, 0, 0xff, 0xff})
	}()

	if _, err := newWaylandClient(ours).event(); err == nil {
		t.Error("event() accepted a header of 65535 bytes, want an error")
	}
}

func TestAConnectionOpensTheCardGateAndRunsTheConnectedHook(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	watch := newOutputWatch(server.path, func(bool) {})
	watch.retry = 10 * time.Millisecond
	woken := make(chan bool, 8)
	watch.connected = func() { woken <- watch.serving() }
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	if watch.serving() {
		t.Fatal("watch.serving() is true before the first dial, want false")
	}
	go watch.run(ctx)
	session := server.client()

	select {
	case serving := <-woken:
		if !serving {
			t.Error("watch.serving() is false when the connected hook runs, want true")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the connected hook did not run within 5 seconds")
	}

	_ = os.Remove(watch.socketPath)
	server.end(session)
	deadline := time.Now().Add(5 * time.Second)
	for watch.serving() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if watch.serving() {
		t.Error("watch.serving() is still true 5 seconds after the compositor ended, want false")
	}
}
