package main

// These tests drive the real link against a module on a real unix
// socket in a directory the test owns. What they cover is the
// contract with liken-layout.so: the handshake, the sequence numbers
// a request and its reply are matched by, the store the events build,
// and what a reconnect replays.

import (
	"bufio"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The lines the module sends are spelled again here, from the
// protocol, rather than built from the operator's own constants. A
// verb that is wrong in one place has to be wrong in both before a
// test passes.
const moduleHelloLine = "hello liken-layout 1"

// moduleScript is what one test's module answers with. It is stated
// before the listener starts, because the module answers on its own
// goroutine from the first connection on.
type moduleScript struct {
	// The first line it sends. A test that states another version
	// drives the mismatch, and an empty one is the version this
	// operator speaks.
	hello string
	// The lines it sends after its hello and before it accepts the
	// operator's, which is where the module reports the outputs it
	// has at connect time.
	greeting []string
	// The requests, by verb, that it refuses, and the reason it
	// gives.
	refuse map[string]string
	// A module that answers nothing at all, which is a compositor
	// whose event loop stopped before the operator connected.
	mute bool
	// A module that greets the operator and answers nothing after
	// that, which is the same compositor a moment later.
	muteAfterHello bool
}

// fakeModule is liken-layout.so's side of the control socket: a unix
// listener that greets each connection, answers every request it
// reads, and sends the events its script states.
type fakeModule struct {
	t        *testing.T
	path     string
	script   moduleScript
	listener *net.UnixListener

	mu          sync.Mutex
	requests    []string
	connections int
	live        net.Conn
	arrived     chan struct{}
}

func newFakeModule(t *testing.T, script moduleScript) *fakeModule {
	t.Helper()
	if script.hello == "" {
		script.hello = moduleHelloLine
	}
	module := &fakeModule{
		t:       t,
		path:    filepath.Join(t.TempDir(), "layout.sock"),
		script:  script,
		arrived: make(chan struct{}, 8),
	}
	module.listener = listenOnSocket(t, module.path)
	go module.serve()
	return module
}

func (m *fakeModule) serve() {
	for {
		connection, err := m.listener.Accept()
		if err != nil {
			return
		}
		go m.session(connection)
	}
}

// One connection: the hello, then one reply for every request, with
// the scripted greeting in the window the protocol allows for it.
func (m *fakeModule) session(connection net.Conn) {
	defer func() { _ = connection.Close() }()

	m.mu.Lock()
	m.connections++
	m.live = connection
	m.mu.Unlock()

	fmt.Fprintf(connection, "%s\n", m.script.hello)
	lines := bufio.NewScanner(connection)
	for lines.Scan() {
		sequence, request, _ := strings.Cut(lines.Text(), " ")
		verb, _, _ := strings.Cut(request, " ")
		isHello := verb == layoutHelloEvent

		m.mu.Lock()
		m.requests = append(m.requests, request)
		m.mu.Unlock()
		reason := m.script.refuse[verb]

		// The greeting goes out before the hello's own answer,
		// because the protocol lets an event arrive at any time after
		// the module's hello.
		if isHello {
			for _, event := range m.script.greeting {
				fmt.Fprintf(connection, "%s\n", event)
			}
		}
		switch {
		case m.script.mute, m.script.muteAfterHello && !isHello:
		case reason != "":
			fmt.Fprintf(connection, "error %s %s\n", sequence, reason)
		default:
			fmt.Fprintf(connection, "ok %s\n", sequence)
		}
		if isHello {
			m.arrived <- struct{}{}
		}
	}
}

// send puts one event on the connection the module serves now.
func (m *fakeModule) send(line string) {
	m.t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live == nil {
		m.t.Fatal("the module has no connection to send on")
	}
	fmt.Fprintf(m.live, "%s\n", line)
}

// drop ends the connection the module serves now, which is what a
// compositor restart does to it.
func (m *fakeModule) drop() {
	m.t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live == nil {
		m.t.Fatal("the module has no connection to drop")
	}
	_ = m.live.Close()
}

func (m *fakeModule) read() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.requests...)
}

func (m *fakeModule) sessions() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connections
}

// accepted waits for the module to accept one operator's hello.
func (m *fakeModule) accepted() {
	m.t.Helper()
	select {
	case <-m.arrived:
	case <-time.After(5 * time.Second):
		m.t.Fatal("no operator connected to the module")
	}
}

// servedLayoutLink is a link with a module answering it, running for
// the length of the test. Its waits are short, so a test that must
// reach a timeout reaches it at once.
func servedLayoutLink(t *testing.T, module *fakeModule) *layoutLink {
	t.Helper()
	link := newLayoutLink(module.path)
	link.reply = 200 * time.Millisecond
	link.dial = time.Millisecond
	go link.run(t.Context())
	module.accepted()
	waitUntil(t, "the link serves", link.moduleServing)
	return link
}

// waitUntil polls one state the link reaches on a goroutine of its
// own. Every wait is bounded, and a bound that is reached is the
// failure the test reports.
func waitUntil(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: never happened", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLayoutLinkGreetsTheModuleAndReadsItsOutputs(t *testing.T) {
	// The module reports every output it has before it answers the
	// hello, so the handshake reads events as they arrive.
	module := newFakeModule(t, moduleScript{greeting: []string{
		"output HDMI-A-1 1920 1080 2",
		"output HDMI-A-2 1920 1080 1",
	}})
	link := servedLayoutLink(t, module)

	if got := module.read(); len(got) != 1 || got[0] != "hello liken-operator 1" {
		t.Fatalf("the module read %q, want the operator's hello", got)
	}
	state := link.state()
	if !state.Serving || state.Reason != "" {
		t.Errorf("state = %+v, want a serving link with no reason", state)
	}
	// The logical size is what a rectangle for that screen is in: the
	// 4K panel at scale 2 lays out as a 1080p one.
	want := map[string]layoutOutput{
		"HDMI-A-1": {Connector: "HDMI-A-1", Width: 1920, Height: 1080, Scale: 2},
		"HDMI-A-2": {Connector: "HDMI-A-2", Width: 1920, Height: 1080, Scale: 1},
	}
	for connector, output := range want {
		if state.Outputs[connector] != output {
			t.Errorf("%s = %+v, want %+v", connector, state.Outputs[connector], output)
		}
	}
}

func TestLayoutLinkSendsEachRequestWithItsSequence(t *testing.T) {
	module := newFakeModule(t, moduleScript{})
	link := servedLayoutLink(t, module)

	// Every call blocks until the module answers the number it sent,
	// so the requests below are read in this order.
	for _, send := range []func() error{
		func() error { return link.Listen("wayland-claim-1", "HDMI-A-1") },
		func() error {
			return link.Place(7, "HDMI-A-1", rect{X: 0, Y: 0, W: 1344, H: 1080}, transitionFade, 300)
		},
		func() error { return link.Hide(8, transitionFade, 250) },
		func() error { return link.Order("HDMI-A-1", []int{7, 8}) },
		func() error { return link.Commit() },
		func() error { return link.Close("wayland-claim-1") },
	} {
		if err := send(); err != nil {
			t.Fatal(err)
		}
	}

	want := []string{
		"hello liken-operator 1",
		"listen wayland-claim-1 HDMI-A-1",
		"place 7 HDMI-A-1 0 0 1344 1080 fade 300",
		"hide 8 fade 250",
		"order HDMI-A-1 7 8",
		"commit",
		"close wayland-claim-1",
	}
	got := module.read()
	if len(got) != len(want) {
		t.Fatalf("the module read %q, want %q", got, want)
	}
	for i, request := range want {
		if got[i] != request {
			t.Errorf("request %d = %q, want %q", i, got[i], request)
		}
	}
}

func TestLayoutLinkReportsWhatTheModuleRefuses(t *testing.T) {
	module := newFakeModule(t, moduleScript{refuse: map[string]string{
		"listen": "wayland-claim-1 is not a name libwayland accepts",
	}})
	link := servedLayoutLink(t, module)

	err := link.Listen("wayland-claim-1", "HDMI-A-1")
	if err == nil {
		t.Fatal("a refused listen answered no error")
	}
	if !strings.Contains(err.Error(), "is not a name libwayland accepts") {
		t.Errorf("the error is %q, and it does not carry the module's reason", err)
	}
}

func TestLayoutLinkGivesUpOnAModuleThatAnswersNothing(t *testing.T) {
	// A compositor that stopped running its event loop answers no
	// request. The prepare call that waits on it is the kubelet's, and
	// it must not hang.
	module := newFakeModule(t, moduleScript{mute: true})
	link := newLayoutLink(module.path)
	link.reply = 100 * time.Millisecond
	link.dial = time.Millisecond
	go link.run(t.Context())

	waitUntil(t, "the link reports a module that answers nothing", func() bool {
		return strings.Contains(link.state().Reason, "accept the hello")
	})
	if link.moduleServing() {
		t.Error("a module that answered no hello serves the operator")
	}
}

func TestLayoutLinkRefusesARequestWithNoModuleServing(t *testing.T) {
	link := newLayoutLink(filepath.Join(t.TempDir(), "layout.sock"))

	err := link.Commit()
	if err == nil {
		t.Fatal("a commit with no connection answered no error")
	}
	if !strings.Contains(err.Error(), "is not serving") {
		t.Errorf("the error is %q, and it does not say the module serves nothing", err)
	}
}

func TestLayoutLinkReconnectsAndReplays(t *testing.T) {
	module := newFakeModule(t, moduleScript{greeting: []string{"output HDMI-A-1 1920 1080 2"}})
	link := newLayoutLink(module.path)
	link.reply = 200 * time.Millisecond
	link.dial = time.Millisecond
	// The replay is what the operator's own hook does: it re-opens
	// the socket every prepared claim holds.
	link.replay = func() error { return link.Listen("wayland-claim-1", "HDMI-A-1") }
	go link.run(t.Context())
	module.accepted()

	waitUntil(t, "the first connection replays", func() bool {
		return len(module.read()) == 2
	})
	module.send("surface 7 wayland-claim-1 1920 1080")
	waitUntil(t, "the surface arrives", func() bool { return len(link.state().Surfaces) == 1 })

	// The compositor restarts on every mode change, which ends the
	// connection and every surface with it.
	module.drop()
	module.accepted()
	waitUntil(t, "the link serves the second connection", link.moduleServing)
	waitUntil(t, "the second connection replays", func() bool {
		return len(module.read()) == 4
	})

	if sessions := module.sessions(); sessions != 2 {
		t.Errorf("the module served %d connections, want 2", sessions)
	}
	// The store is the new compositor's: the surfaces went with the
	// old one, and the outputs came back in its greeting.
	state := link.state()
	if len(state.Surfaces) != 0 {
		t.Errorf("the store holds %+v, and the compositor that drew them is gone", state.Surfaces)
	}
	if len(state.Outputs) != 1 {
		t.Errorf("the store holds %+v, want the output the new connection reported", state.Outputs)
	}
	want := []string{
		"hello liken-operator 1", "listen wayland-claim-1 HDMI-A-1",
		"hello liken-operator 1", "listen wayland-claim-1 HDMI-A-1",
	}
	got := module.read()
	for i, request := range want {
		if got[i] != request {
			t.Errorf("request %d = %q, want %q", i, got[i], request)
		}
	}
}

func TestLayoutLinkFailsAWaitingRequestWhenTheConnectionEnds(t *testing.T) {
	// A request must not wait out its timeout on a connection that is
	// already gone, because the caller is the kubelet's prepare call.
	module := newFakeModule(t, moduleScript{muteAfterHello: true})
	link := newLayoutLink(module.path)
	link.reply = 30 * time.Second
	link.dial = time.Millisecond
	go link.run(t.Context())
	module.accepted()

	// The module accepted the hello and answers nothing after it, so
	// the link serves and the commit below waits.
	waitUntil(t, "the link serves", link.moduleServing)
	failed := make(chan error, 1)
	go func() { failed <- link.Commit() }()
	waitUntil(t, "the commit reaches the module", func() bool { return len(module.read()) == 2 })
	module.drop()

	select {
	case err := <-failed:
		if err == nil {
			t.Fatal("a commit on a connection that ended answered no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the commit waited on a connection that had ended")
	}
}

func TestLayoutLinkStopsWhenTheModuleRefusesTheHello(t *testing.T) {
	// The module answers the hello with an error when it does not
	// speak the version the operator states, so the refusal is read
	// the same way the version line is.
	module := newFakeModule(t, moduleScript{refuse: map[string]string{
		layoutHelloEvent: "this module speaks version 2",
	}})
	link := newLayoutLink(module.path)
	link.reply = 200 * time.Millisecond
	link.dial = time.Millisecond
	go link.run(t.Context())

	waitUntil(t, "the link reports the refusal", func() bool {
		return strings.Contains(link.state().Reason, "this module speaks version 2")
	})
	if link.moduleServing() {
		t.Error("a module that refused the hello serves the operator")
	}
}

func TestLayoutLinkReadsAnAnswerNothingWaitsFor(t *testing.T) {
	// A request that ran out its timeout leaves no waiter behind, and
	// the module may answer it after that. The answer is read and
	// dropped, and the connection keeps serving.
	module := newFakeModule(t, moduleScript{})
	link := servedLayoutLink(t, module)

	module.send("ok 99")
	if err := link.Commit(); err != nil {
		t.Fatalf("the connection stopped serving after a late answer: %v", err)
	}
}

func TestLayoutLinkRefusesAProtocolVersionItDoesNotSpeak(t *testing.T) {
	// A module the operator does not speak to is a module that serves
	// nothing, the same answer a socket nothing listens on gives.
	module := newFakeModule(t, moduleScript{hello: "hello liken-layout 2"})
	link := newLayoutLink(module.path)
	link.reply = 200 * time.Millisecond
	link.dial = time.Millisecond
	go link.run(t.Context())

	waitUntil(t, "the link reports the version it does not speak", func() bool {
		return strings.Contains(link.state().Reason, "protocol version 2")
	})
	if link.moduleServing() {
		t.Error("a module speaking another version serves the operator")
	}
	// The link keeps dialing, because the compositor that restarts
	// next may carry the module this operator speaks to.
	waitUntil(t, "the link dials again", func() bool { return module.sessions() > 1 })
}

func TestLayoutLinkRefusesAFirstLineThatIsNotTheModulesHello(t *testing.T) {
	for _, greeting := range []string{
		"hello liken-operator 1",
		"hello liken-layout one",
		"output HDMI-A-1 1920 1080 2",
		"hello",
	} {
		if _, err := moduleVersion(greeting); err == nil {
			t.Errorf("moduleVersion(%q) read a version", greeting)
		}
	}
	version, err := moduleVersion(moduleHelloLine)
	if err != nil || version != layoutProtocolVersion {
		t.Errorf("moduleVersion(%q) = %d, %v", moduleHelloLine, version, err)
	}
}

func TestLayoutLinkRefusesATransitionTheModuleDoesNotRun(t *testing.T) {
	module := newFakeModule(t, moduleScript{})
	link := servedLayoutLink(t, module)

	err := link.Place(7, "HDMI-A-1", rect{W: 1920, H: 1080}, "dissolve", 300)
	if err == nil {
		t.Fatal("a placement with an unknown transition answered no error")
	}
	// A hide runs no move: the surface is leaving the screen, and
	// there is no rectangle to glide it to.
	if err := link.Hide(7, transitionMove, 300); err == nil {
		t.Fatal("a hide with a move answered no error")
	}
	if requests := module.read(); len(requests) != 1 {
		t.Errorf("the module read %q, and neither transition must reach it", requests)
	}
}

func TestLayoutLinkRefusesAnOrderThatNamesNoSurface(t *testing.T) {
	module := newFakeModule(t, moduleScript{})
	link := servedLayoutLink(t, module)

	if err := link.Order("HDMI-A-1", nil); err == nil {
		t.Fatal("an order with no surface in it answered no error")
	}
	if requests := module.read(); len(requests) != 1 {
		t.Errorf("the module read %q, and an empty order must not reach it", requests)
	}
}

func TestNoLayoutLinkServesNothing(t *testing.T) {
	// The operator wires the link in one place. A plugin built without
	// one refuses every prepare instead of dereferencing nothing.
	var link *layoutLink

	if link.moduleServing() {
		t.Error("a link that is not there serves the operator")
	}
	if state := link.state(); state.Serving || state.Reason == "" {
		t.Errorf("state = %+v, want a reason and no serving", state)
	}
	if err := link.Listen("wayland-claim-1", "HDMI-A-1"); err == nil {
		t.Error("a listen on a link that is not there answered no error")
	}
}

func TestParseLayoutReplyMatchesAnAnswerToItsRequest(t *testing.T) {
	for line, want := range map[string]struct {
		sequence int
		failed   bool
		isReply  bool
	}{
		"ok 7":                      {sequence: 7, isReply: true},
		"error 8 HDMI-A-9 is dark":  {sequence: 8, failed: true, isReply: true},
		"error 9":                   {sequence: 9, failed: true, isReply: true},
		"ok":                        {},
		"ok seven":                  {},
		"error eight the reason":    {},
		"surface 7 wayland-0 16 16": {},
	} {
		sequence, answer, isReply := parseLayoutReply(line)
		if isReply != want.isReply || sequence != want.sequence || (answer.err != nil) != want.failed {
			t.Errorf("parseLayoutReply(%q) = %d, %v, %v; want %d, failed=%v, reply=%v",
				line, sequence, answer.err, isReply, want.sequence, want.failed, want.isReply)
		}
	}
}
