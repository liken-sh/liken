package main

// The operator's side of the control socket that liken-layout.so
// listens on. The module is the compositor's controller: it opens a
// Wayland socket for each claim, reports every surface it sees, and
// places a surface where this link tells it to.
//
// The protocol is lines of UTF-8 text, one message per line, tokens
// separated by single spaces. The operator is the one writer, and the
// module holds no layout of its own, so a restart on either side
// converges: a new connection clears this store, and the replay hook
// re-opens every socket the prepared claims hold.
//
// A request carries a sequence number the operator picks, and the
// module answers each one with one ok or one error line for that
// number. Every other line is an event the module sends on its own,
// and an event updates the store and wakes the operator's loop. The
// store is the operator's whole picture of what the compositor shows,
// so nothing else in this operator asks the compositor what surfaces
// it holds.
//
// A module whose protocol version this operator does not speak is a
// module that serves nothing, the same answer moduleServing gives for
// a socket nothing listens on. The taint that reports it is a later
// plan's work.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// layoutSocketPath is where the module listens, in the pod's own
// config volume.
//
// The volume is an emptyDir that only this pod's three containers
// mount, where the Wayland socket directory is a hostPath that every
// consumer mounts. A consumer that could reach this socket could
// place surfaces, so the control socket is not in the directory
// consumers reach. It is a variable so the tests can point it at a
// directory they control.
var layoutSocketPath = "/etc/weston/layout.sock"

// layoutProtocolVersion is the version of the line protocol this
// operator speaks. The module states its own in its hello, and a
// version that is not this one leaves the link serving nothing.
const layoutProtocolVersion = 1

// layoutReplyTimeout bounds every wait on the module: the hello, and
// each request after it. The module answers a request out of its own
// event loop with no work on the wire, so an answer that takes longer
// than this means the compositor is not running its loop.
const layoutReplyTimeout = 2 * time.Second

// layoutDialInterval is how long the link waits before it dials
// again, and layoutDialLimit is the longest that wait grows to. The
// compositor restarts on every mode change, so the first retry is
// quick, and a module that never comes back costs one dial every few
// seconds.
const (
	layoutDialInterval = 250 * time.Millisecond
	layoutDialLimit    = 4 * time.Second
)

// layoutLineLimit bounds one line off the socket. Every message in
// this protocol is a verb and a few numbers, so a line longer than
// this is a corrupt stream, and the bound keeps it from allocating
// whatever the bytes happen to say.
const layoutLineLimit = 4096

// The names the two ends give themselves in their hello lines.
const (
	layoutOperatorName = "liken-operator"
	layoutModuleName   = "liken-layout"
)

// helloSequence is the number the hello carries. Every connection
// starts its own count, because the module answers a sequence number
// on the connection it read it on.
const helloSequence = 1

// The transitions a placement may name. The module animates a fade
// with ivi-layout's visibility transition and a move with its
// destination transition, and it ignores the duration of none.
const (
	transitionNone = "none"
	transitionFade = "fade"
	transitionMove = "move"
)

// One answer to one request. A nil error is the module's ok line.
type layoutReply struct {
	err error
}

// layoutLink is one standing connection to the module, and what that
// connection reported.
type layoutLink struct {
	socketPath string
	// Reports carries one wake for every event the module sends. It
	// joins the operator's other wake sources in wakes(), and it holds
	// no state: a wake means read the store again.
	reports chan struct{}
	// Replay re-opens the sockets the prepared claims hold. It runs on
	// every new connection, because the compositor's restart took
	// every socket the module had opened. It is nil until the operator
	// wires it, and a nil hook replays nothing.
	replay func() error
	// The bounds of a wait on the module and of the wait between two
	// dials. They are fields so a test drives the whole link inside
	// one short test.
	reply time.Duration
	dial  time.Duration

	// Writes serializes the writes to the socket. It is a second lock
	// so that a write which blocks in the kernel never holds the lock
	// the read loop needs to deliver a reply.
	writes sync.Mutex

	mu       sync.Mutex
	conn     net.Conn
	serving  bool
	reason   string
	sequence int
	waiting  map[int]chan layoutReply
	outputs  map[string]layoutOutput
	surfaces map[int]layoutSurface
	// Generation counts the connections this link has opened. A reader
	// that remembers what it sent compares it with the generation it
	// sent on, because a new connection is a new compositor: the
	// surfaces and their ids went with the old one, and everything the
	// reader stated has to be stated again.
	generation int
}

func newLayoutLink(socketPath string) *layoutLink {
	return &layoutLink{
		socketPath: socketPath,
		reports:    make(chan struct{}, 1),
		reply:      layoutReplyTimeout,
		dial:       layoutDialInterval,
		reason:     "the link has not connected yet",
		waiting:    map[int]chan layoutReply{},
		outputs:    map[string]layoutOutput{},
		surfaces:   map[int]layoutSurface{},
	}
}

// moduleServing reports whether the module answers this operator
// right now. It is the layout module's half of the gate that
// compositorServing is the compositor's half of: a prepare that
// passed both delivers a socket that a client can connect to and a
// controller that will place what the client draws.
//
// A link the operator never wired serves nothing, which is what the
// tests that drive a prepare with no module behind it rely on.
func (l *layoutLink) moduleServing() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.serving
}

// Listen asks the module to open one Wayland socket and to remember
// which output the surfaces that arrive on it go on. The call is
// idempotent, so a replay re-issues it for a socket that is already
// open.
func (l *layoutLink) Listen(socketName, connector string) error {
	return l.request(fmt.Sprintf("listen %s %s", socketName, connector))
}

// Close asks the module to unlink one Wayland socket and stop
// accepting on it. It names the socket, not this link: the link's own
// connection ends with its context. Clients already connected to the
// socket keep their connections, because the compositor keeps their
// surfaces.
func (l *layoutLink) Close(socketName string) error {
	return l.request("close " + socketName)
}

// Place states where one surface shows. Nothing reaches the screen
// until Commit, so a whole batch of placements lands in one frame.
func (l *layoutLink) Place(id int, connector string, where rect, transition string, milliseconds int) error {
	switch transition {
	case transitionNone, transitionFade, transitionMove:
	default:
		return fmt.Errorf("%q is no transition this module runs: %s, %s, or %s",
			transition, transitionNone, transitionFade, transitionMove)
	}
	return l.request(fmt.Sprintf("place %d %s %d %d %d %d %s %d",
		id, connector, where.X, where.Y, where.W, where.H, transition, milliseconds))
}

// Hide makes one surface invisible, and Commit is what takes it off
// the screen. It carries the two tokens a placement carries: a fade
// runs ivi-layout's visibility-off transition, which is how a surface
// that is still drawing leaves a region, and none takes it off at
// once.
//
// A hide runs no move. The surface is leaving the screen, so there is
// no rectangle to glide it to.
func (l *layoutLink) Hide(id int, transition string, milliseconds int) error {
	switch transition {
	case transitionNone, transitionFade:
	default:
		return fmt.Errorf("%q is no transition a hide runs: %s or %s",
			transition, transitionNone, transitionFade)
	}
	return l.request(fmt.Sprintf("hide %d %s %d", id, transition, milliseconds))
}

// Order states the stacking order of one output's surfaces, bottom
// first. A surface on the output that the list leaves out keeps its
// relative order below the listed ones.
//
// An order that names no surface says nothing, and the protocol has
// no line for it, so this refuses it here rather than sending a verb
// with no argument.
func (l *layoutLink) Order(connector string, ids []int) error {
	if len(ids) == 0 {
		return fmt.Errorf("the order for %s names no surface", connector)
	}
	var line strings.Builder
	fmt.Fprintf(&line, "order %s", connector)
	for _, id := range ids {
		fmt.Fprintf(&line, " %d", id)
	}
	return l.request(line.String())
}

// Commit is one ivi commit_changes for every placement since the last
// one.
func (l *layoutLink) Commit() error {
	return l.request("commit")
}

// request sends one line and waits for the module's answer to that
// line's sequence number.
//
// The wait is bounded, because the caller is the kubelet's prepare
// call or the operator's own loop, and neither may hang on a
// compositor that stopped running its event loop. A connection that
// ends while a request waits fails that request at once, for the same
// reason.
func (l *layoutLink) request(request string) error {
	if l == nil {
		return errors.New("the operator wired no layout module")
	}
	l.mu.Lock()
	socket := l.conn
	if socket == nil {
		reason := l.reason
		l.mu.Unlock()
		return fmt.Errorf("the layout module is not serving %s: %s", l.socketPath, reason)
	}
	l.sequence++
	sequence := l.sequence
	answers := make(chan layoutReply, 1)
	l.waiting[sequence] = answers
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.waiting, sequence)
		l.mu.Unlock()
	}()

	if err := l.write(socket, fmt.Sprintf("%d %s", sequence, request)); err != nil {
		return fmt.Errorf("sending %q to the layout module: %w", request, err)
	}
	select {
	case answer := <-answers:
		if answer.err != nil {
			return fmt.Errorf("the layout module refused %q: %w", request, answer.err)
		}
		return nil
	case <-time.After(l.reply):
		return fmt.Errorf("the layout module did not answer %q within %s", request, l.reply)
	}
}

// write puts one line on the socket, under a deadline of its own. A
// module that stopped reading fills the socket's buffer, and a write
// with no deadline would block for as long as that lasted.
func (l *layoutLink) write(socket net.Conn, line string) error {
	l.writes.Lock()
	defer l.writes.Unlock()
	if err := socket.SetWriteDeadline(time.Now().Add(l.reply)); err != nil {
		return err
	}
	_, err := io.WriteString(socket, line+"\n")
	return err
}

// run keeps one connection to the module for as long as the operator
// runs. A connection that served resets the wait, and a dial that
// found nothing doubles it, so a compositor that is restarting is
// picked up in a quarter second and a module that is gone costs one
// dial every few seconds.
func (l *layoutLink) run(ctx context.Context) {
	wait := l.dial
	for {
		if l.connection(ctx) {
			wait = l.dial
		} else {
			wait = min(2*wait, layoutDialLimit)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// connection runs one connection from the dial to the end of the
// module, and answers whether the handshake succeeded.
func (l *layoutLink) connection(ctx context.Context) bool {
	socket, err := net.DialTimeout("unix", l.socketPath, socketDialTimeout)
	if err != nil {
		l.down(fmt.Sprintf("dialing %s: %v", l.socketPath, err))
		return false
	}
	defer func() { _ = socket.Close() }()

	// The read loop below blocks in the kernel, so the way the context
	// ends this connection is by closing the socket under it.
	ended := make(chan struct{})
	defer close(ended)
	go func() {
		select {
		case <-ctx.Done():
			_ = socket.Close()
		case <-ended:
		}
	}()

	// Every connection starts from an empty store. The compositor's
	// surfaces went with the compositor, and the outputs the module
	// reports at connect time are what it has now.
	l.opened()
	lines := bufio.NewScanner(socket)
	lines.Buffer(make([]byte, 0, layoutLineLimit), layoutLineLimit)
	if err := l.handshake(socket, lines); err != nil {
		l.down(err.Error())
		return false
	}
	l.up(socket)
	defer l.down("the connection to the layout module ended")

	// The replay's own requests need the read loop below to deliver
	// their replies, so it runs beside the loop and not before it.
	if l.replay != nil {
		go func() {
			if err := l.replay(); err != nil {
				fmt.Fprintf(os.Stderr, "re-opening the Wayland sockets the prepared claims hold: %v\n", err)
			}
		}()
	}
	for lines.Scan() {
		l.line(lines.Text())
	}
	return true
}

// handshake reads the module's hello, states this operator's own, and
// waits for the module to accept it.
//
// The module reports every output it has before it answers, so the
// wait for the answer reads events as they arrive.
func (l *layoutLink) handshake(socket net.Conn, lines *bufio.Scanner) error {
	greeting, err := l.readLine(socket, lines)
	if err != nil {
		return fmt.Errorf("reading the layout module's hello: %w", err)
	}
	version, err := moduleVersion(greeting)
	if err != nil {
		return err
	}
	if version != layoutProtocolVersion {
		return fmt.Errorf("the layout module speaks protocol version %d and this operator speaks %d",
			version, layoutProtocolVersion)
	}
	if err := l.write(socket, fmt.Sprintf("%d %s %s %d",
		helloSequence, layoutHelloEvent, layoutOperatorName, layoutProtocolVersion)); err != nil {
		return fmt.Errorf("stating this operator's hello: %w", err)
	}
	for {
		line, err := l.readLine(socket, lines)
		if err != nil {
			return fmt.Errorf("waiting for the layout module to accept the hello: %w", err)
		}
		sequence, answer, isReply := parseLayoutReply(line)
		if !isReply {
			l.event(line)
			continue
		}
		if sequence != helloSequence {
			return fmt.Errorf("the layout module answered sequence %d before it answered the hello", sequence)
		}
		if answer.err != nil {
			return fmt.Errorf("the layout module refused the hello: %w", answer.err)
		}
		return nil
	}
}

// readLine reads one line under the reply timeout. Every wait on the
// module is bounded, and the handshake's waits are the two that run
// before there is a read loop to bound them.
func (l *layoutLink) readLine(socket net.Conn, lines *bufio.Scanner) (string, error) {
	if err := socket.SetReadDeadline(time.Now().Add(l.reply)); err != nil {
		return "", err
	}
	defer func() { _ = socket.SetReadDeadline(time.Time{}) }()
	if !lines.Scan() {
		if err := lines.Err(); err != nil {
			return "", err
		}
		return "", errors.New("the layout module closed the connection")
	}
	return lines.Text(), nil
}

// moduleVersion reads the version off the module's hello line and
// refuses a first line that is not one.
func moduleVersion(greeting string) (int, error) {
	fields := strings.Fields(greeting)
	if len(fields) != 3 || fields[0] != layoutHelloEvent || fields[1] != layoutModuleName {
		return 0, fmt.Errorf("%q is not the layout module's hello", greeting)
	}
	version, read := layoutNumber(fields[2])
	if !read {
		return 0, fmt.Errorf("the layout module states protocol version %q", fields[2])
	}
	return version, nil
}

// line routes one line off the socket: a reply goes to the request
// that is waiting for it, and everything else is an event.
func (l *layoutLink) line(text string) {
	if sequence, answer, isReply := parseLayoutReply(text); isReply {
		l.answer(sequence, answer)
		return
	}
	l.event(text)
}

// parseLayoutReply reads an ok or an error line and the sequence
// number it answers. An error line with no text of its own still
// carries an error, because a caller reports what the module refused.
func parseLayoutReply(text string) (int, layoutReply, bool) {
	verb, rest, split := strings.Cut(text, " ")
	if !split {
		return 0, layoutReply{}, false
	}
	switch verb {
	case "ok":
		sequence, read := layoutNumber(rest)
		if !read {
			return 0, layoutReply{}, false
		}
		return sequence, layoutReply{}, true
	case "error":
		number, message, _ := strings.Cut(rest, " ")
		sequence, read := layoutNumber(number)
		if !read {
			return 0, layoutReply{}, false
		}
		if message == "" {
			message = "the module gave no reason"
		}
		return sequence, layoutReply{err: errors.New(message)}, true
	}
	return 0, layoutReply{}, false
}

// answer hands one reply to the request waiting for it. A reply for a
// sequence nobody waits on is a request that already ran out its
// timeout, which is worth reading in the log: the module answered,
// late.
func (l *layoutLink) answer(sequence int, answer layoutReply) {
	l.mu.Lock()
	waiting, expected := l.waiting[sequence]
	delete(l.waiting, sequence)
	l.mu.Unlock()
	if !expected {
		fmt.Fprintf(os.Stderr, "the layout module answered sequence %d, which nothing is waiting for\n", sequence)
		return
	}
	waiting <- answer
}

// opened empties the store for a new connection.
func (l *layoutLink) opened() {
	l.mu.Lock()
	l.outputs = map[string]layoutOutput{}
	l.surfaces = map[int]layoutSurface{}
	l.sequence = helloSequence
	l.generation++
	l.mu.Unlock()
	l.signal()
}

// up marks the link serving on the connection the handshake accepted.
func (l *layoutLink) up(socket net.Conn) {
	l.mu.Lock()
	l.conn, l.serving, l.reason = socket, true, ""
	l.mu.Unlock()
	l.signal()
}

// down records why the link serves nothing and fails every request
// that is still waiting.
//
// The store keeps what the ended connection reported until the next
// connection empties it. Nothing acts on it in the meantime, because
// the state a reader holds says the module is not serving.
func (l *layoutLink) down(reason string) {
	l.mu.Lock()
	l.conn, l.serving, l.reason = nil, false, reason
	waiting := l.waiting
	l.waiting = map[int]chan layoutReply{}
	l.mu.Unlock()
	for _, answers := range waiting {
		answers <- layoutReply{err: errors.New(reason)}
	}
	l.signal()
}

// signal wakes the operator's loop. The channel holds one wake,
// because a wake means read the store again and two of them mean the
// same thing.
func (l *layoutLink) signal() {
	select {
	case l.reports <- struct{}{}:
	default:
	}
}
