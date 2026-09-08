package main

// what these tests prove: what one failed read costs the run, the environment
// the Job reads, and that nothing is published before the broker answers the
// handshake.

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// one user whose items cannot be read costs that user alone, whichever of the
// two reads failed. The run carries on, and the error it returns fails the
// Job, so the next pass runs it again.
func TestOneUserWhoseItemsCannotBeReadDoesNotStopTheRun(t *testing.T) {
	for _, test := range []struct {
		name string
		one  bool
	}{
		{name: "the resumable read"},
		{name: "the played read", one: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			backfill, messages, logged := standBackfill(t, &fakeBackfillJellyfin{
				users: []jellyfinUser{{Name: "Chris", ID: "user-chris"},
					{Name: "kelly", ID: "user-kelly"}},
				refuse:    "user-chris",
				refuseOne: test.one,
				resumable: map[string][]jellyfinItem{"user-kelly": {{ID: "item-matrix", Type: "Movie",
					ProviderIds: map[string]string{"Tmdb": "603"}}}},
			})

			counts, err := backfill.run(t.Context())

			if err == nil {
				t.Fatal("the run answered no error for the user it could not read")
			}
			want := jellyfinBackfillCounts{users: 2, published: 1, failed: 1}
			if counts != want {
				t.Errorf("counts = %+v, want %+v", counts, want)
			}
			if len(messages.held) != 1 {
				t.Errorf("messages = %d, want the one user it could read", len(messages.held))
			}
			if !strings.Contains(logged.String(), "could not read the items of the user Chris") {
				t.Errorf("log = %q, want the user it could not read", logged.String())
			}
		})
	}
}

// a server that answers no users ends the run before anything is published,
// because there is nothing to read the items of.
func TestTheRunEndsWhenTheUsersCannotBeRead(t *testing.T) {
	backfill, messages, _ := standBackfill(t, &fakeBackfillJellyfin{refuseAll: true})

	counts, err := backfill.run(t.Context())

	if err == nil {
		t.Fatal("the run answered no error for the users it could not read")
	}
	if counts != (jellyfinBackfillCounts{}) {
		t.Errorf("counts = %+v, want nothing counted", counts)
	}
	if len(messages.held) != 0 {
		t.Errorf("messages = %d, want none", len(messages.held))
	}
}

// the Job reads the namespace, the topic tree, the broker, and the Jellyfin
// server out of its environment, and falls back to the default topic base.
func TestNewJellyfinBackfillReadsItsEnvironment(t *testing.T) {
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(topicBaseVariable, "")
	t.Setenv(busAddressVariable, "broker.liken:1883")
	t.Setenv(jellyfinURLVariable, "http://jellyfin.jellyfin.svc:8096")
	t.Setenv(jellyfinAPIKeyVariable, "the-key")
	logged := &strings.Builder{}

	backfill := newJellyfinBackfill(logged)

	if backfill.namespace != "house" || backfill.topicBase != defaultTopicBase {
		t.Errorf("namespace and base = %q and %q, want house and the default",
			backfill.namespace, backfill.topicBase)
	}
	if backfill.api.base != "http://jellyfin.jellyfin.svc:8096" || backfill.api.key != "the-key" {
		t.Errorf("server = %q with the key %q, want the two the environment names",
			backfill.api.base, backfill.api.key)
	}
	if backfill.bus == nil || backfill.publish == nil || backfill.ready == nil {
		t.Error("the backfill was built with no bus to publish over")
	}
	if backfill.pace != jellyfinBackfillPace {
		t.Errorf("pace = %s, want %s", backfill.pace, jellyfinBackfillPace)
	}
	if !strings.Contains(logged.String(), "backfilling the progress of house") {
		t.Errorf("log = %q, want the namespace and the server", logged.String())
	}
}

// the three bounds in the values a test drives them at.
func shorterBackfillWaits(t *testing.T, connect time.Duration) {
	t.Helper()
	connectWas, paceWas, flushWas := jellyfinBackfillConnect, jellyfinBackfillPace, jellyfinBackfillFlush
	jellyfinBackfillConnect, jellyfinBackfillPace, jellyfinBackfillFlush =
		connect, time.Microsecond, 10*time.Millisecond
	t.Cleanup(func() {
		jellyfinBackfillConnect, jellyfinBackfillPace, jellyfinBackfillFlush =
			connectWas, paceWas, flushWas
	})
}

// wires the backfill to a client that dials one pipe, with the broker on the
// far end. The connect callback is the one the environment builds, so the
// wait for the handshake is the one the Job makes.
func standBackfillBus(t *testing.T, backfill *jellyfinBackfill, answering bool) *fakeBroker {
	t.Helper()
	shorterBackoff(t)
	near, far := net.Pipe()
	t.Cleanup(func() {
		near.Close()
		far.Close()
	})
	broker := newFakeBroker(far)

	ready, once := make(chan struct{}), sync.Once{}
	backfill.ready = ready
	backfill.pace = jellyfinBackfillPace
	backfill.bus = newBus("pipe", "jellyfin-backfill-house", nil,
		func(*Bus) { once.Do(func() { close(ready) }) }, nil)
	conns := make(chan net.Conn, 1)
	if answering {
		conns <- near
	}
	backfill.bus.dial = func(ctx context.Context) (net.Conn, error) {
		select {
		case conn := <-conns:
			return conn, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	backfill.publish = backfill.bus.Publish
	return broker
}

// the run publishes over the broker it waited for, so no message is dropped
// for a client that had not connected yet.
func TestTheBackfillPublishesOverTheBroker(t *testing.T) {
	shorterBackfillWaits(t, busTestTimeout)
	backfill, _, _ := standBackfill(t, &fakeBackfillJellyfin{
		users: []jellyfinUser{{Name: "Chris", ID: "user-chris"}},
		resumable: map[string][]jellyfinItem{"user-chris": {{ID: "item-matrix", Type: "Movie",
			ProviderIds:  map[string]string{"Tmdb": "603"},
			RunTimeTicks: 81600000000,
			UserData:     jellyfinUserData{PlaybackPositionTicks: 42100000000}}}},
	})
	broker := standBackfillBus(t, backfill, true)

	counts, err := backfill.runOnBus(t.Context())

	if err != nil {
		t.Fatalf("running the backfill: %v", err)
	}
	if counts != (jellyfinBackfillCounts{users: 1, published: 1}) {
		t.Errorf("counts = %+v, want one user and one play", counts)
	}
	published := waitForPublish(t, broker.pubs)
	want := playOutsideTopic(defaultTopicBase, "house", "jellyfin-user-chris-item-matrix")
	if published.topic != want {
		t.Errorf("topic = %q, want %q", published.topic, want)
	}
	if published.retained {
		t.Error("the outside play was retained")
	}
}

// a Job that reaches no broker fails within the connect wait and publishes
// nothing, because a publish made while the client is disconnected is
// dropped.
func TestTheBackfillEndsWhenTheBrokerDoesNotAnswer(t *testing.T) {
	shorterBackfillWaits(t, 50*time.Millisecond)
	backfill, messages, _ := standBackfill(t, &fakeBackfillJellyfin{
		users: []jellyfinUser{{Name: "Chris", ID: "user-chris"}},
		resumable: map[string][]jellyfinItem{"user-chris": {{ID: "item-matrix", Type: "Movie",
			ProviderIds: map[string]string{"Tmdb": "603"}}}},
	})
	standBackfillBus(t, backfill, false)

	counts, err := backfill.runOnBus(t.Context())

	if err == nil {
		t.Fatal("the run answered no error for the broker it never reached")
	}
	if counts != (jellyfinBackfillCounts{}) {
		t.Errorf("counts = %+v, want nothing counted", counts)
	}
	if len(messages.held) != 0 {
		t.Errorf("messages = %d, want none", len(messages.held))
	}
}

// the signal the kubelet sends ends the wait for the broker, so a Job that is
// stopped ends instead of holding its pod for the connect wait.
func TestTheBackfillEndsOnTheSignal(t *testing.T) {
	shorterBackfillWaits(t, busTestTimeout)
	backfill, _, _ := standBackfill(t, &fakeBackfillJellyfin{})
	standBackfillBus(t, backfill, false)
	stopped, stop := context.WithCancel(t.Context())
	stop()

	if _, err := backfill.runOnBus(stopped); err == nil {
		t.Fatal("the run answered no error for the signal that stopped it")
	}
}
