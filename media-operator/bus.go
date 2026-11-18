package main

// Bus is an MQTT 3.1.1 client for one broker. It keeps one TCP
// connection open and reconnects with backoff when the connection fails.
//
// Only the writer goroutine writes the connection, because two
// goroutines writing one connection would interleave their bytes and
// corrupt a packet. Publish and Subscribe encode a complete frame and
// put it on a channel, and the writer sends it.

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"time"
)

// busKeepalive is the keepalive interval in seconds that the client
// sends in CONNECT. The writer sends a PINGREQ when nothing has been
// written for half of it, so the broker hears from the client within
// the interval. When the write queue already holds busQueueDepth
// frames, a new frame is dropped, which QoS 0 permits.
const (
	busKeepalive  = 30
	busQueueDepth = 64
)

const (
	busMinBackoff = time.Second
	busMaxBackoff = 30 * time.Second
)

// busHandler receives the topic and payload of one inbound message. The
// Bus calls it on the reader goroutine, so a handler that blocks delays
// every later message on the connection.
type busHandler func(topic string, payload []byte)

// busWill is the MQTT Last Will that the client sends in CONNECT. The
// broker publishes it when the connection ends without a DISCONNECT, so
// a killed pod's availability topic reads offline.
type busWill struct {
	Topic    string
	Payload  []byte
	Retained bool
}

// Bus keeps filters and packetID across reconnects. out is the write
// queue of the open session, and nil while the client is disconnected.
// mutex guards filters, out, and packetID.
type Bus struct {
	clientID  string
	will      *busWill
	onConnect func(*Bus)
	handler   busHandler
	dial      func(context.Context) (net.Conn, error)

	// retainedHandler, when set, receives the messages that have the
	// retain flag, and handler receives the rest. The broker sets the
	// flag only on a stored message that it sends because of a new
	// subscription (MQTT 3.1.1, 3.3.1.3), so the flag separates the state
	// before the subscription from the changes after it. Set it before
	// Run.
	retainedHandler busHandler

	mutex    sync.Mutex
	filters  map[string]struct{}
	out      chan []byte
	packetID uint16
}

// newBus returns a client that dials address over TCP. Run starts it.
// onConnect, when set, runs after each CONNACK and after the stored
// subscriptions are sent.
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

// Run keeps a session open until ctx ends. When a session ends, it
// waits and dials again. After a session that got a CONNACK it waits
// busMinBackoff, so a connection that drops after an hour reconnects
// quickly. After each attempt that got no CONNACK the wait doubles, up
// to busMaxBackoff, so a broker that is down does not get a tight
// reconnect loop.
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

// runSession dials, sends CONNECT, and serves the connection until the
// reader or the writer fails or ctx ends. It returns true when the broker
// sent a CONNACK.
func (b *Bus) runSession(parent context.Context) (connected bool) {
	conn, err := b.dial(parent)
	if err != nil {
		return false
	}
	defer conn.Close()

	connect, err := encodeConnect(b.clientID, busKeepalive, b.will)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding the CONNECT packet: %v\n", err)
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
	// Closing the connection makes the blocked Read in readLoop return,
	// so a silent broker cannot keep the session open after ctx ends.
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
		// When the writer fails, cancel closes the connection, which
		// ends readLoop.
		defer cancel()
		b.writeLoop(ctx, conn, out)
	}()

	// The SUBSCRIBE frames go on the queue before anything that
	// onConnect publishes, so the broker receives them first.
	b.resubscribe(out)
	if b.onConnect != nil {
		b.onConnect(b)
	}

	b.readLoop(reader)
	cancel()
	writing.Wait()
	return true
}

// writeLoop is the only writer of conn. It sends each queued frame and
// the PINGREQ of the keepalive.
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

// readLoop passes each inbound PUBLISH to retainedHandler or handler
// until a read fails. It ignores every other packet, because a client
// that subscribes at QoS 0 has nothing to do with a SUBACK or a
// PINGRESP.
func (b *Bus) readLoop(reader *bufio.Reader) {
	for {
		first, body, err := readPacket(reader)
		if err != nil {
			return
		}
		if first&0xF0 == mqttPublish {
			topic, payload, ok := parsePublish(body)
			switch {
			case !ok:
			case first&mqttRetainFlag != 0 && b.retainedHandler != nil:
				b.retainedHandler(topic, payload)
			case b.handler != nil:
				b.handler(topic, payload)
			}
		}
	}
}

// Publish queues a QoS 0 PUBLISH and is safe to call from any
// goroutine. It returns true when the frame reached the queue, and
// false when the client is disconnected or the queue is full. A caller
// that skips payloads it already published records a payload only
// after true, so a dropped frame is sent on the next try, and it
// publishes its retained state again from onConnect.
//
// A message too long for one packet is refused with a line on stderr,
// because the broker would close the connection on the malformed packet
// and every frame queued behind it would be lost.
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

// Connected reports whether a session is open. A caller whose publish
// is the only record of an action, such as clearing a retained topic,
// checks it first and retries on a later pass while it is false.
func (b *Bus) Connected() bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.out != nil
}

// Subscribe stores the filter and queues a SUBSCRIBE when a session is
// open. Each new session sends every stored filter, so a SUBSCRIBE
// dropped on a full queue or a closed connection is sent at the next
// reconnect. A filter too long for one packet is refused with a line on
// stderr and is not stored.
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

// resubscribe queues a SUBSCRIBE for every stored filter, in sorted
// order, so every reconnect sends the same frames.
func (b *Bus) resubscribe(out chan<- []byte) {
	b.mutex.Lock()
	filters := make([]string, 0, len(b.filters))
	for filter := range b.filters {
		filters = append(filters, filter)
	}
	frames := make([][]byte, 0, len(filters))
	sort.Strings(filters)
	for _, filter := range filters {
		// Subscribe stores only filters that encode.
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

// nextPacketID returns the packet identifier for a SUBSCRIBE. MQTT
// reserves zero, so the counter wraps to 1. The caller holds the mutex.
func (b *Bus) nextPacketID() uint16 {
	b.packetID++
	if b.packetID == 0 {
		b.packetID = 1
	}
	return b.packetID
}
