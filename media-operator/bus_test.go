package main

// These tests run the client against a fake broker on a net.Pipe, so
// they cover the CONNECT handshake, resubscribing, publishing, and
// inbound delivery without TCP or Mosquitto.

import (
	"bufio"
	"context"
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

// busTestTimeout bounds each wait. In a synctest bubble it passes on the
// fake clock, so a client that never responds fails the test at once.
const busTestTimeout = 2 * time.Second

// brokerPublish is a message that the broker read from the client.
type brokerPublish struct {
	topic    string
	payload  []byte
	retained bool
}

// fakeBroker is the broker end of a pipe. It replies to CONNECT with a
// CONNACK and to each SUBSCRIBE with a SUBACK, and records each filter
// and PUBLISH that it receives.
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

// push sends the client a message with the retain flag, as the broker
// does for the stored value of a topic when a client subscribes.
func (b *fakeBroker) push(topic string, payload []byte) {
	b.conn.Write(frameOf(encodePublish(topic, payload, true)))
}

// forward sends the client a message without the retain flag, as the
// broker does for a publish that arrives while the client is subscribed.
func (b *fakeBroker) forward(topic string, payload []byte) {
	b.conn.Write(frameOf(encodePublish(topic, payload, false)))
}

// readTopicFilter reads the length-prefixed topic filter of a SUBSCRIBE.
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

// startBus starts a client whose dialer returns count pipe connections
// in order and then blocks, so a client that reconnects more often than
// the test expects waits instead of spinning.
func startBus(t *testing.T, count int, will *busWill, handler busHandler) (*Bus, []*fakeBroker, <-chan *Bus) {
	t.Helper()
	return startBusWith(t, count, will, handler, nil)
}

// startBusWith is startBus with a retainedHandler.
func startBusWith(t *testing.T, count int, will *busWill, handler, retained busHandler) (*Bus, []*fakeBroker, <-chan *Bus) {
	t.Helper()
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
	bus.retainedHandler = retained
	bus.dial = func(ctx context.Context) (net.Conn, error) {
		select {
		case conn := <-conns:
			return conn, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// A synctest bubble fails if a goroutine is still running when the
	// test ends, so the cleanup waits for Run to return.
	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-ended:
		case <-time.After(busTestTimeout):
			t.Error("the client kept running after the test ended")
		}
	})
	go func() {
		defer close(ended)
		bus.Run(ctx)
	}()
	return bus, brokers, connected
}

func waitForConnect(t *testing.T, connected <-chan *Bus) {
	t.Helper()
	select {
	case <-connected:
	case <-time.After(busTestTimeout):
		t.Fatal("the client never connected")
	}
}

// Connected is true from the CONNACK until the connection ends.
func TestConnectedIsTrueWhileASessionIsOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)
		if !bus.Connected() {
			t.Fatal("the client reports no session after its CONNACK")
		}

		waitForDisconnect(t, bus, brokers[0])
	})
}

// waitForDisconnect closes the broker's end of the connection and fails
// the test if Connected is still true once the client is idle. It must
// run in a synctest bubble.
func waitForDisconnect(t *testing.T, bus *Bus, broker *fakeBroker) {
	t.Helper()
	broker.conn.Close()
	synctest.Wait()
	if bus.Connected() {
		t.Fatal("the client still reports a session after the broker closed")
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

// A client that connects calls onConnect and sends every stored
// subscription.
func TestBusConnectsAndSendsStoredSubscriptions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		bus.Subscribe("liken/media/plays/+/+/status")

		waitForConnect(t, connected)
		if got := waitForString(t, brokers[0].subs); got != "liken/media/plays/+/+/status" {
			t.Errorf("the subscription is %q, want liken/media/plays/+/+/status", got)
		}
	})
}

// A publish reaches the broker with its topic, payload, and retain flag.
func TestBusPublishesToTheBroker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)

		bus.Publish("liken/media/plays/house/movie/status", []byte(`{"item":1}`), true)

		got := waitForPublish(t, brokers[0].pubs)
		if got.topic != "liken/media/plays/house/movie/status" {
			t.Errorf("the topic is %q, want liken/media/plays/house/movie/status", got.topic)
		}
		if string(got.payload) != `{"item":1}` {
			t.Errorf("the payload is %q, want {\"item\":1}", got.payload)
		}
		if !got.retained {
			t.Error("the publish was not retained")
		}
	})
}

// A message from the broker reaches the handler with its topic and
// payload.
func TestBusDeliversAnInboundPublishToTheHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
				t.Errorf("the topic is %q, want liken/media/plays/house/movie/status", got.topic)
			}
			if string(got.payload) != `{"paused":true}` {
				t.Errorf("the payload is %q, want {\"paused\":true}", got.payload)
			}
		case <-time.After(busTestTimeout):
			t.Fatal("the handler read nothing the broker pushed")
		}
	})
}

// After a dropped connection, the client reconnects and sends the stored
// subscription again without another Subscribe call.
func TestBusResendsSubscriptionsAfterAReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 2, nil, nil)
		bus.Subscribe("liken/media/plays/+/+/status")

		waitForConnect(t, connected)
		if got := waitForString(t, brokers[0].subs); got != "liken/media/plays/+/+/status" {
			t.Fatalf("the first subscription is %q, want liken/media/plays/+/+/status", got)
		}

		// The client's next dial returns the second broker.
		brokers[0].conn.Close()

		waitForConnect(t, connected)
		if got := waitForString(t, brokers[1].subs); got != "liken/media/plays/+/+/status" {
			t.Errorf("after the reconnect the subscription is %q, want liken/media/plays/+/+/status", got)
		}
	})
}

// Publish on a client that never connected does not panic.
func TestBusDropsAPublishWhileDisconnected(t *testing.T) {
	bus := newBus("pipe", "media-operator", nil, nil, nil)
	// The client never ran, so out is nil.
	bus.Publish("liken/media/plays/house/movie/status", []byte("x"), true)
}

// Nothing listens on port 1, so a TCP dial there fails.
func TestNewBusDialsTheAddressOverTCP(t *testing.T) {
	bus := newBus("127.0.0.1:1", "media-operator", nil, nil, nil)

	_, err := bus.dial(context.Background())

	if err == nil {
		t.Fatal("the dial reached something on a port nothing listens on")
	}
}

// While the broker is down, the first dial is immediate and each later
// wait doubles until it reaches busMaxBackoff.
func TestTheBackoffGrowsToItsCeilingWhileTheBrokerIsDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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

		want := []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
			16 * time.Second, busMaxBackoff, busMaxBackoff}
		for dial, wait := range want {
			if waited := <-waits; waited != wait {
				t.Errorf("dial %d came %v after the one before, want %v", dial+1, waited, wait)
			}
		}
		cancel()

		select {
		case <-stopped:
		case <-time.After(busTestTimeout):
			t.Fatal("Run did not return when its context ended")
		}
	})
}

// Run returns as soon as its context ends, even during a backoff wait.
func TestRunReturnsDuringABackoffWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		began := time.Now()
		cancel()
		<-stopped

		if waited := time.Since(began); waited != 0 {
			t.Errorf("Run returned %s after its context ended, want at once", waited)
		}
	})
}

// A message with the retain flag reaches the retained handler, and a
// message without it reaches the ordinary handler.
func TestARetainedMessageReachesTheRetainedHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		live := make(chan string, 2)
		retained := make(chan string, 2)
		_, brokers, connected := startBusWith(t, 1, nil,
			func(topic string, _ []byte) { live <- topic },
			func(topic string, _ []byte) { retained <- topic })
		waitForConnect(t, connected)

		brokers[0].push("liken/media/players/house/theater/volume", []byte(`{"level":0.5,"muted":false}`))
		brokers[0].forward("liken/media/players/house/theater/volume", []byte(`{"level":0.55,"muted":false}`))

		mustMatch(t, waitForTopic(t, retained), "liken/media/players/house/theater/volume")
		mustMatch(t, waitForTopic(t, live), "liken/media/players/house/theater/volume")
	})
}

// Without a retained handler, the ordinary handler receives retained
// messages too.
func TestWithNoRetainedHandlerEveryMessageReachesTheHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		live := make(chan string, 2)
		_, brokers, connected := startBus(t, 1, nil, func(topic string, _ []byte) { live <- topic })
		waitForConnect(t, connected)

		brokers[0].push("liken/media/remotes/house/sofa/focus", []byte("theater"))

		mustMatch(t, waitForTopic(t, live), "liken/media/remotes/house/sofa/focus")
	})
}

func waitForTopic(t *testing.T, topics <-chan string) string {
	t.Helper()
	select {
	case topic := <-topics:
		return topic
	case <-time.After(busTestTimeout):
		t.Fatal("no message reached the handler")
		return ""
	}
}
