package main

// What these tests prove: the environment the role reads, the topics it
// joins a Play from, where each message lands, and that the role runs
// until the kubelet stops it.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// One message the role published, as the recording publish function
// read it.
type jellyfinMessage struct {
	topic    string
	payload  []byte
	retained bool
}

// The bus a test reads. The role publishes through a function, so a
// test needs no broker.
type jellyfinMessages struct {
	mutex sync.Mutex
	held  []jellyfinMessage
}

func (m *jellyfinMessages) publish(topic string, payload []byte, retained bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.held = append(m.held, jellyfinMessage{topic: topic, payload: payload, retained: retained})
}

// The one outside play the role published, read back as the payload the
// progress role receives.
func (m *jellyfinMessages) outside(t *testing.T) (string, outsidePlay) {
	t.Helper()
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if len(m.held) != 1 {
		t.Fatalf("messages = %d, want one", len(m.held))
	}
	play := outsidePlay{}
	if err := json.Unmarshal(m.held[0].payload, &play); err != nil {
		t.Fatalf("reading the message back: %v", err)
	}
	return m.held[0].topic, play
}

// One role over a fake Jellyfin, with the index primed, a recording
// publish function, and a log the test reads.
func standJellyfinRole(t *testing.T, fake *fakeJellyfin) (*jellyfin, *jellyfinMessages, *strings.Builder) {
	t.Helper()
	logged := &strings.Builder{}
	messages := &jellyfinMessages{}
	role := newJellyfinOn("house", defaultTopicBase, defaultMediaTopicBase, "127.0.0.1:0",
		standJellyfinServer(t, fake), newJellyfinClock().now, messages.publish, logged)
	role.index.prime(t.Context())
	return role, messages, logged
}

// The role reads the namespace, both topic trees, the broker, and the
// Jellyfin server out of the environment, and falls back to the two
// default topic bases and the default port.
func TestNewJellyfinReadsItsEnvironment(t *testing.T) {
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(topicBaseVariable, "")
	t.Setenv(mediaTopicBaseVariable, "")
	t.Setenv(busAddressVariable, "")
	t.Setenv(jellyfinURLVariable, "http://jellyfin.jellyfin.svc:8096")
	t.Setenv(jellyfinAPIKeyVariable, "the-key")
	t.Setenv(jellyfinListenVariable, "")
	logged := &strings.Builder{}

	role := newJellyfin(logged)

	if role.namespace != "house" {
		t.Errorf("namespace = %q, want house", role.namespace)
	}
	if role.topicBase != defaultTopicBase || role.mediaBase != defaultMediaTopicBase {
		t.Errorf("bases = %q and %q, want the two defaults", role.topicBase, role.mediaBase)
	}
	if role.listen != defaultJellyfinListen {
		t.Errorf("listen = %q, want %q", role.listen, defaultJellyfinListen)
	}
	if role.api.base != "http://jellyfin.jellyfin.svc:8096" || role.api.key != "the-key" {
		t.Errorf("server = %q with key %q, want the two the environment states", role.api.base, role.api.key)
	}
	if role.publish == nil || role.bus == nil {
		t.Error("the role was built with no bus to publish on")
	}
	if !strings.Contains(logged.String(), "house") {
		t.Errorf("log = %q, want the namespace it serves", logged.String())
	}
}

// The stated port and topic trees are the ones the role takes.
func TestNewJellyfinTakesTheStatedAddresses(t *testing.T) {
	t.Setenv(libraryNamespaceVariable, "loft")
	t.Setenv(topicBaseVariable, "house/library")
	t.Setenv(mediaTopicBaseVariable, "house/media")
	t.Setenv(busAddressVariable, "")
	t.Setenv(jellyfinURLVariable, "http://jellyfin:8096")
	t.Setenv(jellyfinAPIKeyVariable, "the-key")
	t.Setenv(jellyfinListenVariable, ":9090")

	role := newJellyfin(&strings.Builder{})

	if role.listen != ":9090" || role.topicBase != "house/library" || role.mediaBase != "house/media" {
		t.Errorf("role = %q, %q, %q, want what the environment states",
			role.listen, role.topicBase, role.mediaBase)
	}
}

// The role reads the sidecar's position on the media tree and the
// operator's audience and final on this operator's tree, for its own
// namespace.
func TestTheJellyfinRoleSubscribesToEveryTopicItJoins(t *testing.T) {
	role, _, _ := standJellyfinRole(t, jellyfinFixture())
	role.bus = newBus("nowhere", "jellyfin-house", nil, nil, role.onMessage)

	role.subscribe()

	held := map[string]bool{}
	for filter := range role.bus.filters {
		held[filter] = true
	}
	for _, filter := range []string{
		mediaPlayStatusFilter(defaultMediaTopicBase, "house"),
		playAudienceFilter(defaultTopicBase, "house"),
		playFinalFilter(defaultTopicBase, "house"),
	} {
		if !held[filter] {
			t.Errorf("the role does not subscribe to %q", filter)
		}
	}
	if len(held) != 3 {
		t.Errorf("filters = %v, want the three it joins", held)
	}
}

// Each message lands in the join, and a message of another namespace or
// another kind lands nowhere.
func TestEachMessageLandsInTheJoin(t *testing.T) {
	for _, test := range []struct {
		name   string
		topic  string
		writes int
	}{
		{name: "a status of this namespace", writes: 1,
			topic: defaultMediaTopicBase + "/plays/house/play-1/status"},
		{name: "a status of another namespace", writes: 0,
			topic: defaultMediaTopicBase + "/plays/loft/play-1/status"},
		{name: "the availability of a playback pod", writes: 0,
			topic: defaultMediaTopicBase + "/plays/house/play-1/availability"},
		{name: "a final of this namespace", writes: 1,
			topic: defaultTopicBase + "/plays/house/play-1/final"},
		{name: "a final of another namespace", writes: 0,
			topic: defaultTopicBase + "/plays/loft/play-1/final"},
		{name: "a message of neither tree", writes: 0,
			topic: "somewhere/else/entirely"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := jellyfinFixture()
			role, _, _ := standJellyfinRole(t, fake)
			role.onMessage(playAudienceTopic(defaultTopicBase, "house", "play-1"),
				jellyfinAudience(t, []string{"chris"}, map[string]string{"tmdb": "603"}, 0, 0))

			role.onMessage(test.topic, []byte(`{"phase":"Finished","item":0,"position":"1:10:10","duration":"2:16:00"}`))
			role.out.tick(t.Context())

			if len(fake.writes) != test.writes {
				t.Errorf("writes = %+v, want %d", fake.writes, test.writes)
			}
		})
	}
}

// An audience of another namespace lands nowhere, so one role carries
// one namespace's Plays.
func TestAnAudienceOfAnotherNamespaceLandsNowhere(t *testing.T) {
	fake := jellyfinFixture()
	role, _, _ := standJellyfinRole(t, fake)

	role.onMessage(playAudienceTopic(defaultTopicBase, "loft", "play-1"),
		jellyfinAudience(t, []string{"chris"}, map[string]string{"tmdb": "603"}, 0, 0))
	role.onMessage(defaultMediaTopicBase+"/plays/house/play-1/status",
		[]byte(`{"item":0,"position":"1:10:10","duration":"2:16:00"}`))
	role.out.tick(t.Context())

	if len(fake.writes) != 0 {
		t.Errorf("writes = %+v, want none for a Play with no audience", fake.writes)
	}
}

// The role runs the webhook server, the bus, and the write loop until
// the context ends.
func TestTheRoleRunsUntilItIsStopped(t *testing.T) {
	role, _, _ := standJellyfinRole(t, jellyfinFixture())
	role.bus = newBus("nowhere", "jellyfin-house", nil, nil, role.onMessage)
	jellyfinTicksEvery(t, time.Millisecond)

	stopped, stop := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer stop()
	role.serve(stopped)
}

// A port the role cannot listen on ends the role, because a Jellyfin
// that posts to an address nothing answers loses every event it posts.
func TestAPortTheRoleCannotListenOnEndsIt(t *testing.T) {
	role, _, logged := standJellyfinRole(t, jellyfinFixture())
	role.bus = newBus("nowhere", "jellyfin-house", nil, nil, role.onMessage)
	role.listen = "256.256.256.256:8080"
	jellyfinTicksEvery(t, time.Millisecond)

	role.serve(t.Context())

	if !strings.Contains(logged.String(), "the webhook server stopped") {
		t.Errorf("log = %q, want the server that could not listen", logged.String())
	}
}

// Moves the write loop's pass to a wait a test can hold, and puts it
// back afterwards.
func jellyfinTicksEvery(t *testing.T, interval time.Duration) {
	t.Helper()
	held := jellyfinWriteInterval
	jellyfinWriteInterval = interval
	t.Cleanup(func() { jellyfinWriteInterval = held })
}
