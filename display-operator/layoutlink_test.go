package main

// These tests run the link against a fake liken-layout.so on a unix
// socket in a temporary directory.

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

// The fake module's lines are literals, not built from the operator's
// constants, so a wrong verb in the operator's code fails a test.
const moduleHelloLine = "hello liken-layout 1"

// moduleScript sets how a fake module behaves. A test must not change
// it after the module starts, because the module reads it from its own
// goroutines without a lock.
type moduleScript struct {
	// hello is the first line the module sends. Empty means
	// moduleHelloLine.
	hello string
	// greeting is the events the module sends before it replies to
	// the operator's hello.
	greeting []string
	// refuse maps a request verb to the reason of the "error" reply.
	refuse map[string]string
	// mute makes the module reply to nothing, like a compositor whose
	// event loop has stopped. muteAfterHello makes it stop after its
	// reply to the hello.
	mute           bool
	muteAfterHello bool
}

// fakeModule sends a hello on each connection and replies to each
// request as its script says.
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
	return fakeModuleOn(t, filepath.Join(t.TempDir(), "layout.sock"), script)
}

// fakeModuleOn starts the fake module at path, so that a test can
// start the link first.
func fakeModuleOn(t *testing.T, path string, script moduleScript) *fakeModule {
	t.Helper()
	if script.hello == "" {
		script.hello = moduleHelloLine
	}
	module := &fakeModule{
		t:       t,
		path:    path,
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

		// The real module sends its state after its reply to the hello.
		// The fake sends it before, so the tests also cover the events
		// that handshake reads while it waits for the reply.
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

func (m *fakeModule) send(line string) {
	m.t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live == nil {
		m.t.Fatal("the module has no connection to send on")
	}
	fmt.Fprintf(m.live, "%s\n", line)
}

// drop closes the current connection, as a compositor restart does.
func (m *fakeModule) drop() {
	m.t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live == nil {
		m.t.Fatal("the module has no connection to drop")
	}
	_ = m.live.Close()
}

// read returns the requests the module has received, in order,
// including the operator's hello.
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

// accepted waits until the module has received a hello from the
// operator.
func (m *fakeModule) accepted() {
	m.t.Helper()
	select {
	case <-m.arrived:
	case <-time.After(5 * time.Second):
		m.t.Fatal("no operator connected to the module")
	}
}

// servedLayoutLink starts a link to the module and waits until it
// serves. The link runs until the test ends.
func servedLayoutLink(t *testing.T, module *fakeModule) *layoutLink {
	t.Helper()
	link := newLayoutLink(module.path)
	link.replyTimeout = 200 * time.Millisecond
	link.dialWait = time.Millisecond
	go link.run(t.Context())
	module.accepted()
	waitUntil(t, "the link serves", link.moduleServing)
	return link
}

// The dial wait is an hour, so only the new socket file can make the
// link dial before the test times out.
func TestLayoutLinkDialsWhenTheModulesSocketAppears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.sock")
	link := newLayoutLink(path)
	link.dialWait, link.dialWaitLimit = time.Hour, time.Hour
	go link.run(t.Context())
	time.Sleep(50 * time.Millisecond)

	module := fakeModuleOn(t, path, moduleScript{})

	module.accepted()
	waitUntil(t, "the link serves", link.moduleServing)
}

// waitUntil polls ready until it returns true, and fails the test
// after five seconds. The link changes state on its own goroutine, and
// no channel reports each change, so the tests poll.
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
	// The fake module sends its outputs before it replies to the
	// hello, so the handshake must store events while it waits.
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
	// Width and height are logical sizes, the units of a placement
	// rectangle, so a 4K panel at scale 2 is 1920 by 1080.
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
			t.Fatalf("sending a request over a serving link: %v", err)
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
		t.Fatal("a refused listen returned no error")
	}
	if !strings.Contains(err.Error(), "is not a name libwayland accepts") {
		t.Errorf("the error is %q, want it to include the module's reason", err)
	}
}

func TestLayoutLinkGivesUpOnAModuleThatAnswersNothing(t *testing.T) {
	// The kubelet's prepare call must not block on a compositor whose
	// event loop has stopped.
	module := newFakeModule(t, moduleScript{mute: true})
	link := newLayoutLink(module.path)
	link.replyTimeout = 100 * time.Millisecond
	link.dialWait = time.Millisecond
	go link.run(t.Context())

	waitUntil(t, "the link reports a module that does not reply", func() bool {
		return strings.Contains(link.state().Reason, "accept the hello")
	})
	if link.moduleServing() {
		t.Error("a module that sent no hello serves the operator")
	}
}

func TestLayoutLinkRefusesARequestWithNoModuleServing(t *testing.T) {
	link := newLayoutLink(filepath.Join(t.TempDir(), "layout.sock"))

	err := link.Commit()
	if err == nil {
		t.Fatal("a commit with no connection returned no error")
	}
	if !strings.Contains(err.Error(), "is not serving") {
		t.Errorf("the error is %q, want it to say the layout module is not serving", err)
	}
}

func TestLayoutLinkReconnectsAndReplays(t *testing.T) {
	module := newFakeModule(t, moduleScript{greeting: []string{"output HDMI-A-1 1920 1080 2"}})
	link := newLayoutLink(module.path)
	link.replyTimeout = 200 * time.Millisecond
	link.dialWait = time.Millisecond
	link.replay = func() error { return link.Listen("wayland-claim-1", "HDMI-A-1") }
	go link.run(t.Context())
	module.accepted()

	waitUntil(t, "the first connection replays", func() bool {
		return len(module.read()) == 2
	})
	module.send("surface 7 wayland-claim-1 1920 1080")
	waitUntil(t, "the surface arrives", func() bool { return len(link.state().Surfaces) == 1 })

	// A compositor restart closes the connection and removes every
	// surface.
	module.drop()
	module.accepted()
	waitUntil(t, "the link serves the second connection", link.moduleServing)
	waitUntil(t, "the second connection replays", func() bool {
		return len(module.read()) == 4
	})

	if sessions := module.sessions(); sessions != 2 {
		t.Errorf("the module served %d connections, want 2", sessions)
	}
	// The store holds only what the second connection reported.
	state := link.state()
	if len(state.Surfaces) != 0 {
		t.Errorf("the store holds the surfaces %+v of the old compositor, want none", state.Surfaces)
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
	// The request must fail when the connection ends, not after the
	// 30-second reply timeout, because the caller can be the kubelet's
	// prepare call.
	module := newFakeModule(t, moduleScript{muteAfterHello: true})
	link := newLayoutLink(module.path)
	link.replyTimeout = 30 * time.Second
	link.dialWait = time.Millisecond
	go link.run(t.Context())
	module.accepted()

	waitUntil(t, "the link serves", link.moduleServing)
	failed := make(chan error, 1)
	go func() { failed <- link.Commit() }()
	waitUntil(t, "the commit reaches the module", func() bool { return len(module.read()) == 2 })
	module.drop()

	select {
	case err := <-failed:
		if err == nil {
			t.Fatal("a commit on a connection that ended returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the commit waited on a connection that had ended")
	}
}

func TestLayoutLinkStopsWhenTheModuleRefusesTheHello(t *testing.T) {
	// The real module replies "error" to a hello with a version it
	// does not speak.
	module := newFakeModule(t, moduleScript{refuse: map[string]string{
		layoutHelloEvent: "this module speaks version 2",
	}})
	link := newLayoutLink(module.path)
	link.replyTimeout = 200 * time.Millisecond
	link.dialWait = time.Millisecond
	go link.run(t.Context())

	waitUntil(t, "the link reports the refusal", func() bool {
		return strings.Contains(link.state().Reason, "this module speaks version 2")
	})
	if link.moduleServing() {
		t.Error("a module that refused the hello serves the operator")
	}
}

func TestLayoutLinkReadsAnAnswerNothingWaitsFor(t *testing.T) {
	// A reply can arrive after its request timed out. The link must
	// drop it and keep serving.
	module := newFakeModule(t, moduleScript{})
	link := servedLayoutLink(t, module)

	module.send("ok 99")
	if err := link.Commit(); err != nil {
		t.Fatalf("the connection stopped serving after a late reply: %v", err)
	}
}

func TestLayoutLinkRefusesAProtocolVersionItDoesNotSpeak(t *testing.T) {
	module := newFakeModule(t, moduleScript{hello: "hello liken-layout 2"})
	link := newLayoutLink(module.path)
	link.replyTimeout = 200 * time.Millisecond
	link.dialWait = time.Millisecond
	go link.run(t.Context())

	waitUntil(t, "the link reports the version it does not speak", func() bool {
		return strings.Contains(link.state().Reason, "protocol version 2")
	})
	if link.moduleServing() {
		t.Error("a module speaking another version serves the operator")
	}
	// The link keeps dialing, because the next compositor restart can
	// load a module with a matching version.
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
		t.Fatal("a placement with an unknown transition returned no error")
	}
	if err := link.Hide(7, transitionMove, 300); err == nil {
		t.Fatal("a hide with a move returned no error")
	}
	if requests := module.read(); len(requests) != 1 {
		t.Errorf("the module read %q, want neither transition sent", requests)
	}
}

func TestLayoutLinkRefusesAnOrderThatNamesNoSurface(t *testing.T) {
	module := newFakeModule(t, moduleScript{})
	link := servedLayoutLink(t, module)

	if err := link.Order("HDMI-A-1", nil); err == nil {
		t.Fatal("an order with no surface in it returned no error")
	}
	if requests := module.read(); len(requests) != 1 {
		t.Errorf("the module read %q, want an empty order not sent", requests)
	}
}

func TestNoLayoutLinkServesNothing(t *testing.T) {
	// A plugin with a nil link must fail each prepare call with an
	// error, not a nil pointer dereference.
	var link *layoutLink

	if link.moduleServing() {
		t.Error("a link that is not there serves the operator")
	}
	if state := link.state(); state.Serving || state.Reason == "" {
		t.Errorf("state = %+v, want a reason and no serving", state)
	}
	if err := link.Listen("wayland-claim-1", "HDMI-A-1"); err == nil {
		t.Error("a listen on a link that is not there returned no error")
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
		sequence, reply, isReply := parseLayoutReply(line)
		if isReply != want.isReply || sequence != want.sequence || (reply.err != nil) != want.failed {
			t.Errorf("parseLayoutReply(%q) = %d, %v, %v, want sequence %d, failed %v, reply %v",
				line, sequence, reply.err, isReply, want.sequence, want.failed, want.isReply)
		}
	}
}
