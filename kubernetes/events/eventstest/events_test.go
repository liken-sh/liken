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

func sendTo(t *testing.T, server *apiservertest.Server, method, path, contentType, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, apiservertest.Host+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(responseBody))
}

const podEvent = `{"metadata":{"generateName":"east.","namespace":"observatory"},"involvedObject":{"kind":"Pod","namespace":"observatory","name":"east"},"reason":"Created","type":"Normal","count":1}`

// echo plays the test's own fake, and writes back each request's method
// and path.
var echo = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, r.Method+" "+r.URL.Path)
})

func TestEventsServesOnlyEventRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &eventstest.Events{}
		server := apiservertest.Start(t, fake.Around(echo))

		created, _ := sendTo(t, server, http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json", podEvent)
		_, other := sendTo(t, server, http.MethodGet, "/api/v1/namespaces/observatory/pods", "", "")

		if created != http.StatusCreated || len(fake.About("Pod", "east")) != 1 || len(fake.About("Pod", "west")) != 0 {
			t.Errorf("the create returned %d and the fake stores %+v, want 201 and one Event about Pod east", created, fake.List())
		}
		if other != "GET /api/v1/namespaces/observatory/pods" {
			t.Errorf("the other request returned %q, want the response of the test's fake", other)
		}
	})
}

func TestEventsRejectsWhatTheAPIServerRejects(t *testing.T) {
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
				fake := &eventstest.Events{}
				server := apiservertest.Start(t, fake)

				status, _ := sendTo(t, server, c.method, c.path, c.contentType, c.body)

				if status != c.want || len(fake.List()) != 0 {
					t.Errorf("the fake returned %d and stores %+v, want %d and nothing", status, fake.List(), c.want)
				}
			})
		})
	}
}

func TestEventsRejectsACreateInANamespaceThatIsBeingDeleted(t *testing.T) {
	cases := []struct {
		name          string
		markNamespace func(*eventstest.Events, string)
		want          int
	}{
		{"terminating", (*eventstest.Events).Terminate, http.StatusForbidden},
		{"deleted", (*eventstest.Events).DeleteNamespace, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fake := &eventstest.Events{}
				server := apiservertest.Start(t, fake)
				c.markNamespace(fake, "observatory")

				status, _ := sendTo(t, server, http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json", podEvent)

				if status != c.want || len(fake.List()) != 0 {
					t.Errorf("the fake returned %d and stores %+v, want %d and nothing", status, fake.List(), c.want)
				}
			})
		})
	}
}

// A merge patch replaces the fields it sets, merges an object into the
// existing object, and removes a field that it sets to null.
func TestEventsAppliesAMergePatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &eventstest.Events{}
		server := apiservertest.Start(t, fake)
		sendTo(t, server, http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json", podEvent)
		name := fake.List()[0].Metadata.Name

		status, _ := sendTo(t, server, http.MethodPatch, "/api/v1/namespaces/observatory/events/"+name, "application/merge-patch+json",
			`{"count":2,"reason":null,"involvedObject":{"uid":"uid-1"}}`)

		want := events.ObjectReference{Kind: "Pod", Namespace: "observatory", Name: "east", UID: "uid-1"}
		if got := fake.List(); status != http.StatusOK || len(got) != 1 || got[0].Count != 2 || got[0].Reason != "" || got[0].InvolvedObject != want {
			t.Errorf("the patch returned %d and the fake stores %+v, want 200 and one Event with count 2, no reason, and uid-1 in the involved object", status, got)
		}
	})
}

func TestEventsRejectsAPatchWithWrongFieldTypes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &eventstest.Events{}
		server := apiservertest.Start(t, fake)
		sendTo(t, server, http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json", podEvent)
		name := fake.List()[0].Metadata.Name

		status, _ := sendTo(t, server, http.MethodPatch, "/api/v1/namespaces/observatory/events/"+name, "application/merge-patch+json", `{"count":"two"}`)

		if got := fake.List(); status != http.StatusUnprocessableEntity || got[0].Count != 1 {
			t.Errorf("the patch returned %d and the fake stores %+v, want 422 and count 1", status, got)
		}
	})
}

func TestEventsRefuseFailsWritesAndExpireDeletesEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &eventstest.Events{}
		server := apiservertest.Start(t, fake)
		fake.Refuse(2)

		createDuringRestart, _ := sendTo(t, server, http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json", podEvent)
		patchDuringRestart, _ := sendTo(t, server, http.MethodPatch, "/api/v1/namespaces/observatory/events/east.1", "application/merge-patch+json", `{"count":2}`)
		created, _ := sendTo(t, server, http.MethodPost, "/api/v1/namespaces/observatory/events", "application/json", podEvent)
		before := len(fake.List())
		fake.Expire()

		if createDuringRestart != http.StatusServiceUnavailable || patchDuringRestart != http.StatusServiceUnavailable || created != http.StatusCreated {
			t.Errorf("the fake returned %d, %d, and %d; want 503, 503, and 201", createDuringRestart, patchDuringRestart, created)
		}
		if before != 1 || len(fake.List()) != 0 {
			t.Errorf("the fake stored %d Events and then %d, want 1 and then none", before, len(fake.List()))
		}
	})
}
