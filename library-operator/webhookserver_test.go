package main

// what these tests read: the endpoint the media servers post to, the
// paths it holds for the next pass, and the address the operator
// reports on every Library.

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// a webhook reaches the Library its path names, and the changed path
// out of the payload is held for the next pass.
func TestWebhookHandlerHoldsThePathThePayloadNames(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "a Radarr import",
			payload: `{"movieFile":{"path":"/movies/Arrival (2016)/Arrival.mkv"}}`,
			want:    "/movies/Arrival (2016)/Arrival.mkv",
		},
		{
			name:    "a Jellyfin item",
			payload: `{"Path":"/media/movies/Arrival (2016)"}`,
			want:    "/media/movies/Arrival (2016)",
		},
		{
			name:    "a payload with no path at all",
			payload: `{"eventType":"Test"}`,
			want:    "",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			operator := testOperator(t, newFakeCluster())

			recorded := postWebhook(t, operator, "/webhook/house/movies", one.payload)

			if recorded.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", recorded.Code, http.StatusNoContent)
			}
			held := operator.paths.held("house", "movies")
			if len(held) != 1 || held[0] != one.want {
				t.Errorf("held = %q, want %q", held, one.want)
			}
		})
	}
}

// postWebhook sends one webhook to the operator's own endpoint and
// answers with what it wrote back.
func postWebhook(t *testing.T, operator *operator, path, payload string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload))
	recorded := httptest.NewRecorder()
	operator.webhookHandler().ServeHTTP(recorded, request)
	return recorded
}

// a request that names no Library, and a method that is not a post,
// hold nothing.
func TestWebhookHandlerRefusesWhatItCannotServe(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{name: "a read", method: http.MethodGet, path: "/webhook/house/movies",
			status: http.StatusMethodNotAllowed},
		{name: "no library at all", method: http.MethodPost, path: "/webhook/house",
			status: http.StatusNotFound},
		{name: "an empty namespace", method: http.MethodPost, path: "/webhook//movies",
			status: http.StatusNotFound},
		{name: "another path", method: http.MethodPost, path: "/healthz",
			status: http.StatusNotFound},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			operator := testOperator(t, newFakeCluster())
			request := httptest.NewRequest(one.method, one.path, strings.NewReader("{}"))
			recorded := httptest.NewRecorder()

			operator.webhookHandler().ServeHTTP(recorded, request)

			if recorded.Code != one.status {
				t.Errorf("status = %d, want %d", recorded.Code, one.status)
			}
			if len(operator.paths.held("house", "movies")) != 0 {
				t.Error("a request the endpoint refused still held a path")
			}
		})
	}
}

// one path is held once, however many webhooks name it, and a sender
// that floods the endpoint costs one full walk rather than unbounded
// memory.
func TestHeldPathsAreASetWithACeiling(t *testing.T) {
	held := newHeldPaths(make(chan struct{}, 1))

	held.hold("house", "movies", "/one")
	held.hold("house", "movies", "/one")
	held.hold("house", "movies", "/two")

	if got := held.held("house", "movies"); len(got) != 2 || got[0] != "/one" || got[1] != "/two" {
		t.Errorf("held = %q, want the two paths in order", got)
	}

	for index := range heldPathLimit + 10 {
		held.hold("house", "movies", string(rune('a'+index%26))+string(rune('a'+index/26)))
	}

	if got := held.held("house", "movies"); len(got) != 1 || got[0] != "" {
		t.Errorf("held = %q, want the full walk that covers them all", got)
	}
}

// a path is released once its Job exists, and paths held for a Library
// the collection no longer holds are dropped.
func TestHeldPathsAreReleasedAndRetained(t *testing.T) {
	held := newHeldPaths(make(chan struct{}, 1))
	held.hold("house", "movies", "/one")
	held.hold("house", "gone", "/two")

	held.release("house", "movies", "/one")
	held.retain(map[string]bool{"house/movies": true})

	if got := held.held("house", "movies"); len(got) != 0 {
		t.Errorf("held = %q, want none after the release", got)
	}
	if got := held.held("house", "gone"); len(got) != 0 {
		t.Errorf("held = %q, want none for a Library the collection does not hold", got)
	}
}

// a webhook wakes the loop, so the Job is created on the next pass and
// not on the next tick.
func TestHoldingAPathWakesTheLoop(t *testing.T) {
	woken := make(chan struct{}, 1)
	held := newHeldPaths(woken)

	held.hold("house", "movies", "/one")

	select {
	case <-woken:
	case <-time.After(time.Second):
		t.Fatal("the webhook woke no pass")
	}
}

// the address names the operator's own Service and the Library, and no
// pod, so it holds for the whole life of the Library.
func TestWebhookURLNamesTheOperatorsService(t *testing.T) {
	got := webhookURL("liken-system", "house", "movies")

	if got != "http://library-operator.liken-system.svc/webhook/house/movies" {
		t.Errorf("webhookURL = %q, want the operator's own address", got)
	}
}

// the server runs until the operator stops, and it stops with it.
func TestServeWebhooksRunsUntilTheOperatorStops(t *testing.T) {
	operator := testOperator(t, newFakeCluster())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	stopped, stop := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- operator.serveWebhooks(stopped, address) }()

	waitForWebhook(t, address, "/webhook/house/movies")
	stop()

	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("err = %v, want a clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the webhook server did not stop with the operator")
	}
}

// waitForWebhook posts to the endpoint until it answers, so the test
// never races the listener coming up.
func waitForWebhook(t *testing.T, address, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Post("http://"+address+path, "application/json", strings.NewReader("{}"))
		if err == nil {
			drain(response.Body)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the webhook endpoint never answered")
}

// an address nothing can listen on ends the server, and the operator
// reports it rather than standing with an address nothing answers.
func TestServeWebhooksReportsAnAddressItCannotBind(t *testing.T) {
	operator := testOperator(t, newFakeCluster())

	err := operator.serveWebhooks(context.Background(), "127.0.0.1:-1")

	if err == nil {
		t.Fatal("err = nil, want the failure to listen")
	}
}

// A full walk held after a folder path replaces the set, because a walk
// covers every path. Two held paths would stand two scan Jobs for one claim
// that admits one writer, and the second would wait out the first for
// nothing.
func TestAFullWalkHeldAfterAPathReplacesIt(t *testing.T) {
	held := newHeldPaths(nil)

	held.hold("house", "movies", "/media/movies/One (2001)")
	held.hold("house", "movies", "/media/movies/Two (2002)")
	held.hold("house", "movies", "")

	if paths := held.held("house", "movies"); !slices.Equal(paths, []string{""}) {
		t.Errorf("the held paths are %v, want the full walk alone", paths)
	}
}
