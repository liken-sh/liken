// Package phd2 is a client of PHD2's event server, the API that KStars
// and astrophotography-operator drive PHD2 through. The server listens
// on port 4400 for PHD2's first instance and speaks JSON-RPC 2.0 over
// TCP: each message is one line of JSON that ends in CR LF. PHD2 sends
// events on the same connection as the answers to requests, in the
// order it makes them. The protocol is documented only on PHD2's wiki,
// at https://github.com/OpenPHDGuiding/phd2/wiki/EventMonitoring, and
// where the wiki is silent the client follows src/event_server.cpp.
//
// A Client follows the repository's rule for events. Run opens the
// connection, which is the subscription, and then reads the app state,
// the calibration, the equipment, and the pixel scale once as the
// baseline. The events keep the state current after that. When the
// connection ends, the state empties, and the next Run reads a new
// baseline. Nothing in the package reads state again on a timer.
package phd2

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Port is the event server's port for PHD2's first instance. PHD2
// listens on 4400 plus the instance number minus one.
const Port = 4400

// DialTimeout bounds the TCP dial of each Run. The caller's context can
// end the dial sooner.
const DialTimeout = 10 * time.Second

// writeTimeout bounds each write. A PHD2 that reads nothing for this
// long is stuck, and the write fails rather than block the caller.
const writeTimeout = 10 * time.Second

// maxLine bounds one message. The largest answer PHD2 sends is
// get_star_image, which this client never asks for, and the events
// are a few hundred bytes.
const maxLine = 1 << 20

var (
	// ErrRunning is the error of a Run while another Run of the same
	// Client has not returned.
	ErrRunning = errors.New("phd2: the client is already running")
	// ErrNotConnected is the error of a request while the client has no
	// connection.
	ErrNotConnected = errors.New("phd2: no connection to PHD2")
	// ErrDisconnected is the error of a request whose connection ended
	// before PHD2 answered, and the error that ends Run when PHD2
	// closes the connection.
	ErrDisconnected = errors.New("phd2: the connection ended")
)

// Dialer opens the connection of each Run. *net.Dialer is one.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Option changes a Client that NewClient returns.
type Option func(*Client)

// WithDialer makes each Run open its connection through dialer. A test
// passes a dialer that answers with one end of an in-memory pipe, so it
// can run on the fake clock of testing/synctest, which a real socket
// stops.
func WithDialer(dialer Dialer) Option { return func(c *Client) { c.dialer = dialer } }

// WithNotify calls notify after each change to the state. It runs on
// the client's reader, so it must not block and must not call the
// Client.
func WithNotify(notify func()) Option { return func(c *Client) { c.notify = notify } }

// Client is a client of one PHD2. Its methods are safe to call from
// several goroutines.
type Client struct {
	address string
	dialer  Dialer
	notify  func()

	// mu guards every field below it. The reader applies each message
	// under it, so a reader of the state never reads half a message.
	mu      sync.Mutex
	conn    net.Conn
	running bool
	state   State
	rms     window
	nextID  int
	pending map[int]*request
	// changed is closed and replaced on every change, which wakes each
	// WaitFor to check its condition again.
	changed chan struct{}
	// reread holds the reads that events asked for and the refresher
	// has not sent yet.
	reread map[string]bool
	wake   chan struct{}

	// writing serializes the writes, so two messages never interleave.
	writing sync.Mutex
}

// request is one request that waits for its answer. The reader applies
// a read's answer to the state itself, so a read needs no waiter.
type request struct {
	method string
	done   chan error
}

// NewClient returns a client of the PHD2 at address, such as
// "east-guider.observatory.svc:4400". It opens no connection until Run.
func NewClient(address string, options ...Option) *Client {
	c := &Client{
		address: address,
		dialer:  &net.Dialer{Timeout: DialTimeout},
		notify:  func() {},
		changed: make(chan struct{}),
		reread:  map[string]bool{},
		wake:    make(chan struct{}, 1),
	}
	for _, option := range options {
		option(c)
	}
	return c
}

// Run dials PHD2, reads the baseline, and applies each message until
// the connection fails or ctx ends. It always returns an error: the
// reason the connection ended, or the error of ctx. Run opens one
// connection, and the caller decides when to open the next.
func (c *Client) Run(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return ErrRunning
	}
	c.running = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
	}()

	conn, err := c.dialer.DialContext(ctx, "tcp", c.address)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(runCtx, func() { conn.Close() })
	defer stop()

	c.mu.Lock()
	c.conn = conn
	c.state, c.rms = State{Open: true}, window{}
	c.pending = map[int]*request{}
	// The connection is open, so every event from here on reaches the
	// reader. The baseline reads go out now, and their answers arrive
	// after any event that PHD2 made before it read them.
	c.reread = map[string]bool{methodAppState: true, methodCalibrated: true, methodConnected: true, methodPixelScale: true}
	c.changeLocked()
	c.mu.Unlock()

	var refresher sync.WaitGroup
	refresher.Go(func() { c.refresh(runCtx) })
	err = c.read(conn)
	cancel()
	refresher.Wait()
	conn.Close()
	if ctx.Err() != nil {
		err = ctx.Err()
	}

	c.mu.Lock()
	c.conn = nil
	c.state, c.rms = State{}, window{}
	for _, r := range c.pending {
		if r.done != nil {
			r.done <- ErrDisconnected
		}
	}
	c.pending = nil
	c.changeLocked()
	c.mu.Unlock()
	return err
}

// read applies each line until the stream ends.
func (c *Client) read(conn net.Conn) error {
	lines := bufio.NewScanner(conn)
	lines.Buffer(make([]byte, 64*1024), maxLine)
	for lines.Scan() {
		if err := c.receive(lines.Bytes()); err != nil {
			return err
		}
	}
	if err := lines.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrDisconnected, err)
	}
	return ErrDisconnected
}

// The reads of the baseline. Each one also follows an event that can
// change what it reads, because PHD2 sends no event for that change.
const (
	methodAppState   = "get_app_state"
	methodCalibrated = "get_calibrated"
	methodConnected  = "get_connected"
	methodPixelScale = "get_pixel_scale"
)

// refresh sends the reads that the baseline and the events ask for.
// It runs beside the reader, because a read written from the reader
// would wait on a PHD2 that waits for the reader.
func (c *Client) refresh(ctx context.Context) {
	for {
		c.mu.Lock()
		var methods []string
		for _, m := range []string{methodAppState, methodCalibrated, methodConnected, methodPixelScale} {
			if c.reread[m] {
				methods = append(methods, m)
				delete(c.reread, m)
			}
		}
		c.mu.Unlock()
		for _, m := range methods {
			if err := c.send(m, nil, nil); err != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
		}
	}
}

// rereadLocked asks the refresher to send reads. The caller holds mu.
func (c *Client) rereadLocked(methods ...string) {
	for _, m := range methods {
		c.reread[m] = true
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// changeLocked wakes each waiter and calls notify. The caller holds mu.
func (c *Client) changeLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
	c.notify()
}

// send writes one request. A request with done waits for its answer
// there; one without is a read whose answer the reader applies.
func (c *Client) send(method string, params []any, done chan error) error {
	c.mu.Lock()
	conn := c.conn
	if conn == nil {
		c.mu.Unlock()
		return ErrNotConnected
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = &request{method: method, done: done}
	c.mu.Unlock()
	message := map[string]any{"method": method, "id": id}
	if params != nil {
		message["params"] = params
	}
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	c.writing.Lock()
	defer c.writing.Unlock()
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	_, err = conn.Write(append(body, '\r', '\n'))
	return err
}

// call sends one request and waits for its answer, or for ctx.
func (c *Client) call(ctx context.Context, method string, params []any) error {
	done := make(chan error, 1)
	if err := c.send(method, params, done); err != nil {
		return err
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// SetConnected connects or disconnects the camera and the mount that
// PHD2's profile names, and reads the equipment's state again. PHD2
// answers when every connection has finished or failed, which takes
// seconds on real hardware, so ctx bounds the wait.
func (c *Client) SetConnected(ctx context.Context, connected bool) error {
	if err := c.call(ctx, "set_connected", []any{connected}); err != nil {
		return err
	}
	return c.call(ctx, methodConnected, nil)
}

// StopCapture stops looping and guiding. PHD2 answers at once, and the
// events that follow say where the stop left it.
func (c *Client) StopCapture(ctx context.Context) error {
	return c.call(ctx, "stop_capture", nil)
}

// State answers a copy of the state now.
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.clone()
}

// WaitFor waits until condition holds on the state, or until ctx ends.
// It checks at once, and again after each change, with no timer.
func (c *Client) WaitFor(ctx context.Context, condition func(State) bool) error {
	for {
		c.mu.Lock()
		done := condition(c.state.clone())
		changed := c.changed
		c.mu.Unlock()
		if done {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}
