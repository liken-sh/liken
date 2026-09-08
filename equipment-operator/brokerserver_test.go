package main

// The fake broker on a real socket, so a session dials an address the
// way it dials Mosquitto. Each accepted connection becomes one
// fakeBroker, which is the same far end bus_test.go drives over a pipe.

import (
	"net"
	"testing"
	"time"
)

type fakeBrokerServer struct {
	listener net.Listener
	sessions chan *fakeBroker
}

func startFakeBrokerServer(t *testing.T) *fakeBrokerServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	mustSucceed(t, err)

	server := &fakeBrokerServer{listener: listener, sessions: make(chan *fakeBroker, 8)}
	t.Cleanup(func() { listener.Close() })
	go server.accept(t)
	return server
}

func (s *fakeBrokerServer) accept(t *testing.T) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		t.Cleanup(func() { conn.Close() })
		select {
		case s.sessions <- newFakeBroker(conn):
		default:
		}
	}
}

func (s *fakeBrokerServer) address() string {
	return s.listener.Addr().String()
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
