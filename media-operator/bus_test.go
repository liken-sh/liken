package main

// These tests run the client against a fake broker on the far end of a
// net.Pipe, so the connect handshake, the subscription resend, the
// publish path, and the inbound delivery are proved with no TCP and no
// Mosquitto.

import (
	"bufio"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// busTestTimeout bounds every wait: long enough that a loaded machine
// still passes, short enough that a broken client fails in seconds.
const busTestTimeout = 2 * time.Second

// A brokerPublish is one message the broker read from the client.
type brokerPublish struct {
	topic    string
	payload  []byte
	retained bool
}

// fakeBroker is the far end of a pipe: it completes the CONNECT
// handshake, answers each SUBSCRIBE with a SUBACK, records every
// PUBLISH, and can push a PUBLISH back at the client the way Mosquitto
// pushes a retained message to a new subscriber.
type fakeBroker struct {
	conn   net.Conn
	reader *bufio.Reader
	subs   chan string
	pubs   chan brokerPublish
}

func newFakeBroker(conn net.Conn) *fakeBroker {
	broker := &fakeBroker{
		conn:   conn,
		reader: bufio.NewReader(conn),
		subs:   make(chan string, 8),
		pubs:   make(chan brokerPublish, 8),
	}
	go broker.serve()
	return broker
}

func (b *fakeBroker) serve() {
	first, _, err := readPacket(b.reader)
	if err != nil || first&0xF0 != mqttConnect {
		return
	}
	// CONNACK: acknowledge flags 0x00 and return code 0x00.
	if _, err := b.conn.Write([]byte{mqttConnack, 0x02, 0x00, 0x00}); err != nil {
		return
	}
	for {
		first, body, err := readPacket(b.reader)
		if err != nil {
			return
		}
		switch first & 0xF0 {
		case mqttSubscribe:
			packetID := body[0:2]
			_, filter, _ := readTopicFilter(body[2:])
			// SUBACK: the echoed packet identifier and one granted QoS.
			b.conn.Write([]byte{mqttSuback, 0x03, packetID[0], packetID[1], 0x00})
			b.subs <- filter
		case mqttPublish:
			topic, payload, ok := parsePublish(body)
			if ok {
				b.pubs <- brokerPublish{topic: topic, payload: payload, retained: first&0x01 != 0}
			}
		case mqttPingreq:
			b.conn.Write([]byte{mqttPingresp, 0x00})
		}
	}
}

// push sends the client a retained message, the way the broker
// delivers a topic's last value to a fresh subscriber.
func (b *fakeBroker) push(topic string, payload []byte) {
	b.conn.Write(encodePublish(topic, payload, true))
}

// readTopicFilter reads the length-prefixed filter a SUBSCRIBE carries.
func readTopicFilter(body []byte) (int, string, bool) {
	if len(body) < 2 {
		return 0, "", false
	}
	length := int(body[0])<<8 | int(body[1])
	if len(body) < 2+length {
		return 0, "", false
	}
	return 2 + length, string(body[2 : 2+length]), true
}

// startBus wires a client to a dialer that hands out the pipe ends in
// order and blocks once they run out, so a client that reconnects past
// the scripted connections waits instead of spinning. Run stops when
// the test's context ends.
func startBus(t *testing.T, count int, will *busWill, handler busHandler) (*Bus, []*fakeBroker, <-chan *Bus) {
	t.Helper()
	shorterBackoff(t)

	conns := make(chan net.Conn, count)
	brokers := make([]*fakeBroker, count)
	for index := range brokers {
		near, far := net.Pipe()
		conns <- near
		brokers[index] = newFakeBroker(far)
		t.Cleanup(func() {
			near.Close()
			far.Close()
		})
	}

	connected := make(chan *Bus, count)
	bus := newBus("pipe", "media-operator", will, func(b *Bus) { connected <- b }, handler)
	bus.dial = func(ctx context.Context) (net.Conn, error) {
		select {
		case conn := <-conns:
			return conn, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go bus.Run(ctx)
	return bus, brokers, connected
}

func shorterBackoff(t *testing.T) {
	t.Helper()
	minWas, maxWas := busMinBackoff, busMaxBackoff
	t.Cleanup(func() { busMinBackoff, busMaxBackoff = minWas, maxWas })
	busMinBackoff, busMaxBackoff = 5*time.Millisecond, 20*time.Millisecond
}

func waitForConnect(t *testing.T, connected <-chan *Bus) {
	t.Helper()
	select {
	case <-connected:
	case <-time.After(busTestTimeout):
		t.Fatal("the client never connected")
	}
}

func waitForString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(busTestTimeout):
		t.Fatal("nothing arrived on the channel")
		return ""
	}
}

func waitForPublish(t *testing.T, values <-chan brokerPublish) brokerPublish {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(busTestTimeout):
		t.Fatal("no publish reached the broker")
		return brokerPublish{}
	}
}

// The client connects, calls onConnect, and re-sends every remembered
// subscription on that connection.
func TestBusConnectsAndSendsRememberedSubscriptions(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	bus.Subscribe("liken/media/plays/+/+/status")

	waitForConnect(t, connected)
	if got := waitForString(t, brokers[0].subs); got != "liken/media/plays/+/+/status" {
		t.Errorf("subscription = %q", got)
	}
}

// A publish from the caller reaches the broker with its topic, its
// payload, and its retain flag.
func TestBusPublishesToTheBroker(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)

	bus.Publish("liken/media/plays/house/movie/status", []byte(`{"item":1}`), true)

	got := waitForPublish(t, brokers[0].pubs)
	if got.topic != "liken/media/plays/house/movie/status" {
		t.Errorf("topic = %q", got.topic)
	}
	if string(got.payload) != `{"item":1}` {
		t.Errorf("payload = %q", got.payload)
	}
	if !got.retained {
		t.Error("the publish was not retained")
	}
}

// A message the broker pushes reaches the handler with its topic and
// payload.
func TestBusDeliversAnInboundPublishToTheHandler(t *testing.T) {
	received := make(chan brokerPublish, 1)
	handler := func(topic string, payload []byte) {
		received <- brokerPublish{topic: topic, payload: append([]byte(nil), payload...)}
	}
	_, brokers, connected := startBus(t, 1, nil, handler)
	waitForConnect(t, connected)

	brokers[0].push("liken/media/plays/house/movie/status", []byte(`{"paused":true}`))

	select {
	case got := <-received:
		if got.topic != "liken/media/plays/house/movie/status" {
			t.Errorf("topic = %q", got.topic)
		}
		if string(got.payload) != `{"paused":true}` {
			t.Errorf("payload = %q", got.payload)
		}
	case <-time.After(busTestTimeout):
		t.Fatal("the handler read nothing the broker pushed")
	}
}

// A dropped connection reconnects, and the remembered subscription
// goes out again on the new connection with no second Subscribe call.
func TestBusResendsSubscriptionsAfterAReconnect(t *testing.T) {
	bus, brokers, connected := startBus(t, 2, nil, nil)
	bus.Subscribe("liken/media/plays/+/+/status")

	waitForConnect(t, connected)
	if got := waitForString(t, brokers[0].subs); got != "liken/media/plays/+/+/status" {
		t.Fatalf("first subscription = %q", got)
	}

	// Drop the first connection. The client reconnects onto the second
	// broker and re-sends the filter it remembers.
	brokers[0].conn.Close()

	waitForConnect(t, connected)
	if got := waitForString(t, brokers[1].subs); got != "liken/media/plays/+/+/status" {
		t.Errorf("resent subscription = %q", got)
	}
}

// A publish made while the client is disconnected is dropped at QoS 0,
// and the caller re-publishes from onConnect once the connection
// returns.
func TestBusDropsAPublishWhileDisconnected(t *testing.T) {
	shorterBackoff(t)
	bus := newBus("pipe", "media-operator", nil, nil, nil)
	// No connection is ever dialed, so out stays nil and the publish
	// has nowhere to go.
	bus.Publish("liken/media/plays/house/movie/status", []byte("x"), true)
	// The test proves only that the call returns and panics on nothing.
}

// The client newBus builds dials the address over TCP. Nothing answers
// this port, so the dial fails rather than reaching some other broker.
func TestNewBusDialsTheAddressOverTCP(t *testing.T) {
	bus := newBus("127.0.0.1:1", "media-operator", nil, nil, nil)

	_, err := bus.dial(context.Background())

	if err == nil {
		t.Fatal("the dial reached something on a port nothing listens on")
	}
}

// A broker that never answers is retried ever more slowly, up to the
// ceiling, so a broker that is down does not become a tight reconnect loop.
func TestTheBackoffGrowsToItsCeilingWhileTheBrokerIsDown(t *testing.T) {
	shorterBackoff(t)
	waits := make(chan time.Duration, 16)
	last := time.Now()

	bus := newBus("pipe", "media-operator", nil, nil, nil)
	bus.dial = func(ctx context.Context) (net.Conn, error) {
		waits <- time.Since(last)
		last = time.Now()
		return nil, errors.New("the broker is down")
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		bus.Run(ctx)
	}()

	// The first dial is immediate. Each wait after it is twice the
	// one before, up to the ceiling the third wait already reaches.
	floors := []time.Duration{0, busMinBackoff, 2 * busMinBackoff, busMaxBackoff, busMaxBackoff}
	for dial, floor := range floors {
		select {
		case waited := <-waits:
			if waited < floor {
				t.Errorf("dial %d came %v after the one before, want at least %v", dial+1, waited, floor)
			}
		case <-time.After(busTestTimeout):
			t.Fatal("the client stopped dialing")
		}
	}
	cancel()

	select {
	case <-stopped:
	case <-time.After(busTestTimeout):
		t.Fatal("Run did not return when its context ended")
	}
}

// Run returns while it waits out a backoff, so a pod that is shutting
// down does not sit through the whole wait.
func TestRunReturnsWhileItWaitsOutABackoff(t *testing.T) {
	minWas, maxWas := busMinBackoff, busMaxBackoff
	t.Cleanup(func() { busMinBackoff, busMaxBackoff = minWas, maxWas })
	busMinBackoff, busMaxBackoff = time.Minute, time.Minute

	dialed := make(chan struct{}, 1)
	bus := newBus("pipe", "media-operator", nil, nil, nil)
	bus.dial = func(ctx context.Context) (net.Conn, error) {
		dialed <- struct{}{}
		return nil, errors.New("the broker is down")
	}

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		bus.Run(ctx)
	}()

	<-dialed
	cancel()

	select {
	case <-stopped:
	case <-time.After(busTestTimeout):
		t.Fatal("Run waited out the whole backoff")
	}
}
