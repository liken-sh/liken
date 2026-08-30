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
	"net"
	"sort"
	"sync"
	"time"
)

// The keepalive the client asks the broker for, and the queue the
// writer drains. The client sends a PINGREQ once the connection has
// been idle longer than half the keepalive, so the broker never
// reaches the keepalive without hearing from the client. The queue is
// bounded, and a publish that would overflow it is dropped, which is
// correct at QoS 0.
const (
	busKeepalive  = 30
	busQueueDepth = 64
)

// The reconnect backoff bounds. The client waits busMinBackoff after
// the first failure and doubles the wait up to busMaxBackoff, so a
// broker that is down does not become a tight reconnect loop. Both are
// variables so a test drives a reconnect in milliseconds.
var (
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
// disconnected, and the mutex guards both it and the fields around it.
type Bus struct {
	clientID  string
	will      *busWill
	onConnect func(*Bus)
	handler   busHandler
	dial      func(context.Context) (net.Conn, error)

	mutex    sync.Mutex
	filters  map[string]struct{}
	out      chan []byte
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

	if _, err := conn.Write(encodeConnect(b.clientID, busKeepalive, b.will)); err != nil {
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
	b.out = out
	b.mutex.Unlock()
	defer func() {
		b.mutex.Lock()
		b.out = nil
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
// handler. A SUBACK and a PINGRESP are read and dropped: the client
// asks for QoS 0, so a SUBACK carries nothing to act on, and a
// PINGRESP only proves the broker is alive, which a successful read
// already shows. Any read error ends the loop and the session.
func (b *Bus) readLoop(reader *bufio.Reader) {
	for {
		first, body, err := readPacket(reader)
		if err != nil {
			return
		}
		if first&0xF0 == mqttPublish {
			topic, payload, ok := parsePublish(body)
			if ok && b.handler != nil {
				b.handler(topic, payload)
			}
		}
	}
}

// Publish enqueues a QoS 0 PUBLISH from any goroutine. While the client
// is disconnected the queue does not exist and the publish is dropped,
// which is correct at QoS 0: a caller re-publishes its retained state
// from onConnect, so the broker holds the current value again within
// the reconnect.
func (b *Bus) Publish(topic string, payload []byte, retained bool) {
	b.mutex.Lock()
	out := b.out
	b.mutex.Unlock()
	if out == nil {
		return
	}
	select {
	case out <- encodePublish(topic, payload, retained):
	default:
	}
}

// Subscribe remembers the filter and sends it if the client is
// connected. The remembered set is what runSession re-sends on every
// reconnect, so a subscription outlives the connection it was made on.
func (b *Bus) Subscribe(filter string) {
	b.mutex.Lock()
	b.filters[filter] = struct{}{}
	out := b.out
	frame := encodeSubscribe(b.nextPacketID(), filter)
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
		frames = append(frames, encodeSubscribe(b.nextPacketID(), filter))
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
