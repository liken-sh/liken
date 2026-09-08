package main

// What these tests prove: the rows the progress role writes from each
// message it hears, and the messages it publishes back, against a
// progress store loaded with the shipped schema and a broker the test
// reads.

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The role reads the namespace, both topic trees, the broker, and its
// agent's address out of the environment, and falls back to the
// loopback agent and the two default topic bases.
func TestNewProgressReadsItsEnvironment(t *testing.T) {
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(topicBaseVariable, "")
	t.Setenv(mediaTopicBaseVariable, "")
	t.Setenv(progressAPIVariable, "")
	t.Setenv(busAddressVariable, "")
	var logged strings.Builder

	role := newProgress(&logged)

	if role.namespace != "house" {
		t.Errorf("namespace = %q, want house", role.namespace)
	}
	if role.store.base != defaultProgressAPI {
		t.Errorf("store = %q, want the loopback agent %s", role.store.base, defaultProgressAPI)
	}
	if role.topicBase != defaultTopicBase || role.mediaBase != defaultMediaTopicBase {
		t.Errorf("bases = %q and %q, want the two defaults", role.topicBase, role.mediaBase)
	}
	if want := progressAvailabilityTopic(defaultTopicBase, "house"); role.availabilityTopic != want {
		t.Errorf("availability topic = %q, want %q", role.availabilityTopic, want)
	}
	if !strings.Contains(logged.String(), "house") {
		t.Errorf("log = %q, want the namespace it records", logged.String())
	}
}

// The role reads a media status topic in its own namespace, both of its
// own Play topics, and every person's forget topic.
func TestTheProgressRoleSubscribesToEveryTopicItRecords(t *testing.T) {
	role, _ := recordingProgress(t)

	held := map[string]bool{}
	for filter := range role.bus.filters {
		held[filter] = true
	}

	for _, filter := range []string{
		mediaPlayStatusFilter(defaultMediaTopicBase, "house"),
		mediaPlayAvailabilityFilter(defaultMediaTopicBase, "house"),
		playAudienceFilter(defaultTopicBase, "house"),
		playFinalFilter(defaultTopicBase, "house"),
		playOutsideFilter(defaultTopicBase, "house"),
		personForgetFilter(defaultTopicBase),
	} {
		if !held[filter] {
			t.Errorf("the role does not subscribe to %q", filter)
		}
	}
}

// recordingProgress is one role over a store loaded with the shipped
// schema and a bus that never connects, so a test drives a message
// through the handler and reads the rows.
func recordingProgress(t *testing.T) (*progress, *sql.DB) {
	t.Helper()
	db := newSQLiteProgress(t)
	server := httptest.NewServer(&sqliteAgent{db: db})
	t.Cleanup(server.Close)

	role := newProgressOn("house", defaultTopicBase, defaultMediaTopicBase,
		newProgressStore(server.URL, server.Client()), io.Discard)
	role.bus = newBus("nowhere", "progress-house", nil, nil, role.onMessage)
	role.subscribe()
	return role, db
}

// A media status report writes where the Play reached and marks it
// running, so a screen that reads the store sees a film in flight.
func TestAStatusReportWritesThePosition(t *testing.T) {
	role, db := recordingProgress(t)

	role.onMessage(mediaPlayStatusTopic("house", "play-1"),
		[]byte(`{"paused":false,"item":2,"position":"1:01:05","duration":"2:00:00","title":"ignored"}`))

	row := heldPlay(t, db, "play-1")
	if row.Item != 2 || row.Position != 3665 || row.Duration != 7200 {
		t.Errorf("row = %+v, want the reported item and the positions in seconds", row)
	}
	if row.Phase != playPhaseRunning {
		t.Errorf("phase = %q, want %q", row.Phase, playPhaseRunning)
	}
}

// A status report on another namespace's topic writes nothing, because
// one store holds one namespace.
func TestAStatusReportOfAnotherNamespaceWritesNothing(t *testing.T) {
	role, db := recordingProgress(t)

	role.onMessage(mediaPlayStatusTopic("loft", "play-1"),
		[]byte(`{"item":0,"position":"0:00:10","duration":"1:00:00"}`))

	if heldPlay(t, db, "play-1").Play != "" {
		t.Error("the role recorded a Play of another namespace")
	}
}

// An audience message writes the Player, the people, and the aliases,
// which is everything the role cannot read for itself.
func TestAnAudienceMessageWritesWhatTheOperatorKnows(t *testing.T) {
	role, db := recordingProgress(t)
	payload, _ := json.Marshal(playAudience{
		Player: "living-room",
		People: []string{"chris"}, Aliases: map[string]string{"tmdb": "2316"}, Season: 3, Episode: 5,
	})

	role.onMessage(playAudienceTopic(defaultTopicBase, "house", "play-1"), payload)

	row := heldPlay(t, db, "play-1")
	if row.Player != "living-room" || row.Season != 3 {
		t.Errorf("row = %+v, want what the audience named", row)
	}
	if people := heldPeople(t, db, "play-1"); people["chris"] != 1 {
		t.Errorf("people = %v, want chris", people)
	}
	if aliases := heldAliases(t, db, "play-1"); aliases["tmdb"] != "2316" {
		t.Errorf("aliases = %v, want the tmdb id", aliases)
	}
}

// An empty audience is the operator clearing the topic after it
// released the Play. The rows are the history, so nothing changes.
func TestAnEmptyAudienceLeavesTheRowsAlone(t *testing.T) {
	role, db := recordingProgress(t)
	payload, _ := json.Marshal(playAudience{Player: "living-room", People: []string{"chris"}})
	topic := playAudienceTopic(defaultTopicBase, "house", "play-1")
	role.onMessage(topic, payload)

	role.onMessage(topic, nil)

	if heldPlay(t, db, "play-1").Player != "living-room" {
		t.Error("the clear took the Play's row")
	}
	if people := heldPeople(t, db, "play-1"); people["chris"] != 1 {
		t.Errorf("people = %v, want the row the clear left alone", people)
	}
}

// One outside message writes the whole row, because the jellyfin role carries
// the audience and the position in the one message.
func TestAnOutsideMessageWritesTheWholeRow(t *testing.T) {
	role, db := recordingProgress(t)
	payload, _ := json.Marshal(outsidePlay{
		Player: "jellyfin", People: []string{"chris"},
		Aliases:  map[string]string{"tmdb": "603"},
		Position: 4210, Duration: 8160, At: 1_757_300_000,
	})

	role.onMessage(playOutsideTopic(defaultTopicBase, "house", "jellyfin-7-19"), payload)

	row := heldPlay(t, db, "jellyfin-7-19")
	if row.Player != "jellyfin" || row.Position != 4210 || row.Recorded != 1_757_300_000 {
		t.Errorf("row = %+v, want the outside play the message named", row)
	}
	if people := heldPeople(t, db, "jellyfin-7-19"); people["chris"] != 1 {
		t.Errorf("people = %v, want chris", people)
	}
	if aliases := heldAliases(t, db, "jellyfin-7-19"); aliases["tmdb"] != "603" {
		t.Errorf("aliases = %v, want the tmdb id", aliases)
	}
}

// An empty message is a cleared topic on every kind the role records, and a
// clear writes no row.
func TestAnEmptyMessageWritesNoRow(t *testing.T) {
	cases := []struct {
		name  string
		topic string
	}{
		{name: "a position", topic: mediaPlayStatusTopic("house", "play-1")},
		{name: "a final status", topic: playFinalTopic(defaultTopicBase, "house", "play-1")},
		{name: "an outside play", topic: playOutsideTopic(defaultTopicBase, "house", "play-1")},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			role, db := recordingProgress(t)

			role.onMessage(testCase.topic, nil)

			if heldPlay(t, db, "play-1").Play != "" {
				t.Errorf("the role wrote a row from an empty message on %q", testCase.topic)
			}
		})
	}
}

// The final status marks the row ended, which is the mark the operator
// reads before it releases the Play's finalizer.
func TestAFinalMessageMarksTheRowEnded(t *testing.T) {
	role, db := recordingProgress(t)

	payload, _ := json.Marshal(playFinal{Phase: "Finished", Item: 1, Position: "0:59:59", Duration: "1:00:00"})
	role.onMessage(playFinalTopic(defaultTopicBase, "house", "play-1"), payload)

	row := heldPlay(t, db, "play-1")
	if row.Phase != "Finished" || row.Position != 3599 || row.Duration != 3600 {
		t.Errorf("row = %+v, want the final status", row)
	}
	if row.Ended == 0 {
		t.Error("the row is not marked ended")
	}
}

// A forget message takes the person out of every Play in the store.
func TestAForgetMessageTakesThePersonOutOfEveryPlay(t *testing.T) {
	role, db := recordingProgress(t)
	payload, _ := json.Marshal(playAudience{People: []string{"chris", "thora"}})
	role.onMessage(playAudienceTopic(defaultTopicBase, "house", "play-1"), payload)

	role.onMessage(personForgetTopic(defaultTopicBase, "thora"), []byte(`{"at":"2026-09-06T21:00:00Z"}`))

	people := heldPeople(t, db, "play-1")
	if len(people) != 1 || people["chris"] != 1 {
		t.Errorf("people = %v, want chris alone", people)
	}
}

// servingProgress is one role on a broker the test reads, so the
// retained messages it publishes travel a real connection.
func servingProgress(t *testing.T) (*progress, *fakeBroker, *sql.DB) {
	t.Helper()
	db := newSQLiteProgress(t)
	server := httptest.NewServer(&sqliteAgent{db: db})
	t.Cleanup(server.Close)
	address, accepted := testBroker(t)
	shorterBackoff(t)
	graceWas := progressFlushGrace
	t.Cleanup(func() { progressFlushGrace = graceWas })
	progressFlushGrace = 5 * time.Millisecond

	role := newProgressOn("house", defaultTopicBase, defaultMediaTopicBase,
		newProgressStore(server.URL, server.Client()), io.Discard)
	role.bus = newBus(address, "progress-house",
		&busWill{Topic: role.availabilityTopic, Payload: []byte(availabilityOffline), Retained: true},
		role.onConnect, role.onMessage)
	role.subscribe()

	stopped, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		role.serve(stopped)
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})
	return role, waitForBroker(t, accepted), db
}

// The role publishes what it recorded for one Play, retained, so the
// operator reads the last position back after a restart.
func TestTheRolePublishesWhatItRecorded(t *testing.T) {
	role, broker, _ := servingProgress(t)
	waitForTopic(t, broker, role.availabilityTopic)

	role.onMessage(mediaPlayStatusTopic("house", "play-1"),
		[]byte(`{"item":0,"position":"0:10:00","duration":"1:30:00"}`))

	published := waitForTopic(t, broker, playRecordedTopic(defaultTopicBase, "house", "play-1"))
	if !published.retained {
		t.Error("the recorded message is not retained")
	}
	var recorded playRecorded
	if err := json.Unmarshal(published.payload, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Position != "0:10:00" || recorded.Ended {
		t.Errorf("recorded = %+v, want the position of a Play still running", recorded)
	}
	if recorded.At == "" {
		t.Error("the recorded message names no time")
	}
}

// The final publishes the same message with the ended mark, which is
// what releases the Play.
func TestTheRolePublishesTheEndedMark(t *testing.T) {
	role, broker, _ := servingProgress(t)
	waitForTopic(t, broker, role.availabilityTopic)

	payload, _ := json.Marshal(playFinal{Phase: "Finished", Item: 1, Position: "1:00:00", Duration: "1:00:00"})
	role.onMessage(playFinalTopic(defaultTopicBase, "house", "play-1"), payload)

	published := waitForTopic(t, broker, playRecordedTopic(defaultTopicBase, "house", "play-1"))
	var recorded playRecorded
	if err := json.Unmarshal(published.payload, &recorded); err != nil {
		t.Fatal(err)
	}
	if !recorded.Ended || recorded.Item != 1 {
		t.Errorf("recorded = %+v, want the ended mark", recorded)
	}
}

// The role answers a forget request with one message per namespace, so
// the operator releases the Person's finalizer once every store has
// answered.
func TestTheRoleAnswersAForgetRequest(t *testing.T) {
	role, broker, _ := servingProgress(t)
	waitForTopic(t, broker, role.availabilityTopic)

	role.onMessage(personForgetTopic(defaultTopicBase, "thora"), []byte(`{"at":"2026-09-06T21:00:00Z"}`))

	published := waitForTopic(t, broker, personForgottenTopic(defaultTopicBase, "thora", "house"))
	if !published.retained {
		t.Error("the forgotten message is not retained")
	}
	var answer struct {
		At string `json:"at"`
	}
	if err := json.Unmarshal(published.payload, &answer); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, answer.At); err != nil {
		t.Errorf("at = %q, want an RFC 3339 time: %v", answer.At, err)
	}
}

// An empty forget request is the operator's clear, and the role clears
// its own answer with it.
func TestAnEmptyForgetClearsTheAnswer(t *testing.T) {
	role, broker, _ := servingProgress(t)
	waitForTopic(t, broker, role.availabilityTopic)

	role.onMessage(personForgetTopic(defaultTopicBase, "thora"), nil)

	published := waitForTopic(t, broker, personForgottenTopic(defaultTopicBase, "thora", "house"))
	if len(published.payload) != 0 || !published.retained {
		t.Errorf("message = %q retained %v, want a retained empty payload", published.payload, published.retained)
	}
}

// A position the role cannot read records zero and says so in the pod
// log, because a report of a shape nobody expected must not stop the
// rest.
func TestAMalformedPositionRecordsZero(t *testing.T) {
	db := newSQLiteProgress(t)
	server := httptest.NewServer(&sqliteAgent{db: db})
	t.Cleanup(server.Close)
	var logged strings.Builder
	role := newProgressOn("house", defaultTopicBase, defaultMediaTopicBase,
		newProgressStore(server.URL, server.Client()), &logged)
	role.bus = newBus("nowhere", "progress-house", nil, nil, role.onMessage)

	role.onMessage(mediaPlayStatusTopic("house", "play-1"),
		[]byte(`{"item":0,"position":"halfway","duration":"1:00:00"}`))

	if position := heldPlay(t, db, "play-1").Position; position != 0 {
		t.Errorf("position = %d, want 0 from a value the role cannot read", position)
	}
	if !strings.Contains(logged.String(), "halfway") {
		t.Errorf("log = %q, want the value it could not read", logged.String())
	}
}

// mediaPlayStatusTopic is one Play's status topic in media-operator's
// tree, which the tests publish onto the way its playback sidecar does.
func mediaPlayStatusTopic(namespace, play string) string {
	return defaultMediaTopicBase + "/plays/" + namespace + "/" + play + "/" + mediaStatusKind
}

// H:MM:SS is the shape every position on the bus carries, and the store
// holds seconds, so the two conversions are one pair.
func TestPositionsReadAndWriteAsSeconds(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		seconds int
		ok      bool
	}{
		{name: "the start", value: "0:00:00", seconds: 0, ok: true},
		{name: "a minute in", value: "0:01:30", seconds: 90, ok: true},
		{name: "an hour in", value: "1:01:05", seconds: 3665, ok: true},
		{name: "more hours than a digit", value: "27:00:00", seconds: 97200, ok: true},
		{name: "nothing at all", value: "", seconds: 0, ok: true},
		{name: "two parts", value: "10:00", seconds: 0},
		{name: "a word", value: "halfway", seconds: 0},
		{name: "a negative", value: "-1:00:00", seconds: 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			seconds, ok := parsePosition(testCase.value)
			if seconds != testCase.seconds || ok != testCase.ok {
				t.Errorf("parsePosition(%q) = %d, %v, want %d, %v",
					testCase.value, seconds, ok, testCase.seconds, testCase.ok)
			}
		})
	}
}

func TestSecondsWriteBackAsHoursMinutesAndSeconds(t *testing.T) {
	cases := []struct {
		seconds int
		want    string
	}{
		{seconds: 0, want: "0:00:00"},
		{seconds: 90, want: "0:01:30"},
		{seconds: 3665, want: "1:01:05"},
		{seconds: 97200, want: "27:00:00"},
	}
	for _, testCase := range cases {
		t.Run(testCase.want, func(t *testing.T) {
			if got := formatPosition(testCase.seconds); got != testCase.want {
				t.Errorf("formatPosition(%d) = %q, want %q", testCase.seconds, got, testCase.want)
			}
		})
	}
}
