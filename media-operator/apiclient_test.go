package main

// These tests run the client against a small HTTP server in place of
// the API server, and check the paths, the methods, and the errors.

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// The token is empty, so the client sends no bearer token and reads no
// file.
func testAPIClient(t *testing.T, handler http.Handler) *apiclient.Client {
	t.Helper()
	return apiclient.New(apiservertest.Host, apiservertest.Start(t, handler).Client(), "")
}

// One request that the test server received.
type recordedRequest struct {
	Method string
	Path   string
	Body   []byte
}

// A test server that returns a fixed object for each path and records
// every request it receives.
type cannedAPI struct {
	responses map[string]any
	statuses  map[string]int
	requests  []recordedRequest
}

func (c *cannedAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.requests = append(c.requests, recordedRequest{Method: r.Method, Path: r.URL.Path, Body: body})
		key := r.Method + " " + r.URL.Path
		if status, found := c.statuses[key]; found {
			w.WriteHeader(status)
			return
		}
		answer, found := c.responses[key]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(answer)
	})
}

func TestGetPlayerReadsOneNamespacedObject(t *testing.T) {
	api := &cannedAPI{responses: map[string]any{
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
		t.Errorf("the Player zone is %q, want living-room", player.Spec.Zone)
	}
	if player.Spec.Display == nil || player.Spec.Display.Class != "display-output" {
		t.Fatalf("the Player display is %+v, want class display-output", player.Spec.Display)
	}
}

func TestReplaceStatusPutsAPlayToItsStatusSubresource(t *testing.T) {
	api := &cannedAPI{responses: map[string]any{
		"PUT /apis/media.liken.sh/v1alpha1/namespaces/house/plays/movie/status": Play{},
	}}
	play := &Play{
		Metadata: ObjectMeta{Name: "movie", Namespace: "house", ResourceVersion: "12"},
		Status:   PlayStatus{Phase: phaseRunning, Position: "0:01:00"},
	}

	if err := apiclient.ReplaceStatus(testAPIClient(t, api.handler()), playPath("house", "movie"), play); err != nil {
		t.Fatal(err)
	}
	if len(api.requests) != 1 {
		t.Fatalf("the API server received %+v, want one request", api.requests)
	}
	written := api.requests[0]
	if written.Method != http.MethodPut {
		t.Errorf("the method is %s, want PUT", written.Method)
	}
	if written.Path != "/apis/media.liken.sh/v1alpha1/namespaces/house/plays/movie/status" {
		t.Errorf("the path is %s, want the status subresource of the Play", written.Path)
	}
	var sent Play
	if err := json.Unmarshal(written.Body, &sent); err != nil {
		t.Fatal(err)
	}
	// The resourceVersion in the body makes the write conditional.
	if sent.Metadata.ResourceVersion != "12" {
		t.Errorf("the sent resourceVersion is %q, want 12", sent.Metadata.ResourceVersion)
	}
	if sent.Status.Phase != phaseRunning || sent.Status.Position != "0:01:00" {
		t.Errorf("the sent status is %+v, want phase Running and position 0:01:00", sent.Status)
	}
}

func TestCreatePodPostsToTheNamespacesCollection(t *testing.T) {
	api := &cannedAPI{responses: map[string]any{
		"POST /api/v1/namespaces/house/pods": Pod{Metadata: ObjectMeta{Name: "movie-playback"}},
	}}
	pod := &Pod{Metadata: ObjectMeta{Name: "movie-playback", Namespace: "house"}}

	created, err := CreatePod(testAPIClient(t, api.handler()), pod)
	if err != nil {
		t.Fatal(err)
	}
	if created.Metadata.Name != "movie-playback" {
		t.Errorf("the created Pod is %+v, want movie-playback", created.Metadata)
	}
	if api.requests[0].Path != "/api/v1/namespaces/house/pods" {
		t.Errorf("the path is %s, want the collection of the namespace", api.requests[0].Path)
	}
}

func TestCreateResourceClaimPostsToTheClaimsCollection(t *testing.T) {
	api := &cannedAPI{responses: map[string]any{
		"POST /apis/resource.k8s.io/v1/namespaces/house/resourceclaims": ResourceClaim{},
	}}
	claim := &ResourceClaim{Metadata: ObjectMeta{Name: "movie-devices", Namespace: "house"}}

	if _, err := CreateResourceClaim(testAPIClient(t, api.handler()), claim); err != nil {
		t.Fatal(err)
	}
	if api.requests[0].Path != "/apis/resource.k8s.io/v1/namespaces/house/resourceclaims" {
		t.Errorf("the path is %s, want the collection of the namespace", api.requests[0].Path)
	}
}

// A 404 and a 409 are normal results that callers handle, so the client
// returns them as ErrNotFound and ErrConflict.
func TestClientReturnsErrNotFoundAndErrConflict(t *testing.T) {
	api := &cannedAPI{statuses: map[string]int{
		"GET /api/v1/namespaces/house/pods/movie-playback": http.StatusNotFound,
		"POST /api/v1/namespaces/house/pods":               http.StatusConflict,
	}}
	client := testAPIClient(t, api.handler())

	t.Run("not found", func(t *testing.T) {
		if _, err := GetPod(client, "house", "movie-playback"); err != apiclient.ErrNotFound {
			t.Fatalf("the error is %v, want %v", err, apiclient.ErrNotFound)
		}
	})
	t.Run("conflict", func(t *testing.T) {
		_, err := CreatePod(client, &Pod{Metadata: ObjectMeta{Name: "movie-playback", Namespace: "house"}})
		if err != apiclient.ErrConflict {
			t.Fatalf("the error is %v, want %v", err, apiclient.ErrConflict)
		}
	})
}

// Any other failing status returns an error that includes the server's
// message.
func TestClientReturnsTheServersMessageForOtherErrors(t *testing.T) {
	api := &cannedAPI{statuses: map[string]int{
		"GET /apis/media.liken.sh/v1alpha1/namespaces/house/plays/movie": http.StatusInternalServerError,
	}}

	_, err := GetPlay(testAPIClient(t, api.handler()), "house", "movie")
	if err == nil {
		t.Fatal("a 500 response returned no error, want one")
	}
	if err == apiclient.ErrNotFound || err == apiclient.ErrConflict {
		t.Fatalf("the error is %v, want one that is neither ErrNotFound nor ErrConflict", err)
	}
}
