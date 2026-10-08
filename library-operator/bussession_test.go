package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A short connection to the broker: what it writes, what it reads back, and
// how it ends when the broker does not answer.

// A session on the broker given, closed when the test ends.
func testSession(t *testing.T, broker *retainBroker) *busSession {
	t.Helper()
	session, err := openBusSession(t.Context(), broker.dial, "test-session")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.close)
	return session
}

// A flush returns once the broker has read every publish before it, so a
// reader that connects after the flush finds each one.
func TestASessionsFlushFollowsEveryPublish(t *testing.T) {
	broker := newRetainBroker(t)
	session := testSession(t, broker)

	for _, topic := range []string{"liken/library/a", "liken/library/b"} {
		if err := session.retain(topic, []byte(topic)); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.flush(); err != nil {
		t.Fatal(err)
	}

	if got := broker.retainedUnder("liken/library/"); len(got) != 2 {
		t.Errorf("the broker retains %v, want both topics", got)
	}
}

// A read takes the one retained message on its topic, and a topic with none,
// or with an empty one, reads as absent.
func TestASessionReadsOneRetainedMessage(t *testing.T) {
	cases := []struct {
		name    string
		held    string
		payload string
		found   bool
	}{
		{name: "a message", held: "liken/library/item", payload: "one", found: true},
		{name: "no message"},
		{name: "a message on another topic", held: "liken/library/other", payload: "one"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			broker := newRetainBroker(t)
			if one.held != "" {
				broker.retain(one.held, []byte(one.payload))
			}
			session := testSession(t, broker)

			payload, found, err := session.readRetained("liken/library/item")

			if err != nil || found != one.found || (found && string(payload) != one.payload) {
				t.Errorf("read = %q, %v, %v, want %q, %v", payload, found, err, one.payload, one.found)
			}
		})
	}
}

// A broker that accepts the connection and never answers holds the session
// only until the caller's deadline.
func TestASessionEndsAtTheDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		silent := func(context.Context) (net.Conn, error) {
			client, server := net.Pipe()
			t.Cleanup(func() { server.Close() })
			go func() {
				buffer := make([]byte, 1024)
				for {
					if _, err := server.Read(buffer); err != nil {
						return
					}
				}
			}()
			return client, nil
		}

		_, err := openBusSession(ctx, silent, "test-session")

		if err == nil || !strings.Contains(err.Error(), "the broker") {
			t.Errorf("open = %v, want the connect to fail at the deadline", err)
		}
	})
}
