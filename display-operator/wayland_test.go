package main

import (
	"context"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The opcodes are spelled again here, from the protocol
// definition, rather than read from the operator's own constants. A
// number that is wrong in one place has to be wrong in both before a
// test passes.
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

// A compositor on a socket, serving the wire protocol the
// operator reads from weston. It answers get_registry, sync, and
// bind, and the test drives the globals: what the connection starts
// with, what leaves, what arrives, and when the compositor ends.
type westonBench struct {
	t       *testing.T
	path    string
	version uint32

	mu      sync.Mutex
	outputs map[uint32]westonOutput
	arrived chan *compositorSession
}

// One output of the fake compositor: the connector it names,
// and the modes it states, each one flags, width, height, and a
// refresh in millihertz. The mode whose flags carry 1 is the mode it
// serves.
type westonOutput struct {
	connector string
	modes     [][]uint32
}

// What a served output states: the mode it runs, which its
// monitor also prefers, then a mode it offers and does not run.
func labWestonModes() [][]uint32 {
	return [][]uint32{{3, 3840, 1600, 59997}, {0, 1920, 1080, 60000}}
}

func newWestonBench(t *testing.T, connectors map[uint32]string) *westonBench {
	t.Helper()
	outputs := map[uint32]westonOutput{}
	for global, connector := range connectors {
		outputs[global] = westonOutput{connector: connector, modes: labWestonModes()}
	}
	server := &westonBench{
		t:       t,
		path:    filepath.Join(t.TempDir(), socketName),
		version: outputVersion,
		outputs: outputs,
		arrived: make(chan *compositorSession, 8),
	}
	listener := listenOnSocket(t, server.path)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			session := &compositorSession{server: server, wire: newWaylandClient(connection)}
			go session.serve()
			server.arrived <- session
		}
	}()
	return server
}

// The connection this compositor serves now. A test waits for
// one before it moves any output, and waits again after a
// restart.
func (f *westonBench) client() *compositorSession {
	f.t.Helper()
	select {
	case session := <-f.arrived:
		return session
	case <-time.After(5 * time.Second):
		f.t.Fatal("no client connected to the compositor")
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

// An output arrives on the live connection.
func (f *westonBench) add(session *compositorSession, global uint32, connector string) {
	f.serve(session, global, westonOutput{connector: connector, modes: labWestonModes()})
}

func (f *westonBench) serve(session *compositorSession, global uint32, output westonOutput) {
	f.mu.Lock()
	f.outputs[global] = output
	f.mu.Unlock()
	session.announce(global, output.connector)
}

// An output leaves the live connection.
func (f *westonBench) remove(session *compositorSession, global uint32) {
	f.mu.Lock()
	delete(f.outputs, global)
	f.mu.Unlock()
	var words waylandWords
	words.putUint(global)
	session.send(session.registryID(), wlRegistryGlobalRemove, words)
}

// An output that is already bound states a new set of modes,
// in a batch of its own, the way weston does when a monitor's mode
// changes under an output that survives.
func (f *westonBench) restate(session *compositorSession, global uint32, modes [][]uint32) {
	f.mu.Lock()
	output := f.outputs[global]
	output.modes = modes
	f.outputs[global] = output
	f.mu.Unlock()
	session.states(global, modes)
}

// The compositor ends, which is what every restart looks like
// from the operator's end of the socket.
func (f *westonBench) end(session *compositorSession) {
	_ = session.wire.socket.Close()
}

type compositorSession struct {
	server *westonBench
	wire   *waylandClient

	mu       sync.Mutex
	registry uint32
	// The object the client bound each global to, which is
	// where the events about that output go.
	bound map[uint32]uint32
}

func (s *compositorSession) registryID() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registry
}

// One message out. The test's goroutine and the serving
// goroutine both write, and the lock keeps two messages from
// interleaving.
func (s *compositorSession) send(object uint32, opcode uint16, words waylandWords) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.wire.request(object, opcode, words)
}

func (s *compositorSession) announce(global uint32, connector string) {
	var words waylandWords
	words.putUint(global)
	words.putText(outputInterface)
	words.putUint(s.server.version)
	s.send(s.registryID(), wlRegistryGlobal, words)
}

func (s *compositorSession) serve() {
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
			for global, output := range s.server.snapshot() {
				s.announce(global, output.connector)
			}
		case message.object == displayObject && message.opcode == wlDisplaySync:
			callback := message.fields.uint()
			var done waylandWords
			done.putUint(0)
			s.send(callback, wlCallbackDone, done)
			var deleted waylandWords
			deleted.putUint(callback)
			s.send(displayObject, wlDisplayDeleteID, deleted)
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
			s.burst(id, global, version)
		}
	}
}

// Everything a compositor states about an output when a
// client binds it, in the order weston's bind_output sends it:
// geometry, scale, one mode for every mode the head offers, name,
// description, done. The name sits in the middle of that burst, so a
// client that reads it has to skip the events on both sides of it by
// their own sizes.
func (s *compositorSession) burst(id, global, version uint32) {
	output := s.server.output(global)

	var geometry waylandWords
	for _, value := range []uint32{0, 0, 600, 340, 0} {
		geometry.putUint(value)
	}
	geometry.putText("LGD")
	geometry.putText("LG ULTRAWIDE")
	geometry.putUint(0)
	s.send(id, wlOutputGeometry, geometry)

	if version >= 2 {
		var scale waylandWords
		scale.putUint(1)
		s.send(id, wlOutputScale, scale)
	}
	s.modes(id, output.modes)
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

// The mode flags are the protocol's own bitfield: 1 is the
// mode the output runs and 2 is the one the monitor prefers, and the
// refresh is in millihertz.
func (s *compositorSession) modes(id uint32, modes [][]uint32) {
	for _, mode := range modes {
		var words waylandWords
		for _, value := range mode {
			words.putUint(value)
		}
		s.send(id, wlOutputMode, words)
	}
}

// A batch of its own for an output the client already bound,
// closed by the done event that makes it the output's answer.
func (s *compositorSession) states(global uint32, modes [][]uint32) {
	s.mu.Lock()
	id := s.bound[global]
	s.mu.Unlock()
	s.modes(id, modes)
	s.send(id, wlOutputDone, waylandWords{})
}

// The watch under test, with the reports it makes on a
// channel.
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

// The next report the watch makes, and a failure when it
// makes none.
func (b *watchBench) report() bool {
	b.t.Helper()
	select {
	case recreated := <-b.reports:
		return recreated
	case <-time.After(5 * time.Second):
		b.t.Fatal("the watch reported nothing about the compositor's outputs")
		return false
	}
}

// The reports of one burst, in order.
func (b *watchBench) reported(count int) []bool {
	b.t.Helper()
	var reports []bool
	for range count {
		reports = append(reports, b.report())
	}
	return reports
}

func (b *watchBench) quiet() {
	b.t.Helper()
	select {
	case recreated := <-b.reports:
		b.t.Fatalf("the watch reported recreated=%v with nothing moving", recreated)
	case <-time.After(100 * time.Millisecond):
	}
}

// What the watch has recorded for each connector, once it
// settles on what the test expects. An output's own events arrive
// after the global that raised the report, and the wait covers that
// gap.
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

// A compositor that just started has laid every canvas out at
// its own output's size, so the outputs it announces on a fresh
// connection owe nothing.
func TestTheOutputsOfAFreshConnectionOweNoHeal(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1", 2: "DP-1"})
	bench := newWatchBench(t, server)
	server.client()

	for _, recreated := range bench.reported(2) {
		if recreated {
			t.Error("the outputs a connection starts with owe a heal")
		}
	}
	bench.quiet()
}

// The case a comparison of monitor identities missed. The
// same panel sleeps and wakes, the kernel mode on its connector
// changes, and weston destroys and re-creates the output under the
// same monitor.
func TestAnOutputThatIsReCreatedOwesAHeal(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1", 2: "DP-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(2)

	server.remove(session, 1)
	if bench.report() {
		t.Error("a removal with no output back yet owes a heal")
	}
	server.add(session, 3, "HDMI-A-1")
	if !bench.report() {
		t.Error("an output that left and came back owes no heal")
	}
	bench.quiet()
}

// An output that arrives before the one it replaces leaves is
// the same re-creation, because weston defers a destruction across a
// pending flip.
func TestAnOutputThatArrivesBeforeTheOldOneLeavesOwesAHeal(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(1)

	server.add(session, 2, "HDMI-A-1")
	if bench.report() {
		t.Error("an output that arrived alone owes a heal")
	}
	server.remove(session, 1)
	if !bench.report() {
		t.Error("the removal that completes the re-creation owes no heal")
	}
}

// The operator restarts the compositor itself, for a mode and
// for a heal. Every restart ends the connection, and the connection
// that follows starts from a compositor whose canvases are right, so
// the operator's own restarts report nothing.
func TestACompositorThatRestartsStartsANewBaseline(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1", 2: "DP-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(2)

	server.remove(session, 1)
	if bench.report() {
		t.Error("a removal with no output back yet owes a heal")
	}
	server.end(session)

	next := server.client()
	if bench.report() {
		t.Error("the output a new connection starts with owes a heal")
	}
	server.add(next, 3, "HDMI-A-1")
	if bench.report() {
		t.Error("an output that arrived after a restart pairs with a removal the restart ended")
	}
}

// The name event carries the connector's own name, which keys
// everything the watch records about that output. An output that
// leaves takes its connector's answer with it.
func TestTheWatchNamesEachOutputsConnector(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1", 2: "DP-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(2)
	bench.awaitServed(map[string]string{"HDMI-A-1": "3840x1600@60", "DP-1": "3840x1600@60"})

	server.remove(session, 1)
	bench.report()
	bench.awaitServed(map[string]string{"DP-1": "3840x1600@60"})
}

// A compositor older than the name event is bound at what it
// offers, and its outputs are tracked with no name. The heal
// decision reads no name, so a re-creation still owes a restart.
// Nothing keys a mode to a connector, so such a compositor answers
// no mode readback.
func TestAnOlderCompositorsOutputsAreTrackedWithNoName(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	server.version = 3
	bench := newWatchBench(t, server)
	session := server.client()
	bench.reported(1)

	server.remove(session, 1)
	bench.report()
	server.add(session, 2, "HDMI-A-1")
	if !bench.report() {
		t.Error("an output that left and came back owes no heal on an older compositor")
	}
	if served := bench.watch.served().modes; len(served) != 0 {
		t.Errorf("the watch records %v on a compositor that sends no name event", served)
	}
}

// The plugin's mode readback, reading the compositor this
// bench serves. The budget is short so a test that must reach the
// timeout reaches it at once, the way the plugin's other tests
// shorten it.
func readbackPlugin(watch *outputWatch) *draPlugin {
	return &draPlugin{
		served:         watch.served,
		switchTimeout:  500 * time.Millisecond,
		switchInterval: 2 * time.Millisecond,
	}
}

// The mode the watch has recorded for one connector, once it
// settles on what the test expects.
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

// The readback ends when the compositor that started after
// the restart reports the mode the claim stated.
func TestTheReadbackTakesTheModeTheCompositorServes(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	server.client()

	if err := readbackPlugin(bench.watch).awaitMode(t.Context(), "HDMI-A-1", "3840x1600@60", 0); err != nil {
		t.Errorf("the readback refused the mode the compositor serves: %v", err)
	}
}

// A mode the compositor never takes fails the prepare, and
// the failure names the mode it serves instead.
func TestTheReadbackFailsWithTheModeTheCompositorServesInstead(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	server.client()
	bench.awaitMode("HDMI-A-1", "3840x1600@60")

	err := readbackPlugin(bench.watch).awaitMode(t.Context(), "HDMI-A-1", "1920x1080@60", 0)
	if err == nil {
		t.Fatal("the readback took a mode the compositor does not serve")
	}
	for _, want := range []string{"HDMI-A-1", "1920x1080@60", "3840x1600@60"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %q does not name %q", err, want)
		}
	}
}

// A monitor answers slowly, so weston states the mode it
// settles on in a later batch. The readback takes the last batch,
// not the first.
func TestTheReadbackTakesAModeStatedInALaterBatch(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.awaitMode("HDMI-A-1", "3840x1600@60")

	server.restate(session, 1, [][]uint32{{1, 1920, 1080, 60000}, {2, 3840, 1600, 59997}})
	if err := readbackPlugin(bench.watch).awaitMode(t.Context(), "HDMI-A-1", "1920x1080@60", 0); err != nil {
		t.Errorf("the readback refused the mode a later batch stated: %v", err)
	}
}

// The compositor a switch ended still serves the mode the
// claim replaced, and its answer is not the readback. The wait is
// for the connection that follows the restart.
func TestTheReadbackTakesNoAnswerFromTheCompositorItEnded(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	server.client()
	bench.awaitMode("HDMI-A-1", "3840x1600@60")

	standing := bench.watch.served().session
	err := readbackPlugin(bench.watch).awaitMode(t.Context(), "HDMI-A-1", "3840x1600@60", standing)
	if err == nil {
		t.Fatal("the readback took the answer of the compositor the switch ended")
	}
}

// A dead compositor serves nothing: its answers empty when
// the connection ends, not when the next one opens, so the window
// between two compositors reports no mode instead of the last
// answer of the one that died.
func TestTheModesOfACompositorDieWithIt(t *testing.T) {
	server := newWestonBench(t, map[uint32]string{1: "HDMI-A-1"})
	bench := newWatchBench(t, server)
	session := server.client()
	bench.awaitMode("HDMI-A-1", "3840x1600@60")

	_ = os.Remove(bench.watch.socketPath)
	server.end(session)
	bench.awaitMode("HDMI-A-1", "")
}

// Weston states a refresh in millihertz, and everything else
// here states it in whole hertz.
func TestAWestonModeReadsAsTheModeAClaimStates(t *testing.T) {
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

// A string on the wire is a length that counts the null
// terminator, then the bytes, then the padding to a whole number of
// words.
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

// A message that states a size no message can have is a
// connection to end, not a read to make.
func TestAMessageWithAnImpossibleSizeEndsTheRead(t *testing.T) {
	ours, theirs := net.Pipe()
	t.Cleanup(func() { _ = ours.Close() })
	go func() {
		defer func() { _ = theirs.Close() }()
		_, _ = theirs.Write([]byte{1, 0, 0, 0, 0, 0, 0xff, 0xff})
	}()

	if _, err := newWaylandClient(ours).event(); err == nil {
		t.Error("a message of 65535 bytes was read as a message")
	}
}
