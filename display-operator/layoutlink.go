package main

// This file is the operator's client for the control socket of
// liken-layout.so, the module that weston loads. The module opens one
// Wayland socket for each claim, reports the compositor's outputs and
// surfaces, and places each surface where the operator tells it.
//
// The protocol is text, one message per line, with tokens separated by
// single spaces. The operator puts a sequence number on each request,
// and the module replies with "ok <sequence>" or
// "error <sequence> <reason>". Every other line from the module is an
// event, which updates the store in layoutstore.go and wakes the
// operator's loop.
//
// The module keeps no layout of its own. On each new connection the
// link empties the store, the module sends its current state, and the
// replay hook opens the Wayland socket of each prepared claim again.
// So a restart of the compositor and a restart of the operator recover
// the same way.

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

// layoutSocketPath is where the module listens. It is in the
// weston-config emptyDir, which only this pod mounts, and not in the
// Wayland socket directory, a hostPath that every consumer pod mounts,
// because a consumer that reaches this socket can place any surface.
var layoutSocketPath = "/etc/weston/layout.sock"

// layoutProtocolVersion is the protocol version this operator speaks.
// When the module's hello line has another version, the handshake
// fails and moduleServing returns false.
const layoutProtocolVersion = 1

// layoutReplyTimeout is how long the link waits for the module's
// hello and for the reply to each request. The module handles each
// request on the compositor's event loop and replies at once, so no
// reply in this time means the event loop has stopped.
const layoutReplyTimeout = 2 * time.Second

// layoutDialInterval is the first wait before the link dials again,
// and layoutDialLimit is the longest the wait grows to. A new socket
// file ends the wait early (arrivals.go), so these waits apply when a
// dial or a handshake fails and no new file appears.
const (
	layoutDialInterval = 250 * time.Millisecond
	layoutDialLimit    = 4 * time.Second
)

// layoutLineLimit is the longest line the link reads. A message is a
// verb, some numbers, and at most one name, such as an app_id, so a
// longer line means a corrupt stream. The scanner fails on it, and the
// link closes the connection and dials again.
const layoutLineLimit = 4096

const (
	layoutOperatorName = "liken-operator"
	layoutModuleName   = "liken-layout"
)

// helloSequence is the sequence number of the operator's hello. Each
// connection numbers its requests from this value.
const helloSequence = 1

// The transitions of a placement. The module animates a fade and a
// move itself, on each repaint of the output, and does not use
// ivi-layout's transitions. It ignores the duration of none, and it
// refuses a fade or a move of 0 milliseconds.
const (
	transitionNone = "none"
	transitionFade = "fade"
	transitionMove = "move"
)

// layoutReply is the module's reply to one request. A nil err means
// "ok".
type layoutReply struct {
	err error
}

// layoutLink is the operator's connection to the module and the state
// that the module has reported over it.
type layoutLink struct {
	socketPath string
	// reports wakes the operator's loop, which receives from it
	// through wakes in main.go and then reads the store.
	reports chan struct{}
	// replay opens the Wayland sockets of the prepared claims again
	// after each handshake, because a compositor restart closes every
	// socket the module had opened. A nil replay does nothing.
	replay func() error
	// These are fields so that tests can shorten them.
	replyTimeout  time.Duration
	dialWait      time.Duration
	dialWaitLimit time.Duration

	// writes serializes writes to the socket. It is separate from mu
	// so that a write blocked in the kernel does not hold the lock
	// that the read loop needs to deliver a reply.
	writes sync.Mutex

	mu       sync.Mutex
	conn     net.Conn
	serving  bool
	reason   string
	sequence int
	waiting  map[int]chan layoutReply
	outputs  map[string]layoutOutput
	surfaces map[int]layoutSurface
	// arrangers and departures are keyed by the claim's socket
	// (arrangerstore.go).
	arrangers  map[string]layoutArranger
	departures map[string]string
	// generation counts the connections this link has opened. A new
	// connection can be to a restarted compositor, whose surfaces and
	// surface ids differ, so a caller that tracks what it sent sends
	// everything again when the generation changes.
	generation int
}

func newLayoutLink(socketPath string) *layoutLink {
	return &layoutLink{
		socketPath:    socketPath,
		reports:       make(chan struct{}, 1),
		replyTimeout:  layoutReplyTimeout,
		dialWait:      layoutDialInterval,
		dialWaitLimit: layoutDialLimit,
		reason:        "the link has not connected yet",
		waiting:       map[int]chan layoutReply{},
		outputs:       map[string]layoutOutput{},
		surfaces:      map[int]layoutSurface{},
		arrangers:     map[string]layoutArranger{},
		departures:    map[string]string{},
	}
}

// moduleServing reports whether the handshake with the module has
// succeeded and the connection is still open. A prepare call fails
// while it returns false (waylandServing in dra.go). A nil link
// returns false, so tests of a prepare call can run without a module.
func (l *layoutLink) moduleServing() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.serving
}

// Listen asks the module to open a Wayland socket and to put the
// surfaces of its clients on the given output. The replay sends Listen
// again for a socket that is already open, and that is not an error.
func (l *layoutLink) Listen(socketName, connector string) error {
	return l.request(fmt.Sprintf("listen %s %s", socketName, connector))
}

// Close asks the module to stop accepting connections on a Wayland
// socket and remove it. Clients already connected keep their
// connections and surfaces. Close does not close this link.
func (l *layoutLink) Close(socketName string) error {
	return l.request("close " + socketName)
}

// Place sets the rectangle and the transition of a surface. The screen
// changes only at Commit.
func (l *layoutLink) Place(id int, connector string, where rect, transition string, milliseconds int) error {
	switch transition {
	case transitionNone, transitionFade, transitionMove:
	default:
		return fmt.Errorf("unknown transition %q for a placement, use %s, %s, or %s",
			transition, transitionNone, transitionFade, transitionMove)
	}
	return l.request(fmt.Sprintf("place %d %s %d %d %d %d %s %d",
		id, connector, where.X, where.Y, where.W, where.H, transition, milliseconds))
}

// Hide takes a surface off the screen at the next Commit. With fade,
// the module fades out a surface that is on the screen and hides it
// when the fade ends. With none, or for a surface that is not on the
// screen, it hides the surface at once. A hide cannot move, because a
// hidden surface has no destination rectangle.
func (l *layoutLink) Hide(id int, transition string, milliseconds int) error {
	switch transition {
	case transitionNone, transitionFade:
	default:
		return fmt.Errorf("unknown transition %q for a hide, use %s or %s",
			transition, transitionNone, transitionFade)
	}
	return l.request(fmt.Sprintf("hide %d %s %d", id, transition, milliseconds))
}

// Order sets the stacking order of surfaces on an output, bottom
// first. Surfaces on the output that are not in the list stay below
// the listed ones, in their previous order. The protocol has no
// message for an empty list, so Order returns an error for one and
// sends nothing.
func (l *layoutLink) Order(connector string, ids []int) error {
	if len(ids) == 0 {
		return fmt.Errorf("the order for %s lists no surface", connector)
	}
	var line strings.Builder
	fmt.Fprintf(&line, "order %s", connector)
	for _, id := range ids {
		fmt.Fprintf(&line, " %d", id)
	}
	return l.request(line.String())
}

// Commit applies every placement, hide, and order sent since the last
// Commit, in one ivi-layout commit_changes, and starts their
// transitions.
func (l *layoutLink) Commit() error {
	return l.request("commit")
}

// request sends a request and waits for the reply with its sequence
// number. The callers are the kubelet's prepare call and the
// operator's loop, and neither can block on a stopped compositor, so
// the wait ends after replyTimeout, and at once when the connection
// ends.
func (l *layoutLink) request(request string) error {
	if l == nil {
		return errors.New("the operator has no link to the layout module")
	}
	l.mu.Lock()
	socket := l.conn
	if socket == nil {
		reason := l.reason
		l.mu.Unlock()
		return fmt.Errorf("the layout module at %s is not serving: %s", l.socketPath, reason)
	}
	l.sequence++
	sequence := l.sequence
	replies := make(chan layoutReply, 1)
	l.waiting[sequence] = replies
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.waiting, sequence)
		l.mu.Unlock()
	}()

	if err := l.write(socket, fmt.Sprintf("%d %s", sequence, request)); err != nil {
		return fmt.Errorf("writing %q to the layout module: %w", request, err)
	}
	select {
	case reply := <-replies:
		if reply.err != nil {
			return fmt.Errorf("the layout module refused %q: %w", request, reply.err)
		}
		return nil
	case <-time.After(l.replyTimeout):
		return fmt.Errorf("the layout module sent no reply to %q within %s", request, l.replyTimeout)
	}
}

// write sends one line with a deadline, because a module that stops
// reading fills the socket buffer, and a write without a deadline
// would then block.
func (l *layoutLink) write(socket net.Conn, line string) error {
	l.writes.Lock()
	defer l.writes.Unlock()
	if err := socket.SetWriteDeadline(time.Now().Add(l.replyTimeout)); err != nil {
		return err
	}
	_, err := io.WriteString(socket, line+"\n")
	return err
}

// run connects to the module and reconnects until ctx ends. A
// compositor restart creates a new socket file, and run dials as soon
// as it appears (arrivals.go). Otherwise it dials after a wait, which
// nextDialWait resets after a connection that served and doubles after
// each failure, up to dialWaitLimit.
func (l *layoutLink) run(ctx context.Context) {
	watch := newArrivals(l.socketPath)
	defer watch.close()
	wait := l.dialWait
	arrived := false
	for {
		watch.ready()
		served := l.connection(ctx)
		wait = nextDialWait(served, arrived, wait, l.dialWait, l.dialWaitLimit)
		arrived = watch.wait(ctx, wait)
		if ctx.Err() != nil {
			return
		}
	}
}

// connection dials the module and reads from it until the connection
// ends. It returns whether the handshake succeeded.
func (l *layoutLink) connection(ctx context.Context) bool {
	socket, err := net.DialTimeout("unix", l.socketPath, socketDialTimeout)
	if err != nil {
		l.down(fmt.Sprintf("dialing the layout module: %v", err))
		return false
	}
	defer func() { _ = socket.Close() }()

	// The scanner blocks in a read, so the socket must be closed when
	// ctx ends.
	ended := make(chan struct{})
	defer close(ended)
	go func() {
		select {
		case <-ctx.Done():
			_ = socket.Close()
		case <-ended:
		}
	}()

	l.opened()
	lines := bufio.NewScanner(socket)
	lines.Buffer(make([]byte, 0, layoutLineLimit), layoutLineLimit)
	if err := l.handshake(socket, lines); err != nil {
		l.down(err.Error())
		return false
	}
	l.up(socket)
	defer l.down("the connection to the layout module ended")

	// The replay's requests wait for replies that only the read loop
	// below delivers, so the replay runs in its own goroutine.
	if l.replay != nil {
		go func() {
			if err := l.replay(); err != nil {
				fmt.Fprintf(os.Stderr, "opening the Wayland sockets of the prepared claims again: %v\n", err)
			}
		}()
	}
	for lines.Scan() {
		l.receive(lines.Text())
	}
	return true
}

// handshake reads the module's hello, sends the operator's hello, and
// waits for the reply. An event that arrives before the reply goes to
// the store.
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
		return fmt.Errorf("sending the operator's hello: %w", err)
	}
	for {
		line, err := l.readLine(socket, lines)
		if err != nil {
			return fmt.Errorf("waiting for the layout module to accept the hello: %w", err)
		}
		sequence, reply, isReply := parseLayoutReply(line)
		if !isReply {
			l.event(line)
			continue
		}
		if sequence != helloSequence {
			return fmt.Errorf("the layout module replied to sequence %d before it replied to the hello", sequence)
		}
		if reply.err != nil {
			return fmt.Errorf("the layout module refused the hello: %w", reply.err)
		}
		return nil
	}
}

// readLine reads one line with replyTimeout as the read deadline. Only
// the handshake uses it. The read loop after the handshake has no
// deadline.
func (l *layoutLink) readLine(socket net.Conn, lines *bufio.Scanner) (string, error) {
	if err := socket.SetReadDeadline(time.Now().Add(l.replyTimeout)); err != nil {
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

// moduleVersion returns the protocol version in the module's hello
// line, and an error when the line is not a hello from the module.
func moduleVersion(greeting string) (int, error) {
	fields := strings.Fields(greeting)
	if len(fields) != 3 || fields[0] != layoutHelloEvent || fields[1] != layoutModuleName {
		return 0, fmt.Errorf("the first line %q is not the layout module's hello", greeting)
	}
	version, read := layoutNumber(fields[2])
	if !read {
		return 0, fmt.Errorf("the layout module's hello has the protocol version %q, which is not a number", fields[2])
	}
	return version, nil
}

// receive delivers a reply to its request and applies any other line
// to the store as an event.
func (l *layoutLink) receive(text string) {
	if sequence, reply, isReply := parseLayoutReply(text); isReply {
		l.deliver(sequence, reply)
		return
	}
	l.event(text)
}

// parseLayoutReply parses an "ok" or "error" line. The last result is
// false when the line is not a reply. An "error" line with no reason
// gets a default message, so the caller always has an error to report.
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
			message = "the module sent no reason"
		}
		return sequence, layoutReply{err: errors.New(message)}, true
	}
	return 0, layoutReply{}, false
}

// deliver sends a reply to the request that waits for it. When no
// request waits, the request already timed out, and deliver logs the
// late reply.
func (l *layoutLink) deliver(sequence int, reply layoutReply) {
	l.mu.Lock()
	waiting, expected := l.waiting[sequence]
	delete(l.waiting, sequence)
	l.mu.Unlock()
	if !expected {
		fmt.Fprintf(os.Stderr, "the layout module replied to sequence %d, which no request is waiting for\n", sequence)
		return
	}
	waiting <- reply
}

// opened empties the store and starts a new generation.
func (l *layoutLink) opened() {
	l.mu.Lock()
	l.outputs = map[string]layoutOutput{}
	l.surfaces = map[int]layoutSurface{}
	l.arrangers = map[string]layoutArranger{}
	l.departures = map[string]string{}
	l.sequence = helloSequence
	l.generation++
	l.mu.Unlock()
	l.signal()
}

func (l *layoutLink) up(socket net.Conn) {
	l.mu.Lock()
	l.conn, l.serving, l.reason = socket, true, ""
	l.mu.Unlock()
	l.signal()
}

// down records why the link is not serving and fails the requests
// that are still waiting. The store keeps what the old connection
// reported until the next connection empties it, so the placement pass
// checks Serving before it reads the store.
func (l *layoutLink) down(reason string) {
	l.mu.Lock()
	l.conn, l.serving, l.reason = nil, false, reason
	waiting := l.waiting
	l.waiting = map[int]chan layoutReply{}
	l.mu.Unlock()
	for _, replies := range waiting {
		replies <- layoutReply{err: errors.New(reason)}
	}
	l.signal()
}

// signal wakes the operator's loop. The channel buffers one wake, and
// signal drops a second one, because the loop reads the whole store on
// each wake.
func (l *layoutLink) signal() {
	select {
	case l.reports <- struct{}{}:
	default:
	}
}
