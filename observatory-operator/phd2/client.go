// Package phd2 is a client of PHD2's event server, the API that KStars
// and astrophotography-operator use to control PHD2. The server listens
// on port 4400 for PHD2's first instance and speaks JSON-RPC 2.0 over
// TCP. Each message is one line of JSON that ends in CR LF. PHD2 sends
// events on the same connection as the responses to requests, in the
// order that they happen. The protocol is documented only on PHD2's
// wiki, at https://github.com/OpenPHDGuiding/phd2/wiki/EventMonitoring,
// and where the wiki says nothing the client follows
// src/event_server.cpp.
//
// A Client keeps its state current from events, without polling. Run
// opens the connection first, so no event is missed. Then it reads the
// app state, the calibration, the equipment, and the pixel scale once,
// to set the baseline. Events update the state after that. When the
// connection ends, the state is emptied, and the next Run reads a new
// baseline.
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

// Port is the event server's port for PHD2's first instance. Instance
// n listens on 4400 + n - 1.
const Port = 4400

// DialTimeout is the timeout of the default dialer.
const DialTimeout = 10 * time.Second

// writeTimeout is the longest that one write waits. A PHD2 that accepts
// no data for this long is stuck, and the write fails instead of
// blocking the caller.
const writeTimeout = 10 * time.Second

// maxLine is the longest message that the client reads. The largest
// response PHD2 sends is for get_star_image, which this client never
// requests, and events are a few hundred bytes.
const maxLine = 1 << 20

var (
	// ErrRunning is returned by a Run that starts while another Run of
	// the same Client has not returned.
	ErrRunning = errors.New("phd2: the client is already running")
	// ErrNotConnected is returned by a request that the client makes
	// while it has no connection.
	ErrNotConnected = errors.New("phd2: no connection to PHD2")
	// ErrDisconnected is returned by a request whose connection ended
	// before PHD2 responded. Run returns it when PHD2 closes the
	// connection.
	ErrDisconnected = errors.New("phd2: the connection ended")
)

// Dialer opens the connection for each Run. *net.Dialer implements it.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Option configures a Client in NewClient.
type Option func(*Client)

// WithDialer makes each Run open its connection through dialer. A test
// passes a dialer that returns one end of an in-memory pipe, so the
// client runs on the fake clock of testing/synctest, which a real
// socket would stop.
func WithDialer(dialer Dialer) Option { return func(c *Client) { c.dialer = dialer } }

// WithNotify calls notify with a copy of the state after each change.
// The Client holds its lock while it calls notify, and Run reads no
// message until notify returns, so notify must not block and must not
// call a method of the Client.
func WithNotify(notify func(State)) Option { return func(c *Client) { c.notify = notify } }

// Client is a client of one PHD2. Its methods are safe to call from
// several goroutines.
type Client struct {
	address string
	dialer  Dialer
	notify  func(State)

	// mu guards every field from conn to reread. The reader goroutine
	// applies each message while it holds mu, so State never returns a
	// state that is half updated by a message.
	mu      sync.Mutex
	conn    net.Conn
	running bool
	state   State
	rms     window
	nextID  int
	pending map[int]*request
	// changed is closed and replaced on every change, which wakes each
	// WaitFor so that it checks its condition again.
	changed chan struct{}
	// reread is the set of reads that an event requested and that refresh
	// has not sent yet. refresh waits on wake for new entries.
	reread map[string]bool
	wake   chan struct{}

	// writing makes writes to the connection one at a time, so two
	// messages never interleave.
	writing sync.Mutex
}

// request is a request that PHD2 has not responded to yet. done is nil
// for a read, because the reader applies a read's result to the state
// and no caller waits for it.
type request struct {
	method string
	done   chan reply
}

type reply struct {
	result json.RawMessage
	err    error
}

// NewClient returns a client of the PHD2 at address, such as
// "east-guider.observatory.svc:4400". It opens no connection until you
// call Run.
func NewClient(address string, options ...Option) *Client {
	c := &Client{
		address: address,
		dialer:  &net.Dialer{Timeout: DialTimeout},
		notify:  func(State) {},
		changed: make(chan struct{}),
		reread:  map[string]bool{},
		wake:    make(chan struct{}, 1),
	}
	for _, option := range options {
		option(c)
	}
	return c
}

// Run connects to PHD2, reads the baseline, and applies each message
// until the connection fails or ctx ends. It always returns an error:
// the reason the connection ended, or the error of ctx. Run does not
// reconnect, so the caller calls it again to open a new connection.
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
			r.done <- reply{err: ErrDisconnected}
		}
	}
	c.pending = nil
	c.changeLocked()
	c.mu.Unlock()
	return err
}

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

// The methods that read state. Run sends each of them for the baseline.
// After an event that can change one of these values, the client sends
// its read again, because the event does not include the new value.
const (
	methodAppState   = "get_app_state"
	methodCalibrated = "get_calibrated"
	methodConnected  = "get_connected"
	methodPixelScale = "get_pixel_scale"
)

// refresh sends the reads queued in reread. It runs on its own
// goroutine because the reader must not write. While PHD2 is blocked
// writing an event to the client, it reads nothing, so a write from the
// reader could block, and then nothing would read that event.
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

// rereadLocked queues reads for refresh to send. The caller holds mu.
func (c *Client) rereadLocked(methods ...string) {
	for _, m := range methods {
		c.reread[m] = true
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// changeLocked wakes each WaitFor and calls notify. The caller holds
// mu.
func (c *Client) changeLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
	c.notify(c.state.clone())
}

// send writes one request, and the reader sends the response on done.
// With a nil done, the reader applies the result to the state. PHD2
// accepts params as a list in the order of the method's parameters, or
// as an object by parameter name.
func (c *Client) send(method string, params any, done chan reply) error {
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

// call sends one request and waits for its response or for ctx.
func (c *Client) call(ctx context.Context, method string, params any) error {
	_, err := c.callResult(ctx, method, params)
	return err
}

func (c *Client) callResult(ctx context.Context, method string, params any) (json.RawMessage, error) {
	done := make(chan reply, 1)
	if err := c.send(method, params, done); err != nil {
		return nil, err
	}
	select {
	case r := <-done:
		return r.result, r.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// SetConnected connects or disconnects the camera and the mount in
// PHD2's profile, and then reads the equipment state again. PHD2
// responds after every connection has finished or failed. That takes
// seconds on real hardware, so ctx limits the wait.
func (c *Client) SetConnected(ctx context.Context, connected bool) error {
	if err := c.call(ctx, "set_connected", []any{connected}); err != nil {
		return err
	}
	return c.call(ctx, methodConnected, nil)
}

// StopCapture stops looping and guiding. PHD2 responds at once, and
// the events that follow show the state it stopped in.
func (c *Client) StopCapture(ctx context.Context) error {
	return c.call(ctx, "stop_capture", nil)
}

// State returns a copy of the current state.
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.clone()
}

// WaitFor waits until condition returns true for the state, or until
// ctx ends. It checks at once and after each change.
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
