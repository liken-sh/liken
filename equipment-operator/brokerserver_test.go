package main

// The fake broker on the test network, so a session dials an address
// the way it dials Mosquitto. Each connection becomes one fakeBroker,
// the same far end bus_test.go drives over a pipe of its own.

import (
	"net"
	"sync"
	"testing"
	"time"
)

type fakeBrokerServer struct {
	listening string
	sessions  chan *fakeBroker
	mutex     sync.Mutex
	conns     []net.Conn
}

func startFakeBrokerServer(t *testing.T) *fakeBrokerServer {
	t.Helper()
	server := &fakeBrokerServer{sessions: make(chan *fakeBroker, 8)}
	server.listening = testNetwork.listen(t, server.accept)
	t.Cleanup(server.close)
	return server
}

// accept takes one connection. A test reads the sessions it cares
// about, and a connection past the channel's room is left open and
// unread, as a broker that is slow to answer leaves it.
func (s *fakeBrokerServer) accept(conn net.Conn) {
	s.mutex.Lock()
	s.conns = append(s.conns, conn)
	s.mutex.Unlock()
	select {
	case s.sessions <- newFakeBroker(conn):
	default:
	}
}

// close closes every connection the broker took, when the test ends.
func (s *fakeBrokerServer) close() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, conn := range s.conns {
		conn.Close()
	}
}

func (s *fakeBrokerServer) address() string {
	return s.listening
}

// waitForSession answers the next connection the broker accepted, and
// fails the test rather than hanging when none arrives.
func (s *fakeBrokerServer) waitForSession(t *testing.T) *fakeBroker {
	t.Helper()
	select {
	case broker := <-s.sessions:
		return broker
	case <-time.After(testTimeout):
		t.Fatal("nothing connected to the broker")
		return nil
	}
}

// refuseTopic fails the test if anything is published on the topic
// inside the window.
func (b *fakeBroker) refuseTopic(t *testing.T, topic string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case published := <-b.pubs:
			if published.topic == topic {
				t.Fatalf("%q carried %q", topic, published.payload)
			}
		case <-deadline:
			return
		}
	}
}

// waitForTopic reads publishes until it sees one on the topic it wants,
// so a test names the one message it cares about out of the marks and
// levels around it.
func (b *fakeBroker) waitForTopic(t *testing.T, topic string) brokerPublish {
	t.Helper()
	var seen []string
	deadline := time.After(testTimeout)
	for {
		select {
		case published := <-b.pubs:
			if published.topic == topic {
				return published
			}
			seen = append(seen, published.topic)
		case <-deadline:
			t.Fatalf("nothing was published on %q; the broker saw %v", topic, seen)
			return brokerPublish{}
		}
	}
}
