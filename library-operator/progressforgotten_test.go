package main

// What this test proves: a progress role's answer to a forget request is
// retained on the broker again after the broker restarts. The bus runs with
// persistence off, so a restart drops the answer, and the operator holds the
// Person's finalizer until it reads an answer from every namespace. The
// operator publishes its forget request again after it connects, before or
// after the role's own session is back, and the role answers it again.

import (
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// progressOnABroker is the house namespace's progress role over a store in
// SQLite, subscribed through its own bus to the broker.
func progressOnABroker(t *testing.T, broker *retainBroker) {
	t.Helper()
	server := apiservertest.Start(t, &sqliteAgent{db: newSQLiteProgress(t)})
	role := newProgressOn("house", defaultTopicBase, defaultMediaTopicBase,
		newProgressStore(apiservertest.Host, server.Client()), io.Discard)
	role.bus = newBus(testBusAddress, "progress-house", nil, role.onConnect, role.onMessage)
	role.bus.dial = broker.dial
	role.subscribe()
	go role.bus.Run(t.Context())
}

// operatorBusOnABroker is a client that publishes the way the operator does,
// connected to the broker.
func operatorBusOnABroker(t *testing.T, broker *retainBroker) *Bus {
	t.Helper()
	bus := newBus(testBusAddress, "library-operator", nil, nil, nil)
	bus.dial = broker.dial
	go bus.Run(t.Context())
	return bus
}

func TestTheForgottenAnswerIsRetainedAgainAfterABrokerRestart(t *testing.T) {
	forget := personForgetTopic(defaultTopicBase, "person-b")
	forgotten := personForgottenTopic(defaultTopicBase, "person-b", "house")
	request := []byte(`{"at":"2026-09-06T21:00:00Z"}`)
	for _, test := range []struct {
		name string
		// asks again the way the operator's republish does, at one point
		// against the role's reconnect.
		askAgain func(broker *retainBroker, operator *Bus)
	}{
		{name: "before the role reconnects", askAgain: func(broker *retainBroker, _ *Bus) {
			broker.retain(forget, request)
		}},
		{name: "after the role reconnects", askAgain: func(_ *retainBroker, operator *Bus) {
			time.Sleep(2 * busMinBackoff)
			synctest.Wait()
			operator.Publish(forget, request, true)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				broker := newRetainBroker(t)
				broker.retain(forget, request)
				operator := operatorBusOnABroker(t, broker)
				progressOnABroker(t, broker)
				synctest.Wait()
				if _, held := broker.retainedOn(forgotten); !held {
					t.Fatalf("the broker retains nothing on %s before it restarted", forgotten)
				}

				broker.restart()
				test.askAgain(broker, operator)
				time.Sleep(4 * busMinBackoff)
				synctest.Wait()

				if _, held := broker.retainedOn(forgotten); !held {
					t.Errorf("the broker retains nothing on %s after it restarted", forgotten)
				}
			})
		})
	}
}
