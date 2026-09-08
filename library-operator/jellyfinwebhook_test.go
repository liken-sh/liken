package main

// What these tests prove: what one post of the Webhook plugin becomes
// on the bus, which posts the role drops, the status each post is
// answered with, and the two paths the role answers.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// One post of the plugin, as JSON. Every value is a string, because a
// Handlebars variable the server does not hold renders empty.
func jellyfinPost(t *testing.T, event jellyfinEvent) []byte {
	t.Helper()
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("building the post: %v", err)
	}
	return body
}

// A film and an episode become one outside play each, with the
// positions in seconds and the identity the catalog gives the work.
func TestAPostBecomesAnOutsidePlay(t *testing.T) {
	at := newJellyfinClock().at.Unix()
	for _, test := range []struct {
		name  string
		event jellyfinEvent
		play  string
		want  outsidePlay
	}{
		{
			name: "a film in flight",
			event: jellyfinEvent{Event: jellyfinProgressEvent, User: "chris", UserID: "u1", ItemID: "i1",
				ItemType: "Movie", PositionTicks: "42100000000", RunTimeTicks: "81600000000",
				Paused: "False", Tmdb: "603", Imdb: "tt0133093"},
			play: "jellyfin-u1-i1",
			want: outsidePlay{Player: "jellyfin", People: []string{"chris"},
				Aliases:  map[string]string{"tmdb": "603", "imdb": "tt0133093"},
				Position: 4210, Duration: 8160, At: at},
		},
		{
			name: "an episode in flight, under the series' ids",
			event: jellyfinEvent{Event: jellyfinProgressEvent, User: "chris", UserID: "u1", ItemID: "i2",
				ItemType: "Episode", SeriesID: "item-office", Season: "3", Episode: "5",
				PositionTicks: "6000000000", RunTimeTicks: "13200000000", Tmdb: "999"},
			play: "jellyfin-u1-i2",
			want: outsidePlay{Player: "jellyfin", People: []string{"chris"},
				Aliases: map[string]string{"tmdb": "2316", "tvdb": "73244"},
				Season:  3, Episode: 5, Position: 600, Duration: 1320, At: at},
		},
		{
			name: "a stop, which marks the row ended",
			event: jellyfinEvent{Event: jellyfinStopEvent, User: "chris", UserID: "u1", ItemID: "i1",
				ItemType: "Movie", PositionTicks: "81600000000", RunTimeTicks: "81600000000",
				PlayedToCompletion: "True", Tmdb: "603"},
			play: "jellyfin-u1-i1",
			want: outsidePlay{Player: "jellyfin", People: []string{"chris"},
				Aliases:  map[string]string{"tmdb": "603"},
				Position: 8160, Duration: 8160, Ended: true, At: at},
		},
		{
			name: "a start, with every empty field the template renders",
			event: jellyfinEvent{Event: jellyfinStartEvent, User: "chris", UserID: "u1", ItemID: "i1",
				ItemType: "Movie", PositionTicks: "", RunTimeTicks: "", Season: "", Episode: "",
				Paused: "", PlayedToCompletion: "", Tmdb: "603", Imdb: "", Tvdb: ""},
			play: "jellyfin-u1-i1",
			want: outsidePlay{Player: "jellyfin", People: []string{"chris"},
				Aliases: map[string]string{"tmdb": "603"}, At: at},
		},
		{
			name: "a film played to the end on another event",
			event: jellyfinEvent{Event: jellyfinProgressEvent, User: "chris", UserID: "u1", ItemID: "i1",
				ItemType: "Movie", PlayedToCompletion: "true", Tvdb: "73244"},
			play: "jellyfin-u1-i1",
			want: outsidePlay{Player: "jellyfin", People: []string{"chris"},
				Aliases: map[string]string{"tvdb": "73244"}, Ended: true, At: at},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			role, _, _ := standJellyfinRole(t, jellyfinFixture())

			play, got, ok := role.outsideOf(t.Context(), test.event)

			if !ok {
				t.Fatal("the role dropped a post it can map")
			}
			if play != test.play {
				t.Errorf("play = %q, want %q", play, test.play)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("play = %+v, want %+v", got, test.want)
			}
		})
	}
}

// A post the role cannot map is dropped with a line, because nothing in
// it names a person or a work.
func TestAPostTheRoleCannotMapIsDropped(t *testing.T) {
	for _, test := range []struct {
		name  string
		event jellyfinEvent
	}{
		{name: "no user", event: jellyfinEvent{UserID: "u1", ItemID: "i1", ItemType: "Movie", Tmdb: "603"}},
		{name: "no user id", event: jellyfinEvent{User: "chris", ItemID: "i1", ItemType: "Movie", Tmdb: "603"}},
		{name: "no item id", event: jellyfinEvent{User: "chris", UserID: "u1", ItemType: "Movie", Tmdb: "603"}},
		{name: "a film with no provider ids",
			event: jellyfinEvent{User: "chris", UserID: "u1", ItemID: "i1", ItemType: "Movie"}},
		{name: "an episode that names no series",
			event: jellyfinEvent{User: "chris", UserID: "u1", ItemID: "i1", ItemType: "Episode", Tmdb: "999"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			role, _, logged := standJellyfinRole(t, jellyfinFixture())

			_, _, ok := role.outsideOf(t.Context(), test.event)

			if ok {
				t.Fatal("the role mapped a post that names no person or no work")
			}
			if !strings.Contains(logged.String(), "a jellyfin webhook") {
				t.Errorf("log = %q, want the post it dropped", logged.String())
			}
		})
	}
}

// The post goes out under the play the user and the item name, and it
// is not retained, because the next post repeats the position.
func TestAPostGoesOutOnTheOutsideTopic(t *testing.T) {
	role, messages, _ := standJellyfinRole(t, jellyfinFixture())
	body := jellyfinPost(t, jellyfinEvent{Event: jellyfinProgressEvent, User: "chris", UserID: "u1",
		ItemID: "i1", ItemType: "Movie", PositionTicks: "42100000000", Tmdb: "603"})

	answer := jellyfinPosted(t, role, body)

	if answer != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", answer, http.StatusNoContent)
	}
	topic, play := messages.outside(t)
	if want := playOutsideTopic(defaultTopicBase, "house", "jellyfin-u1-i1"); topic != want {
		t.Errorf("topic = %q, want %q", topic, want)
	}
	if play.Position != 4210 || play.People[0] != "chris" {
		t.Errorf("play = %+v, want the position and the person the post names", play)
	}
	if messages.held[0].retained {
		t.Error("the outside play was retained")
	}
}

// A body of another shape is a bad request, and a body the role cannot
// map is answered as read, because Jellyfin retries what it cannot
// deliver and nothing here improves on the next post.
func TestTheStatusEachPostIsAnsweredWith(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		status int
		sent   int
	}{
		{name: "a post the role maps", status: http.StatusNoContent, sent: 1,
			body: `{"event":"PlaybackProgress","user":"chris","userId":"u1","itemId":"i1","itemType":"Movie","tmdb":"603"}`},
		{name: "a body that is no JSON", status: http.StatusBadRequest, sent: 0, body: `{`},
		{name: "a body of another shape", status: http.StatusBadRequest, sent: 0, body: `["chris"]`},
		{name: "a post that names no work", status: http.StatusOK, sent: 0,
			body: `{"event":"PlaybackProgress","user":"chris","userId":"u1","itemId":"i1","itemType":"Movie"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			role, messages, _ := standJellyfinRole(t, jellyfinFixture())

			answer := jellyfinPosted(t, role, []byte(test.body))

			if answer != test.status {
				t.Errorf("status = %d, want %d", answer, test.status)
			}
			if len(messages.held) != test.sent {
				t.Errorf("messages = %d, want %d", len(messages.held), test.sent)
			}
		})
	}
}

// A post that carries a position this role wrote a moment ago is that
// write coming back, and recording it would move the row's recorded
// time forward for no new fact.
func TestThePostOfTheRolesOwnWriteIsDropped(t *testing.T) {
	role, messages, _ := standJellyfinRole(t, jellyfinFixture())
	role.out.echoes.remember("u1", "i1", 4210)

	for _, ticks := range []string{"42100000000", "42090000000", "42110000000"} {
		body := jellyfinPost(t, jellyfinEvent{Event: jellyfinProgressEvent, User: "chris", UserID: "u1",
			ItemID: "i1", ItemType: "Movie", PositionTicks: ticks, Tmdb: "603"})
		if answer := jellyfinPosted(t, role, body); answer != http.StatusOK {
			t.Errorf("status = %d, want %d", answer, http.StatusOK)
		}
	}

	if len(messages.held) != 0 {
		t.Errorf("messages = %+v, want none", messages.held)
	}
}

// A position two seconds from the last write is a person watching in
// Jellyfin, and it reaches the bus.
func TestAPostBeyondTheEchoWindowReachesTheBus(t *testing.T) {
	role, messages, _ := standJellyfinRole(t, jellyfinFixture())
	role.out.echoes.remember("u1", "i1", 4210)
	body := jellyfinPost(t, jellyfinEvent{Event: jellyfinProgressEvent, User: "chris", UserID: "u1",
		ItemID: "i1", ItemType: "Movie", PositionTicks: "42120000000", Tmdb: "603"})

	answer := jellyfinPosted(t, role, body)

	if answer != http.StatusNoContent || len(messages.held) != 1 {
		t.Errorf("status = %d with %d messages, want %d with one",
			answer, len(messages.held), http.StatusNoContent)
	}
}

// The role answers the endpoint the plugin posts to and the one the
// kubelet reads, and nothing else.
func TestThePathsTheRoleAnswers(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		path   string
		status int
	}{
		{name: "the probe", method: http.MethodGet, path: "/healthz", status: http.StatusOK},
		{name: "the endpoint", method: http.MethodPost, path: "/webhook", status: http.StatusBadRequest},
		{name: "the endpoint read", method: http.MethodGet, path: "/webhook", status: http.StatusMethodNotAllowed},
		{name: "the probe posted to", method: http.MethodPost, path: "/healthz", status: http.StatusMethodNotAllowed},
		{name: "any other path", method: http.MethodGet, path: "/elsewhere", status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			role, _, _ := standJellyfinRole(t, jellyfinFixture())
			server := httptest.NewServer(role.handler())
			t.Cleanup(server.Close)

			request, err := http.NewRequestWithContext(t.Context(), test.method, server.URL+test.path, nil)
			if err != nil {
				t.Fatalf("building the request: %v", err)
			}
			answer, err := server.Client().Do(request)
			if err != nil {
				t.Fatalf("posting: %v", err)
			}
			defer drain(answer.Body)

			if answer.StatusCode != test.status {
				t.Errorf("status = %d, want %d", answer.StatusCode, test.status)
			}
		})
	}
}

// Posts one body to the role's own endpoint and answers the status.
func jellyfinPosted(t *testing.T, role *jellyfin, body []byte) int {
	t.Helper()
	server := httptest.NewServer(role.handler())
	t.Cleanup(server.Close)

	answer, err := server.Client().Post(server.URL+"/webhook", jsonContentType, strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("posting: %v", err)
	}
	defer drain(answer.Body)
	return answer.StatusCode
}

// A field the template did not render is empty and reads as zero, and
// so does a field of any other shape.
func TestANumberOfThePayload(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  int64
	}{
		{name: "a position in ticks", value: "42100000000", want: 42100000000},
		{name: "a variable the template did not render", value: "", want: 0},
		{name: "a value with spaces around it", value: " 42 ", want: 42},
		{name: "a value of another shape", value: "soon", want: 0},
		{name: "a value below zero", value: "-1", want: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := jellyfinNumber(test.value); got != test.want {
				t.Errorf("number of %q = %d, want %d", test.value, got, test.want)
			}
		})
	}
}

// The server writes True and False, and a template of another make
// writes true and false, so the read is without case.
func TestABooleanOfThePayload(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "the server's own word", value: "True", want: true},
		{name: "the word in lower case", value: "true", want: true},
		{name: "the word with spaces around it", value: " TRUE ", want: true},
		{name: "the other word", value: "False", want: false},
		{name: "a variable the template did not render", value: "", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := jellyfinFlag(test.value); got != test.want {
				t.Errorf("flag of %q = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

// A season and an episode number are counts, and an empty one is no
// number at all.
func TestACountOfThePayload(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  int
	}{
		{name: "a season", value: "3", want: 3},
		{name: "no season", value: "", want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := jellyfinCount(test.value); got != test.want {
				t.Errorf("count of %q = %d, want %d", test.value, got, test.want)
			}
		})
	}
}
