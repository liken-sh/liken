package main

// What these tests prove: what the progress role and its store do when
// a message is not what it says, when the agent refuses, and when the
// API server is unwell. Nothing here stops the role, because the next
// report repeats the position and the operator's final closes the gap.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// refusingProgress is one role whose agent refuses every write and
// every read, so a test drives the failure each handler answers.
func refusingProgress(t *testing.T) (*progress, *strings.Builder) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "the agent is unwell")
	}))
	t.Cleanup(server.Close)

	logged := &strings.Builder{}
	role := newProgressOn("house", defaultTopicBase, defaultMediaTopicBase,
		newProgressStore(server.URL, server.Client()), logged)
	role.bus = newBus("nowhere", "progress-house", nil, nil, role.onMessage)
	return role, logged
}

// Each message an agent refuses leaves a line in the pod log and stops
// there, because the writes that follow it would name a row the store
// does not hold.
func TestARefusedWriteIsLoggedAndDropped(t *testing.T) {
	cases := []struct {
		name    string
		topic   string
		payload string
		says    string
	}{
		{
			name:    "a position",
			topic:   mediaPlayStatusTopic("house", "play-1"),
			payload: `{"item":0,"position":"0:01:00","duration":"1:00:00"}`,
			says:    "could not record the position",
		},
		{
			name:    "an audience",
			topic:   playAudienceTopic(defaultTopicBase, "house", "play-1"),
			payload: `{"player":"living-room"}`,
			says:    "could not record the audience",
		},
		{
			name:    "a final status",
			topic:   playFinalTopic(defaultTopicBase, "house", "play-1"),
			payload: `{"phase":"Finished","position":"1:00:00"}`,
			says:    "could not record the final status",
		},
		{
			name:    "a forget request",
			topic:   personForgetTopic(defaultTopicBase, "thora"),
			payload: `{"at":"2026-09-06T21:00:00Z"}`,
			says:    "could not forget thora",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			role, logged := refusingProgress(t)

			role.onMessage(testCase.topic, []byte(testCase.payload))

			if !strings.Contains(logged.String(), testCase.says) {
				t.Errorf("log = %q, want %q", logged.String(), testCase.says)
			}
		})
	}
}

// A payload that is not the message its topic names leaves a line in
// the log and writes nothing, so one bad publish costs one message.
func TestAMessageOfTheWrongShapeWritesNothing(t *testing.T) {
	cases := []struct {
		name  string
		topic string
		says  string
	}{
		{name: "a position", topic: mediaPlayStatusTopic("house", "play-1"), says: "reads as no report"},
		{name: "an audience", topic: playAudienceTopic(defaultTopicBase, "house", "play-1"),
			says: "reads as no audience"},
		{name: "a final status", topic: playFinalTopic(defaultTopicBase, "house", "play-1"),
			says: "reads as no status"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			role, db := recordingProgress(t)
			var logged strings.Builder
			role.log = &logged

			role.onMessage(testCase.topic, []byte("not a message"))

			if heldPlay(t, db, "play-1").Play != "" {
				t.Error("the role wrote a row from a payload it could not read")
			}
			if !strings.Contains(logged.String(), testCase.says) {
				t.Errorf("log = %q, want %q", logged.String(), testCase.says)
			}
		})
	}
}

// A topic the role does not record from writes nothing, so a broker
// that delivers more than the role asked for costs it nothing.
func TestAMessageOnAnotherTopicWritesNothing(t *testing.T) {
	cases := []struct {
		name  string
		topic string
	}{
		{name: "another namespace's audience", topic: playAudienceTopic(defaultTopicBase, "loft", "play-1")},
		{name: "another operator's tree", topic: "liken/somewhere/plays/house/play-1/audience"},
		{name: "a play recorded message", topic: playRecordedTopic(defaultTopicBase, "house", "play-1")},
		{name: "a person forgotten message", topic: personForgottenTopic(defaultTopicBase, "thora", "house")},
		{name: "a media availability report", topic: defaultMediaTopicBase + "/plays/house/play-1/availability"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			role, db := recordingProgress(t)

			role.onMessage(testCase.topic, []byte(`{"player":"living-room","item":1}`))

			if heldPlay(t, db, "play-1").Play != "" {
				t.Errorf("the role recorded a row from %q", testCase.topic)
			}
		})
	}
}

// A store that cannot answer the Watch of a Play publishes no
// projection, so the retained one stays where it is rather than reading
// as a Watch at the beginning.
func TestAFailedWatchReadPublishesNoProjection(t *testing.T) {
	role, logged := refusingProgress(t)

	role.publishWatch(t.Context(), "play-1")

	if !strings.Contains(logged.String(), "could not read the watch of play-1") {
		t.Errorf("log = %q, want the read it could not make", logged.String())
	}
}

// A Play the store holds a row for with no Watch publishes nothing, and
// so does a Watch with no row.
func TestAPlayInNoWatchPublishesNoProjection(t *testing.T) {
	role, _ := recordingProgress(t)
	if err := role.store.recordPosition(t.Context(), "play-1", 0, 10, 60, testRecordedAt); err != nil {
		t.Fatal(err)
	}

	// The bus is not connected, so a publish would be dropped anyway.
	// What this reads is that the role makes no read past the Watch.
	role.publishWatch(t.Context(), "play-1")
}

// A store that answers the Watch and then refuses the read of its
// latest Play publishes no projection, so a half-answered read never
// moves a Watch backward.
func TestAFailedLatestReadPublishesNoProjection(t *testing.T) {
	db := newSQLiteProgress(t)
	// The agent answers the first read and refuses the second, which is
	// the read of the Watch's latest Play.
	server := httptest.NewServer(&sqliteAgent{db: db, queriesLeft: 2})
	t.Cleanup(server.Close)
	var logged strings.Builder
	role := newProgressOn("house", defaultTopicBase, defaultMediaTopicBase,
		newProgressStore(server.URL, server.Client()), &logged)
	role.bus = newBus("nowhere", "progress-house", nil, nil, role.onMessage)
	if err := role.store.recordAudience(t.Context(), "play-1",
		playAudience{Watch: "the-office-with-the-girls"}, testRecordedAt); err != nil {
		t.Fatal(err)
	}

	role.publishWatch(t.Context(), "play-1")

	if !strings.Contains(logged.String(), "could not read the latest play") {
		t.Errorf("log = %q, want the read it could not make", logged.String())
	}
}

// A read the agent answers with an error event is a failure the store
// hands back, so a query that names a column the schema does not have
// never reads as an empty answer.
func TestTheStoreAnswersAnErrorEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"error":"no such column: nowhere"}`+"\n")
	}))
	t.Cleanup(server.Close)
	store := newProgressStore(server.URL, server.Client())

	_, err := store.watchOf(t.Context(), "play-1")

	if err == nil || !strings.Contains(err.Error(), "no such column") {
		t.Errorf("err = %v, want the agent's own message", err)
	}
}

// The agent answers one result per statement. A short answer is a
// failure and never a batch that applied.
func TestTheStoreRefusesAShortAnswer(t *testing.T) {
	cases := []struct {
		name string
		body string
		says string
	}{
		{name: "one result for two statements", body: `{"results":[{"rows_affected":1}]}`, says: "results for"},
		{name: "a per-statement error", body: `{"results":[{"error":"no such table"},{"rows_affected":0}]}`,
			says: "no such table"},
		{name: "an answer that is not JSON", body: `not json`, says: "decoding the answer"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, testCase.body)
			}))
			t.Cleanup(server.Close)
			store := newProgressStore(server.URL, server.Client())

			err := store.recordPosition(t.Context(), "play-1", 0, 1, 2, testRecordedAt)

			if err == nil || !strings.Contains(err.Error(), testCase.says) {
				t.Errorf("err = %v, want %q", err, testCase.says)
			}
		})
	}
}

// A store whose address is no address answers the failure rather than
// hiding it, on the write and on the read alike.
func TestTheStoreAnswersAnAddressItCannotReach(t *testing.T) {
	store := newProgressStore("http://127.0.0.1:1", &http.Client{Timeout: time.Second})

	if err := store.forgetPerson(t.Context(), "thora"); err == nil {
		t.Error("the store hid a write it could not send")
	}
	if _, _, err := store.latestForWatch(t.Context(), "a-watch"); err == nil {
		t.Error("the store hid a read it could not send")
	}
}

// A base that is no URL fails before the request goes out.
func TestTheStoreAnswersABaseItCannotBuildARequestFrom(t *testing.T) {
	store := newProgressStore("://nowhere", &http.Client{})

	if err := store.forgetPerson(t.Context(), "thora"); err == nil {
		t.Error("the store hid a request it could not build")
	}
}

// The claim and the pod both stop on the failure the API server gives,
// so a pass reports it and the next pass tries again.
func TestStandProgressStopsOnTheFailureTheAPIGives(t *testing.T) {
	cluster := newFakeCluster()
	catalog := seedCatalog(cluster, "house-catalog", "house")
	cluster.broken["/api/v1/namespaces/house/persistentvolumeclaims/house-catalog-progress-0"] =
		http.StatusInternalServerError

	if _, err := testOperator(t, cluster).standProgressPod(t.Context(), catalog, 0); err == nil {
		t.Error("the pass stood a progress pod over a claim it could not read")
	}
}

// A list the API server refuses is the failure the pass reports, so the
// slices it writes are the ones it could read the members for.
func TestListProgressMemberPodsAnswersTheFailure(t *testing.T) {
	cluster := newFakeCluster()
	cluster.broken["GET "+podsAllPath] = http.StatusInternalServerError

	if _, err := ListProgressMemberPods(t.Context(), testOperator(t, cluster).client); err == nil {
		t.Error("the list hid a refusal")
	}
}

// The media topic base is media-operator's own default until this
// operator's Deployment names another.
func TestTheMediaTopicBaseFallsBackToTheDefault(t *testing.T) {
	cases := []struct {
		name   string
		stated string
		want   string
	}{
		{name: "unset", stated: "", want: defaultMediaTopicBase},
		{name: "a cluster that moved the tree", stated: "attic/media", want: "attic/media"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := mediaTopicBaseOf(testCase.stated); got != testCase.want {
				t.Errorf("mediaTopicBaseOf(%q) = %q, want %q", testCase.stated, got, testCase.want)
			}
		})
	}
}

// A role built with no log writes nowhere rather than failing, and a
// negative position writes back as the start.
func TestTheRoleWithNoLogAndTheNegativePosition(t *testing.T) {
	(&progress{}).logf("nothing reads this")

	if got := formatPosition(-5); got != "0:00:00" {
		t.Errorf("formatPosition(-5) = %q, want the start", got)
	}
}

// The operator reaches every namespace's progress role through one
// filter, the way it reaches every reporter.
func TestTheProgressAvailabilityFilterReachesEveryNamespace(t *testing.T) {
	filter := progressAvailabilityFilter(defaultTopicBase)

	if filter != defaultTopicBase+"/progress/+/availability" {
		t.Errorf("filter = %q, want the wildcard over the namespace", filter)
	}
	if topic := progressAvailabilityTopic(defaultTopicBase, "house"); !strings.HasPrefix(topic,
		strings.TrimSuffix(filter, "+/availability")) {
		t.Errorf("the topic %q is not under the filter %q", topic, filter)
	}
}

// The client the role reaches its own agent with states no timeout,
// because every request bounds itself with a context.
func TestTheProgressClientBoundsNothingByItself(t *testing.T) {
	if client := defaultProgressClient(); client.Timeout != 0 {
		t.Errorf("timeout = %v, want none; a context bounds every request", client.Timeout)
	}
}
