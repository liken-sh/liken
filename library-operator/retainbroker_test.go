package main

// retainBroker is an in-process broker that holds retained messages the way
// Mosquitto does, for the tests of the work list: one container publishes a
// list and leaves, and later pods each read one message of it. Every
// connection is the far end of a net.Pipe, so the tests reach no socket.
//
// It handles each connection's packets in the order they arrive and writes
// each answer before it reads the next packet, which is the order the work
// list's sessions depend on: a SUBSCRIBE's SUBACK and retained messages go
// out before the answer to a PINGREQ sent after it.

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
)

type retainBroker struct {
	mutex       sync.Mutex
	retained    map[string][]byte
	subscribers map[*brokerClient][]string
	// Every topic a client published to, in the order the broker read them.
	published []string
}

// One connection's write side. A pipe has no buffer, so a broker that wrote
// its answers on the reading goroutine would wait on a client that is itself
// waiting to write its next packet. The queue stands in for the socket buffer
// a real connection has, and one goroutine drains it in order.
type brokerClient struct {
	out  chan []byte
	done chan struct{}
}

func (c *brokerClient) write(frame []byte) {
	select {
	case c.out <- frame:
	case <-c.done:
	}
}

func newRetainBroker(t *testing.T) *retainBroker {
	t.Helper()
	return &retainBroker{retained: map[string][]byte{}, subscribers: map[*brokerClient][]string{}}
}

// A dial that connects to the broker, for a Bus or a session.
func (b *retainBroker) dial(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	go b.serve(server)
	return client, nil
}

// The retained message on one topic, and whether there is one.
func (b *retainBroker) retainedOn(topic string) ([]byte, bool) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	payload, held := b.retained[topic]
	return payload, held
}

// Every topic that holds a retained message under the prefix given.
func (b *retainBroker) retainedUnder(prefix string) []string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	var topics []string
	for topic := range b.retained {
		if strings.HasPrefix(topic, prefix) {
			topics = append(topics, topic)
		}
	}
	return topics
}

// Holds a message as if a publisher had retained it, the state a broker has
// before the test's own client connects.
func (b *retainBroker) retain(topic string, payload []byte) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.retained[topic] = payload
}

func (b *retainBroker) serve(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	client := &brokerClient{out: make(chan []byte, 1<<16), done: make(chan struct{})}
	go func() {
		for {
			select {
			case frame := <-client.out:
				if _, err := conn.Write(frame); err != nil {
					return
				}
			case <-client.done:
				return
			}
		}
	}()
	defer close(client.done)
	defer func() {
		b.mutex.Lock()
		delete(b.subscribers, client)
		b.mutex.Unlock()
	}()
	first, _, err := readPacket(reader)
	if err != nil || first&0xF0 != mqttConnect {
		return
	}
	client.write([]byte{mqttConnack, 0x02, 0x00, 0x00})
	for {
		first, body, err := readPacket(reader)
		if err != nil {
			return
		}
		switch first & 0xF0 {
		case mqttPublish:
			topic, payload, ok := parsePublish(body)
			if ok {
				b.publish(topic, payload, first&0x01 != 0)
			}
		case mqttSubscribe:
			_, filter, _ := readTopicFilter(body[2:])
			client.write([]byte{mqttSuback, 0x03, body[0], body[1], 0x00})
			b.subscribe(client, filter)
		case mqttPingreq:
			client.write([]byte{mqttPingresp, 0x00})
		case mqttDisconnect:
			return
		}
	}
}

// A retained publish replaces the topic's message, and an empty one clears
// it. Every subscriber whose filter matches receives the publish, the empty
// one included.
func (b *retainBroker) publish(topic string, payload []byte, retained bool) {
	b.mutex.Lock()
	b.published = append(b.published, topic)
	if retained && len(payload) == 0 {
		delete(b.retained, topic)
	} else if retained {
		b.retained[topic] = append([]byte(nil), payload...)
	}
	var receivers []*brokerClient
	for client, filters := range b.subscribers {
		for _, filter := range filters {
			if topicMatches(filter, topic) {
				receivers = append(receivers, client)
				break
			}
		}
	}
	b.mutex.Unlock()
	for _, client := range receivers {
		client.write(encodePublish(topic, payload, false))
	}
}

// A new subscription receives every retained message its filter matches,
// before the broker reads the connection's next packet.
func (b *retainBroker) subscribe(client *brokerClient, filter string) {
	b.mutex.Lock()
	b.subscribers[client] = append(b.subscribers[client], filter)
	var frames [][]byte
	for topic, payload := range b.retained {
		if topicMatches(filter, topic) {
			frames = append(frames, encodePublish(topic, payload, true))
		}
	}
	b.mutex.Unlock()
	for _, frame := range frames {
		client.write(frame)
	}
}

// Whether an MQTT filter matches a topic: + matches one level, and # matches
// the rest.
func topicMatches(filter, topic string) bool {
	filters, topics := strings.Split(filter, "/"), strings.Split(topic, "/")
	for index, level := range filters {
		if level == "#" {
			return true
		}
		if index >= len(topics) || (level != "+" && level != topics[index]) {
			return false
		}
	}
	return len(filters) == len(topics)
}
