// Package indi is a client of the INDI protocol, version 1.7, the XML
// over TCP that indiserver speaks to KStars and to every other client.
// It is written in Go with no cgo, because no maintained Go client
// exists.
//
// A Client follows the repository's rule for events. Run opens the
// connection and sends getProperties. Every def*Vector that answers it
// sets the baseline in the Client's Store, and each set*Vector after
// that updates it. When the connection ends, the store empties, and
// the next Run fills it from a new baseline. Callers read the store,
// subscribe to its changes, or wait for a condition on it, and no part
// of the package reads state again on a timer.
package indi

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// DialTimeout bounds the TCP dial of each Run. The caller's context can
// end the dial sooner.
const DialTimeout = 10 * time.Second

// writeTimeout bounds each write. A server that reads nothing for this
// long is stuck, and the write fails rather than block the caller.
const writeTimeout = 10 * time.Second

var (
	// ErrRunning is the error of a Run while another Run of the same
	// Client has not returned.
	ErrRunning = errors.New("indi: the client is already running")
	// ErrNotConnected is the error of a send while the client has no
	// connection.
	ErrNotConnected = errors.New("indi: no connection to the server")
	// ErrDisconnected is the error of a wait whose connection ended.
	ErrDisconnected = errors.New("indi: the connection ended")
	// ErrNotDefined is the error of a send to a property that the
	// device has not defined.
	ErrNotDefined = errors.New("indi: the property is not defined")
	// ErrDeleted is the error of a wait on a property that the device
	// deleted, as indiserver does for every device of a driver that
	// exits.
	ErrDeleted = errors.New("indi: the property was deleted")
	// ErrInvalid is the error of a send that the property's definition
	// forbids: the wrong type, a read-only property, a member the
	// property does not have, or switches that break the switch rule.
	ErrInvalid = errors.New("indi: the property's definition forbids this change")
)

// Client is a client of one INDI server. Its methods are safe to call
// from several goroutines.
type Client struct {
	address string
	dialer  Dialer

	// mutex guards every field below it. The reader applies each
	// element and publishes its event under the mutex, so a reader of
	// the store never reads half of one element.
	mutex       sync.Mutex
	store       Store
	conn        net.Conn
	running     bool
	subscribers map[*subscriber]struct{}
	// changed is closed and replaced on every change, which wakes each
	// WaitFor to check its condition again.
	changed chan struct{}
	blobs   []blobRequest

	// writing serializes the writes to the connection, so two
	// messages never interleave on the wire.
	writing sync.Mutex
}

// NewClient returns a client of the server at address, such as
// "indiserver.observatory:7624". It opens no connection until Run.
func NewClient(address string, options ...Option) *Client {
	// The dialer's TCP keep-alive, on by default, ends a connection to
	// a server that vanished with no close, so a silent socket cannot
	// hold Run forever. INDI has no heartbeat of its own.
	c := &Client{
		address:     address,
		dialer:      &net.Dialer{Timeout: DialTimeout},
		subscribers: map[*subscriber]struct{}{},
		changed:     make(chan struct{}),
	}
	for _, option := range options {
		option(c)
	}
	return c
}

// Dialer opens the connection of each Run. *net.Dialer is one.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// DialerFunc is a function that is a Dialer.
type DialerFunc func(ctx context.Context, network, address string) (net.Conn, error)

func (f DialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}

// Option changes a Client that NewClient returns.
type Option func(*Client)

// WithDialer makes each Run open its connection through dialer. A test
// passes a dialer that answers with one end of an in-memory pipe, so it
// can run on the fake clock of testing/synctest, which a real socket
// stops.
func WithDialer(dialer Dialer) Option {
	return func(c *Client) { c.dialer = dialer }
}

// Run dials the server, sends getProperties, and reads the server's
// messages into the store until the connection fails or ctx ends. It
// always returns an error: the reason the connection ended, or the
// error of ctx.
//
// Run opens one connection, and the caller decides when to open the
// next. An operator can open it again when the server's Service has a
// ready endpoint again, instead of on a timer. Each new connection
// empties the store, and the server's new baseline fills it. Every
// subscriber receives Connected when a connection opens and
// Disconnected when it ends.
func (c *Client) Run(ctx context.Context) error {
	c.mutex.Lock()
	if c.running {
		c.mutex.Unlock()
		return ErrRunning
	}
	c.running = true
	c.mutex.Unlock()
	defer func() {
		c.mutex.Lock()
		c.running = false
		c.mutex.Unlock()
	}()

	conn, err := c.dialer.DialContext(ctx, "tcp", c.address)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	c.mutex.Lock()
	c.conn = conn
	c.store.reset(true)
	c.publish(Event{Kind: Connected})
	blobs := append([]blobRequest(nil), c.blobs...)
	c.mutex.Unlock()

	err = c.read(conn, blobs)
	conn.Close()
	if ctx.Err() != nil {
		err = ctx.Err()
	}

	c.mutex.Lock()
	c.conn = nil
	c.store.reset(false)
	c.publish(Event{Kind: Disconnected, Err: err})
	c.mutex.Unlock()
	return err
}

// read sends the client's first messages, then applies each element
// that the server sends until the stream ends.
func (c *Client) read(conn net.Conn, blobs []blobRequest) error {
	// getProperties goes first. indiserver reads an enableBLOB that
	// names a device before any getProperties as a request for that
	// device alone, and sends the client nothing from the others
	// (ClInfo::crackBLOBHandling and ClInfo::findDevice).
	if err := c.writeTo(conn, getProperties()); err != nil {
		return err
	}
	for _, r := range blobs {
		if err := c.writeTo(conn, enableBLOB(r)); err != nil {
			return err
		}
	}
	decoder := xml.NewDecoder(&validUTF8{from: bufio.NewReaderSize(conn, 64*1024)})
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return fmt.Errorf("indi: the server closed the connection: %w", err)
			}
			return err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if err := c.receive(conn, decoder, start); err != nil {
			return err
		}
	}
}

// receive reads one top-level element and applies it.
func (c *Client) receive(conn net.Conn, decoder *xml.Decoder, start xml.StartElement) error {
	tag := start.Name.Local
	t, isVector := vectorType(tag)
	switch {
	case isVector, tag == "delProperty", tag == "message", tag == "pingRequest":
	default:
		// Every other element, such as another client's new*Vector, is
		// not for this client.
		return decoder.Skip()
	}
	var e element
	if err := decoder.DecodeElement(&e, &start); err != nil {
		return err
	}
	if tag == "pingRequest" {
		return c.writeTo(conn, pingReply(e.UID))
	}
	if tag == "delProperty" && !hasAttribute(start, "name") {
		e.wholeDevice = true
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if event, ok := c.apply(t, e); ok {
		c.publish(event)
	}
	return nil
}

// apply changes the store for one element and returns the event to
// publish. It returns false for an update of a property that the store
// does not hold, which the protocol tells a client to ignore. The
// simulators send such updates: the telescope simulator updates
// TELESCOPE_MOUNT_TYPE before it defines it.
func (c *Client) apply(t Type, e element) (Event, bool) {
	event := Event{Device: e.Device, Property: e.Name, Message: e.Message}
	event.Timestamp, _ = parseTimestamp(e.Timestamp)
	invalid := func(err error) (Event, bool) {
		event.Kind, event.Err = Invalid, err
		return event, true
	}
	tag := e.XMLName.Local
	switch {
	case tag == "message":
		event.Kind, event.Property = Message, ""
	case tag == "delProperty" && e.wholeDevice:
		c.store.removeDevice(e.Device)
		event.Kind = Deleted
	case tag == "delProperty":
		c.store.remove(e.Device, e.Name)
		event.Kind = Deleted
	case tag[:3] == "def":
		p, err := e.definition(t)
		if err != nil {
			return invalid(err)
		}
		c.store.define(p)
		event.Kind = Defined
	default:
		entry := c.store.entry(e.Device, e.Name)
		if entry == nil {
			return Event{}, false
		}
		if entry.property.Type != t {
			return invalid(fmt.Errorf("%s for %s.%s, a %s property", tag, e.Device, e.Name, entry.property.Type))
		}
		p, blobs, err := e.updated(entry.property, c.wantsBLOBs(e.Device, e.Name))
		if err != nil {
			return invalid(err)
		}
		c.store.update(entry, p)
		event.Kind, event.BLOBs = Updated, blobs
	}
	return event, true
}

// hasAttribute reports whether an element carries an attribute, even
// an empty one. A delProperty with no name deletes the whole device,
// and one with an empty name deletes a property named "", which no
// device defines. The receiver simulator sends the second on
// disconnect, and libindi's client deletes nothing for it.
func hasAttribute(start xml.StartElement, name string) bool {
	for _, a := range start.Attr {
		if a.Name.Local == name {
			return true
		}
	}
	return false
}

// WaitFor waits until condition holds on the store, or until ctx ends.
// It checks the condition at once, and again after each change, with
// no timer. The condition runs while the client holds its lock, so it
// must only read the store it receives, and must not call the Client.
func (c *Client) WaitFor(ctx context.Context, condition func(*Store) bool) error {
	return c.waitFor(ctx, func(s *Store) (bool, error) { return condition(s), nil })
}

// waitFor waits until check reports done, or fails.
func (c *Client) waitFor(ctx context.Context, check func(*Store) (bool, error)) error {
	for {
		c.mutex.Lock()
		done, err := check(&c.store)
		changed := c.changed
		c.mutex.Unlock()
		if done || err != nil {
			return err
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Connected reports whether the client has a connection to the server.
func (c *Client) Connected() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.store.Connected()
}

// Devices lists the devices that have at least one property, sorted by
// name.
func (c *Client) Devices() []string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.store.Devices()
}

// Properties lists a device's properties in the order the driver first
// defined them.
func (c *Client) Properties(device string) []Property {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.store.Properties(device)
}

// Property returns one property of one device.
func (c *Client) Property(device, name string) (Property, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.store.Property(device, name)
}

// write sends one message on the current connection.
func (c *Client) write(message []byte) error {
	c.mutex.Lock()
	conn := c.conn
	c.mutex.Unlock()
	if conn == nil {
		return ErrNotConnected
	}
	return c.writeTo(conn, message)
}

func (c *Client) writeTo(conn net.Conn, message []byte) error {
	c.writing.Lock()
	defer c.writing.Unlock()
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	_, err := conn.Write(message)
	return err
}
