package eventstest_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// sendTo sends one request through the server and answers its status
// and body.
func sendTo(t *testing.T, server *apiservertest.Server, method, path, contentType, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, apiservertest.Host+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	answer, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(answer))
}

const podEvent = `{"metadata":{"generateName":"east.","namespace":"observatory"},"involvedObject":{"kind":"Pod","namespace":"observatory","name":"east"},"reason":"Created","type":"Normal","count":1}`

// echo answers each request with its method and its path, the way a
// test's own fake would answer it.
var echo = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, r.Method+" "+r.URL.Path)
})

// Around answers each Event request itself and hands every other
// request to the test's own fake.
func TestEventsServesOnlyEventRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		held := &eventstest.Events{}
		server := apiservertest.Start(t, held.Around(echo))

		created, _ := sendTo(t, server, http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json", podEvent)
		_, other := sendTo(t, server, http.MethodGet, "/api/v1/namespaces/observatory/pods", "", "")

		if created != http.StatusCreated || len(held.About("Pod", "east")) != 1 || len(held.About("Pod", "west")) != 0 {
			t.Errorf("the create answered %d and the fake holds %+v, want 201 and one Event about Pod east", created, held.List())
		}
		if other != "GET /api/v1/namespaces/observatory/pods" {
			t.Errorf("the other request reached %q, want the test's fake", other)
		}
	})
}

// The fake refuses what the API server refuses.
func TestEventsRefusesWhatTheAPIServerRefuses(t *testing.T) {
	cases := []struct {
		name, method, path, contentType, body string
		want                                  int
	}{
		{"a body that is not JSON", http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json", "{", http.StatusBadRequest},
		{"an Event in another namespace than the request", http.MethodPost, "/api/v1/namespaces/default/events", "application/json", podEvent, http.StatusBadRequest},
		{"a cluster-scoped object outside default", http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json",
			`{"metadata":{"generateName":"node-1.","namespace":"observatory"},"involvedObject":{"kind":"Node","name":"node-1"}}`, http.StatusUnprocessableEntity},
		{"an object in another namespace", http.MethodPost, "/api/v1/namespaces/default/events", "application/json",
			`{"metadata":{"generateName":"east.","namespace":"default"},"involvedObject":{"kind":"Pod","namespace":"observatory","name":"east"}}`, http.StatusUnprocessableEntity},
		{"a patch of an Event that does not exist", http.MethodPatch, "/api/v1/namespaces/observatory/events/east.1", "application/merge-patch+json", `{"count":2}`, http.StatusNotFound},
		{"a patch that is not a merge patch", http.MethodPatch, "/api/v1/namespaces/observatory/events/east.1", "application/json-patch+json", `[]`, http.StatusUnsupportedMediaType},
		{"a method the fake does not serve", http.MethodDelete, "/api/v1/namespaces/observatory/events/east.1", "", "", http.StatusMethodNotAllowed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				held := &eventstest.Events{}
				server := apiservertest.Start(t, held)

				status, _ := sendTo(t, server, c.method, c.path, c.contentType, c.body)

				if status != c.want || len(held.List()) != 0 {
					t.Errorf("the fake answered %d and holds %+v, want %d and nothing", status, held.List(), c.want)
				}
			})
		})
	}
}

// A merge patch changes the fields it names, merges an object into an
// object, and removes a field it sets to null.
func TestEventsAppliesAMergePatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		held := &eventstest.Events{}
		server := apiservertest.Start(t, held)
		sendTo(t, server, http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json", podEvent)
		name := held.List()[0].Metadata.Name

		status, _ := sendTo(t, server, http.MethodPatch, "/api/v1/namespaces/observatory/events/"+name, "application/merge-patch+json",
			`{"count":2,"reason":null,"involvedObject":{"uid":"uid-1"}}`)

		want := events.ObjectReference{Kind: "Pod", Namespace: "observatory", Name: "east", UID: "uid-1"}
		if got := held.List(); status != http.StatusOK || len(got) != 1 || got[0].Count != 2 || got[0].Reason != "" || got[0].InvolvedObject != want {
			t.Errorf("the patch answered %d and the fake holds %+v", status, got)
		}
	})
}

// A patch that does not fit the Event's fields is refused.
func TestEventsRefusesAPatchThatDoesNotFit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		held := &eventstest.Events{}
		server := apiservertest.Start(t, held)
		sendTo(t, server, http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json", podEvent)
		name := held.List()[0].Metadata.Name

		status, _ := sendTo(t, server, http.MethodPatch, "/api/v1/namespaces/observatory/events/"+name, "application/merge-patch+json", `{"count":"two"}`)

		if got := held.List(); status != http.StatusUnprocessableEntity || got[0].Count != 1 {
			t.Errorf("the patch answered %d and the fake holds %+v, want 422 and count 1", status, got)
		}
	})
}

// Refuse answers the next writes with 503, the way an API server that
// restarts does, and Expire deletes every Event, the way the TTL does.
func TestEventsRefusesAndExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		held := &eventstest.Events{}
		server := apiservertest.Start(t, held)
		held.Refuse(2)

		refusedCreate, _ := sendTo(t, server, http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json", podEvent)
		refusedPatch, _ := sendTo(t, server, http.MethodPatch, "/api/v1/namespaces/observatory/events/east.1", "application/merge-patch+json", `{"count":2}`)
		created, _ := sendTo(t, server, http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json", podEvent)
		before := len(held.List())
		held.Expire()

		if refusedCreate != http.StatusServiceUnavailable || refusedPatch != http.StatusServiceUnavailable || created != http.StatusCreated {
			t.Errorf("the fake answered %d, %d, and %d; want 503, 503, and 201", refusedCreate, refusedPatch, created)
		}
		if before != 1 || len(held.List()) != 0 {
			t.Errorf("the fake held %d Events and then %d, want 1 and then none", before, len(held.List()))
		}
	})
}
