package main

// The bus client is one live TCP connection to the broker, with a
// reader, a single writer, and a keepalive timer, and it reconnects
// with backoff whenever any of the three fails. Every mode of this
// operator that speaks to the broker holds one Bus and lets it manage
// the connection, so no caller writes the socket directly.
//
// All writes go through one goroutine and one channel, because two
// goroutines writing one TCP connection would interleave their bytes
// and corrupt a packet. A publish or a subscribe from any goroutine
// enqueues a finished frame, and the writer sends it.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"time"
)

// The keepalive the client asks the broker for, and the queue the
// writer drains. The client sends a PINGREQ once the connection has
// been idle longer than half the keepalive, so the broker never
// reaches the keepalive without hearing from the client. The queue is
// bounded. Publish drops a frame that would overflow it, which is
// correct at QoS 0 for a caller that publishes again on a later pass,
// and PublishWait waits for room, for a caller that publishes once.
//
// A frame in the queue is lost when the session ends before the writer
// sends it, so a longer queue loses more frames at each disconnect. A
// caller that sends a burst waits for room instead.
const (
	busKeepalive  = 30
	busQueueDepth = 64
)

// The reconnect backoff bounds. The client waits busMinBackoff after
// the first failure and doubles the wait up to busMaxBackoff, so a
// broker that is down does not become a tight reconnect loop.
const (
	busMinBackoff = time.Second
	busMaxBackoff = 30 * time.Second
)

// busHandler receives one inbound message's topic and payload. The Bus
// calls it on the reader goroutine, so a handler that blocks holds up
// every later message on the connection.
type busHandler func(topic string, payload []byte)

// busWill is the MQTT Last Will the client names at connect time. The
// broker publishes it on any disconnect the client does not make
// cleanly, which is how a killed pod's status is marked offline.
type busWill struct {
	Topic    string
	Payload  []byte
	Retained bool
}

// Bus holds the connection's parts and the state that outlives one
// connection: the remembered subscriptions and the packet identifier
// counter. out is the current connection's write queue, or nil while
// disconnected, and ended closes when that connection's session ends.
// The mutex guards both and the fields around them.
type Bus struct {
	clientID  string
	will      *busWill
	onConnect func(*Bus)
	handler   busHandler
	dial      func(context.Context) (net.Conn, error)

	mutex    sync.Mutex
	filters  map[string]struct{}
	out      chan []byte
	ended    <-chan struct{}
	packetID uint16
}

// newBus builds a client that dials the address over TCP. The address,
// the client identifier, the will, the connect callback, and the
// inbound handler are fixed for the client's life; the connection they
// drive is not.
func newBus(address, clientID string, will *busWill, onConnect func(*Bus), handler busHandler) *Bus {
	bus := &Bus{
		clientID:  clientID,
		will:      will,
		onConnect: onConnect,
		handler:   handler,
		filters:   map[string]struct{}{},
	}
	bus.dial = func(ctx context.Context) (net.Conn, error) {
		dialer := &net.Dialer{}
		return dialer.DialContext(ctx, "tcp", address)
	}
	return bus
}

// Run holds the connection open until ctx ends. It dials, connects,
// and serves one session, then waits a backoff and dials again. A
// session that reached a CONNACK resets the backoff to its floor, so a
// connection that drops after an hour reconnects at once, while a
// broker that never answers is retried ever more slowly.
func (b *Bus) Run(ctx context.Context) {
	backoff := busMinBackoff
	for ctx.Err() == nil {
		connected := b.runSession(ctx)
		if ctx.Err() != nil {
			return
		}
		if connected {
			backoff = busMinBackoff
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if !connected {
			backoff *= 2
			if backoff > busMaxBackoff {
				backoff = busMaxBackoff
			}
		}
	}
}

// runSession dials, completes the CONNECT handshake, and serves the
// connection until its reader or writer fails. It returns whether the
// handshake reached a CONNACK, which is what tells Run to reset the
// backoff.
func (b *Bus) runSession(parent context.Context) (connected bool) {
	conn, err := b.dial(parent)
	if err != nil {
		return false
	}
	defer conn.Close()

	connect, err := encodeConnect(b.clientID, busKeepalive, b.will)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connecting to the broker: %v\n", err)
		return false
	}
	if _, err := conn.Write(connect); err != nil {
		return false
	}
	reader := bufio.NewReader(conn)
	first, body, err := readPacket(reader)
	if err != nil || first&0xF0 != mqttConnack {
		return false
	}
	if err := parseConnack(body); err != nil {
		return false
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// The reader blocks in Read until the broker writes or the
	// connection closes. Closing the connection when the session ends
	// is what unblocks a reader waiting on a silent broker.
	defer context.AfterFunc(ctx, func() { conn.Close() })()

	out := make(chan []byte, busQueueDepth)
	b.mutex.Lock()
	b.out, b.ended = out, ctx.Done()
	b.mutex.Unlock()
	defer func() {
		b.mutex.Lock()
		b.out, b.ended = nil, nil
		b.mutex.Unlock()
	}()

	var writing sync.WaitGroup
	writing.Add(1)
	go func() {
		defer writing.Done()
		// A write failure ends the session, so the reader stops too.
		defer cancel()
		b.writeLoop(ctx, conn, out)
	}()

	// The remembered subscriptions go out first, so the broker is
	// delivering again before onConnect re-publishes any retained
	// state.
	b.resubscribe(out)
	if b.onConnect != nil {
		b.onConnect(b)
	}

	b.readLoop(reader)
	cancel()
	writing.Wait()
	return true
}

// writeLoop is the one goroutine that writes the connection. It sends
// each queued frame and, when no frame has gone out for half the
// keepalive, sends a PINGREQ so the broker hears from the client
// before the keepalive elapses.
func (b *Bus) writeLoop(ctx context.Context, conn net.Conn, out <-chan []byte) {
	idle := time.Duration(busKeepalive) * time.Second / 2
	ticker := time.NewTicker(idle)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-out:
			if _, err := conn.Write(frame); err != nil {
				return
			}
			last = time.Now()
		case <-ticker.C:
			if time.Since(last) >= idle {
				if _, err := conn.Write(encodePingreq()); err != nil {
					return
				}
				last = time.Now()
			}
		}
	}
}

// readLoop reads whole packets and delivers each inbound PUBLISH to the
// handler. Any read error ends the loop and the session.
//
// A PINGRESP is read and dropped, because it only proves the broker is
// alive, and a successful read already shows that. A SUBACK is checked: a
// refused subscription is a reader that receives nothing for the life of
// the session, and the line in the pod log is the only sign of it.
func (b *Bus) readLoop(reader *bufio.Reader) {
	for {
		first, body, err := readPacket(reader)
		if err != nil {
			return
		}
		switch first & 0xF0 {
		case mqttPublish:
			topic, payload, ok := parsePublish(body)
			if ok && b.handler != nil {
				b.handler(topic, payload)
			}
		case mqttSuback:
			if err := parseSuback(body); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
		}
	}
}

// Publish enqueues a QoS 0 PUBLISH from any goroutine. While the client
// is disconnected the queue does not exist and the publish is dropped,
// which is correct at QoS 0: a caller re-publishes its retained state
// from onConnect, so the broker holds the current value again within
// the reconnect. A message too long for one packet is refused with a
// line in the pod log, because the broker would close the connection
// on the malformed packet and drop every publish queued behind it.
//
// The answer is whether the frame reached the queue. A caller that skips
// a payload it already published records the payload only on true, so a
// frame dropped on a full queue goes out again on its next try.
func (b *Bus) Publish(topic string, payload []byte, retained bool) bool {
	b.mutex.Lock()
	out := b.out
	b.mutex.Unlock()
	if out == nil {
		return false
	}
	frame, err := encodePublish(topic, payload, retained)
	if err != nil {
		fmt.Fprintf(os.Stderr, "publishing to %s: %v\n", topic, err)
		return false
	}
	select {
	case out <- frame:
		return true
	default:
		return false
	}
}

// errBusDisconnected is the answer of PublishWait while the client has
// no session with the broker, and when the session ends during the wait.
var errBusDisconnected = errors.New("the bus has no connection to the broker")

// PublishWait enqueues a QoS 0 PUBLISH and waits for room in the write
// queue when the queue is full. It is for a caller that publishes a
// message once and has no later pass that publishes it again: a play
// that is not retained, or the clear of a topic whose object is about to
// go. Publish would drop such a message on a full queue, and nothing
// would send it again.
//
// The wait ends with an error when ctx ends, and at once when the client
// has no session or the session ends, because the queue of a session
// that ended is never written. A nil answer says that the frame is in
// the queue of a live session. The frame is still lost if that session
// ends before the writer sends it.
func (b *Bus) PublishWait(ctx context.Context, topic string, payload []byte, retained bool) error {
	b.mutex.Lock()
	out, ended := b.out, b.ended
	b.mutex.Unlock()
	if out == nil {
		return errBusDisconnected
	}
	frame, err := encodePublish(topic, payload, retained)
	if err != nil {
		return fmt.Errorf("publishing to %s: %w", topic, err)
	}
	select {
	case out <- frame:
		return nil
	case <-ended:
		return errBusDisconnected
	case <-ctx.Done():
		return fmt.Errorf("the bus's write queue stayed full: %w", ctx.Err())
	}
}

// Session answers with a channel that closes when the current session
// ends, or with a closed channel while the client has no session. A
// caller that must send a whole run on one session reads it before the
// run and after it.
func (b *Bus) Session() <-chan struct{} {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.ended == nil {
		return closedSession
	}
	return b.ended
}

// closedSession is the answer of Session while the client has no session.
var closedSession = func() <-chan struct{} {
	closed := make(chan struct{})
	close(closed)
	return closed
}()

// Subscribe remembers the filter and sends it if the client is
// connected. The remembered set is what runSession re-sends on every
// reconnect, so a subscription outlives the connection it was made on.
// A filter too long for one packet is refused with a line in the pod
// log and is not remembered, so every remembered filter encodes.
func (b *Bus) Subscribe(filter string) {
	b.mutex.Lock()
	frame, err := encodeSubscribe(b.nextPacketID(), filter)
	if err != nil {
		b.mutex.Unlock()
		fmt.Fprintf(os.Stderr, "subscribing to %s: %v\n", filter, err)
		return
	}
	b.filters[filter] = struct{}{}
	out := b.out
	b.mutex.Unlock()
	if out == nil {
		return
	}
	select {
	case out <- frame:
	default:
	}
}

// resubscribe sends every remembered filter on a fresh connection. The
// filters go out in sorted order so the frames a reconnect writes are
// the same every time, which keeps a test deterministic.
func (b *Bus) resubscribe(out chan<- []byte) {
	b.mutex.Lock()
	filters := make([]string, 0, len(b.filters))
	for filter := range b.filters {
		filters = append(filters, filter)
	}
	frames := make([][]byte, 0, len(filters))
	sort.Strings(filters)
	for _, filter := range filters {
		// Subscribe remembers only a filter that encodes, so this
		// refuses none in practice.
		frame, err := encodeSubscribe(b.nextPacketID(), filter)
		if err != nil {
			continue
		}
		frames = append(frames, frame)
	}
	b.mutex.Unlock()
	for _, frame := range frames {
		select {
		case out <- frame:
		default:
		}
	}
}

// nextPacketID hands out the identifier a SUBSCRIBE carries and its
// SUBACK echoes. It skips zero, which the protocol reserves. The caller
// holds the mutex.
func (b *Bus) nextPacketID() uint16 {
	b.packetID++
	if b.packetID == 0 {
		b.packetID = 1
	}
	return b.packetID
}
