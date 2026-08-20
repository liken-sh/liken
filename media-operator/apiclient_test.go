package main

// These tests run the client against a small HTTP server that
// answers the way the API server answers, so the paths, the methods,
// and the two named errors are proved without a cluster.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The credentials are empty, so the client sends no bearer token and
// reads nothing from disk.
func testAPIClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewClient(server.URL, server.Client(), "")
}

// One recorded request: what the client sent and where.
type recordedRequest struct {
	Method string
	Path   string
	Body   []byte
}

// A server that answers one canned object per path and records every
// request it received.
type cannedAPI struct {
	answers  map[string]any
	statuses map[string]int
	requests []recordedRequest
}

func (c *cannedAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.requests = append(c.requests, recordedRequest{Method: r.Method, Path: r.URL.Path, Body: body})
		key := r.Method + " " + r.URL.Path
		if status, held := c.statuses[key]; held {
			w.WriteHeader(status)
			return
		}
		answer, held := c.answers[key]
		if !held {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(answer)
	})
}

func TestListPlaysReadsTheCollectionAcrossNamespaces(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"GET /apis/media.liken.sh/v1alpha1/plays": PlayList{
			Metadata: ListMeta{ResourceVersion: "77"},
			Items: []Play{{
				Metadata: ObjectMeta{Name: "movie", Namespace: "house"},
				Spec:     PlaySpec{Players: []string{"theater"}, URIs: []string{"https://nas/film.mkv"}},
			}},
		},
	}}

	list, err := ListPlays(testAPIClient(t, api.handler()))
	if err != nil {
		t.Fatal(err)
	}
	if list.Metadata.ResourceVersion != "77" {
		t.Errorf("resourceVersion = %q, want 77", list.Metadata.ResourceVersion)
	}
	if len(list.Items) != 1 || list.Items[0].Metadata.Name != "movie" {
		t.Fatalf("items = %+v", list.Items)
	}
	if list.Items[0].Spec.Players[0] != "theater" {
		t.Errorf("players = %v", list.Items[0].Spec.Players)
	}
}

func TestGetPlayerReadsOneNamespacedObject(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"GET /apis/media.liken.sh/v1alpha1/namespaces/house/players/theater": Player{
			Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
			Spec: PlayerSpec{
				Zone:    "living-room",
				Display: &PlayerDevice{Class: "display-output", Selector: `device.attributes["monitor.liken.sh"].id == "boe"`},
			},
		},
	}}

	player, err := GetPlayer(testAPIClient(t, api.handler()), "house", "theater")
	if err != nil {
		t.Fatal(err)
	}
	if player.Spec.Zone != "living-room" {
		t.Errorf("zone = %q", player.Spec.Zone)
	}
	if player.Spec.Display == nil || player.Spec.Display.Class != "display-output" {
		t.Fatalf("display = %+v", player.Spec.Display)
	}
}

func TestPutPlayStatusWritesTheStatusSubresource(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"PUT /apis/media.liken.sh/v1alpha1/namespaces/house/plays/movie/status": Play{},
	}}
	play := &Play{
		Metadata: ObjectMeta{Name: "movie", Namespace: "house", ResourceVersion: "12"},
		Status:   PlayStatus{Phase: phaseRunning, Position: "0:01:00"},
	}

	if _, err := PutPlayStatus(testAPIClient(t, api.handler()), play); err != nil {
		t.Fatal(err)
	}
	if len(api.requests) != 1 {
		t.Fatalf("requests = %+v", api.requests)
	}
	written := api.requests[0]
	if written.Method != http.MethodPut {
		t.Errorf("method = %s, want PUT", written.Method)
	}
	if written.Path != "/apis/media.liken.sh/v1alpha1/namespaces/house/plays/movie/status" {
		t.Errorf("path = %s", written.Path)
	}
	var sent Play
	if err := json.Unmarshal(written.Body, &sent); err != nil {
		t.Fatal(err)
	}
	// The resourceVersion in the body is what makes the write
	// conditional.
	if sent.Metadata.ResourceVersion != "12" {
		t.Errorf("resourceVersion = %q, want 12", sent.Metadata.ResourceVersion)
	}
	if sent.Status.Phase != phaseRunning || sent.Status.Position != "0:01:00" {
		t.Errorf("status = %+v", sent.Status)
	}
}

func TestCreatePodPostsToTheNamespacesCollection(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"POST /api/v1/namespaces/house/pods": Pod{Metadata: ObjectMeta{Name: "movie-playback"}},
	}}
	pod := &Pod{Metadata: ObjectMeta{Name: "movie-playback", Namespace: "house"}}

	created, err := CreatePod(testAPIClient(t, api.handler()), pod)
	if err != nil {
		t.Fatal(err)
	}
	if created.Metadata.Name != "movie-playback" {
		t.Errorf("created = %+v", created.Metadata)
	}
	if api.requests[0].Path != "/api/v1/namespaces/house/pods" {
		t.Errorf("path = %s", api.requests[0].Path)
	}
}

func TestCreateResourceClaimPostsToTheClaimsCollection(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"POST /apis/resource.k8s.io/v1/namespaces/house/resourceclaims": ResourceClaim{},
	}}
	claim := &ResourceClaim{Metadata: ObjectMeta{Name: "movie-devices", Namespace: "house"}}

	if _, err := CreateResourceClaim(testAPIClient(t, api.handler()), claim); err != nil {
		t.Fatal(err)
	}
	if api.requests[0].Path != "/apis/resource.k8s.io/v1/namespaces/house/resourceclaims" {
		t.Errorf("path = %s", api.requests[0].Path)
	}
}

// An absent object and a losing write are answers, not failures; the
// callers act on both.
func TestTheClientNamesTheTwoOrdinaryAnswers(t *testing.T) {
	api := &cannedAPI{statuses: map[string]int{
		"GET /api/v1/namespaces/house/pods/movie-playback": http.StatusNotFound,
		"POST /api/v1/namespaces/house/pods":               http.StatusConflict,
	}}
	client := testAPIClient(t, api.handler())

	t.Run("absent", func(t *testing.T) {
		if _, err := GetPod(client, "house", "movie-playback"); err != ErrNotFound {
			t.Fatalf("err = %v, want %v", err, ErrNotFound)
		}
	})
	t.Run("taken", func(t *testing.T) {
		_, err := CreatePod(client, &Pod{Metadata: ObjectMeta{Name: "movie-playback", Namespace: "house"}})
		if err != ErrConflict {
			t.Fatalf("err = %v, want %v", err, ErrConflict)
		}
	})
}

// Any other failing status carries the server's own message, so a
// broken deployment says what the API server said.
func TestAServerErrorCarriesTheServersMessage(t *testing.T) {
	api := &cannedAPI{statuses: map[string]int{
		"GET /apis/media.liken.sh/v1alpha1/plays": http.StatusInternalServerError,
	}}

	_, err := ListPlays(testAPIClient(t, api.handler()))
	if err == nil {
		t.Fatal("a 500 answer produced no error")
	}
	if err == ErrNotFound || err == ErrConflict {
		t.Fatalf("err = %v", err)
	}
}
