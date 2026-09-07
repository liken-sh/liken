package main

// These tests run the client against a small HTTP server that
// answers the way the API server answers, so the paths, the methods,
// and the two named errors are proved without a cluster.

import (
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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

// One recorded request: what the client sent, where, and how.
type recordedRequest struct {
	Method      string
	Path        string
	Query       url.Values
	ContentType string
	Body        []byte
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
		c.requests = append(c.requests, recordedRequest{
			Method:      r.Method,
			Path:        r.URL.Path,
			Query:       r.URL.Query(),
			ContentType: r.Header.Get("Content-Type"),
			Body:        body,
		})
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

// A Receiver is cluster-scoped, so the collection path carries no
// namespace.
func TestListReceiversReadsTheClusterScopedCollection(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"GET /apis/equipment.liken.sh/v1alpha1/receivers": ReceiverList{
			Metadata: ListMeta{ResourceVersion: "77"},
			Items: []Receiver{{
				Metadata: ObjectMeta{Name: "theater"},
				Spec: ReceiverSpec{
					Denon:  &DenonProtocol{Address: "receiver.example"},
					Inputs: []ReceiverInput{{Name: "MPLAY", Machine: "node-1", Monitor: "hdmi-a-1"}},
				},
			}},
		},
	}}

	list, err := ListReceivers(testAPIClient(t, api.handler()))
	mustSucceed(t, err)

	mustMatch(t, list.Metadata.ResourceVersion, "77")
	if len(list.Items) != 1 {
		t.Fatalf("items = %+v", list.Items)
	}
	mustMatch(t, list.Items[0].Metadata.Name, "theater")
	mustMatch(t, list.Items[0].Spec.Denon.Address, "receiver.example")
	mustMatch(t, list.Items[0].Spec.Inputs[0].Monitor, "hdmi-a-1")
	mustMatch(t, api.requests[0].Path, "/apis/equipment.liken.sh/v1alpha1/receivers")
}

func TestGetReceiverReadsOneObjectByName(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"GET /apis/equipment.liken.sh/v1alpha1/receivers/theater": Receiver{
			Metadata: ObjectMeta{Name: "theater", ResourceVersion: "12"},
			Spec: ReceiverSpec{
				Denon: &DenonProtocol{Address: "receiver.example"},
				Session: &ReceiverSession{
					Player:      "house/theater",
					Input:       "MPLAY",
					VolumeTopic: "liken/media/house/theater/volume",
				},
			},
			Status: ReceiverStatus{Power: "On", Input: "MPLAY", Volume: "-30.5"},
		},
	}}

	receiver, err := GetReceiver(testAPIClient(t, api.handler()), "theater")
	mustSucceed(t, err)

	mustMatch(t, receiver.Metadata.ResourceVersion, "12")
	mustMatch(t, receiver.Spec.Session.Player, "house/theater")
	mustMatch(t, receiver.Status.Volume, "-30.5")
	mustMatch(t, api.requests[0].Path, "/apis/equipment.liken.sh/v1alpha1/receivers/theater")
}

// The watch is an ordinary GET whose response the caller owns, so the
// test reads the stream the way the loop would.
func TestWatchReceiversOpensTheStreamAtAResourceVersion(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"GET /apis/equipment.liken.sh/v1alpha1/receivers": map[string]any{
			"type":   "ADDED",
			"object": Receiver{Metadata: ObjectMeta{Name: "theater", ResourceVersion: "78"}},
		},
	}}

	resp, err := WatchReceivers(t.Context(), testAPIClient(t, api.handler()), "77")
	mustSucceed(t, err)
	t.Cleanup(func() { drain(resp.Body) })

	mustMatch(t, resp.StatusCode, http.StatusOK)
	query := api.requests[0].Query
	mustMatch(t, query.Get("watch"), "true")
	mustMatch(t, query.Get("allowWatchBookmarks"), "true")
	mustMatch(t, query.Get("resourceVersion"), "77")

	var event struct {
		Type   string   `json:"type"`
		Object Receiver `json:"object"`
	}
	mustSucceed(t, json.NewDecoder(resp.Body).Decode(&event))
	mustMatch(t, event.Type, "ADDED")
	mustMatch(t, event.Object.Metadata.ResourceVersion, "78")
}

// The status write is a server-side apply on the status subresource:
// the apply media type, this operator's field manager, and a body
// that carries no spec.
func TestApplyReceiverStatusPatchesTheStatusSubresource(t *testing.T) {
	api := &cannedAPI{answers: map[string]any{
		"PATCH /apis/equipment.liken.sh/v1alpha1/receivers/theater/status": Receiver{
			Metadata: ObjectMeta{Name: "theater"},
			Status:   ReceiverStatus{Power: "On"},
		},
	}}
	status := ReceiverStatus{
		Power:  "On",
		Input:  "MPLAY",
		Volume: "-30.5",
		Conditions: []Condition{{
			Type:               "Reachable",
			Status:             ConditionTrue,
			Reason:             "Answered",
			LastTransitionTime: "2026-09-07T12:00:00Z",
		}},
	}

	written, err := ApplyReceiverStatus(testAPIClient(t, api.handler()), "theater", status)
	mustSucceed(t, err)
	mustMatch(t, written.Status.Power, "On")

	if len(api.requests) != 1 {
		t.Fatalf("requests = %+v", api.requests)
	}
	sent := api.requests[0]
	mustMatch(t, sent.Method, http.MethodPatch)
	mustMatch(t, sent.Path, "/apis/equipment.liken.sh/v1alpha1/receivers/theater/status")
	mustMatch(t, sent.ContentType, applyContentType)
	mustMatch(t, sent.Query.Get("fieldManager"), fieldManager)
	mustMatch(t, sent.Query.Get("force"), "true")

	body := map[string]any{}
	mustSucceed(t, json.Unmarshal(sent.Body, &body))
	if _, held := body["spec"]; held {
		t.Errorf("the apply body carries a spec: %s", sent.Body)
	}
	mustMatch(t, body["apiVersion"], any(equipmentAPIVersion))
	mustMatch(t, body["kind"], any("Receiver"))
}

// An absent object and a losing write are answers, not failures; the
// callers act on both.
func TestTheClientNamesTheTwoOrdinaryAnswers(t *testing.T) {
	api := &cannedAPI{statuses: map[string]int{
		"GET /apis/equipment.liken.sh/v1alpha1/receivers/theater":          http.StatusNotFound,
		"PATCH /apis/equipment.liken.sh/v1alpha1/receivers/theater/status": http.StatusConflict,
	}}
	client := testAPIClient(t, api.handler())

	t.Run("absent", func(t *testing.T) {
		if _, err := GetReceiver(client, "theater"); err != ErrNotFound {
			t.Fatalf("err = %v, want %v", err, ErrNotFound)
		}
	})
	t.Run("taken", func(t *testing.T) {
		_, err := ApplyReceiverStatus(client, "theater", ReceiverStatus{})
		if err != ErrConflict {
			t.Fatalf("err = %v, want %v", err, ErrConflict)
		}
	})
}

// Any other failing status carries the server's own message, so a
// broken deployment says what the API server said.
func TestAServerErrorCarriesTheServersMessage(t *testing.T) {
	api := &cannedAPI{statuses: map[string]int{
		"GET /apis/equipment.liken.sh/v1alpha1/receivers": http.StatusInternalServerError,
	}}

	_, err := ListReceivers(testAPIClient(t, api.handler()))
	mustFail(t, err)
	if err == ErrNotFound || err == ErrConflict {
		t.Fatalf("err = %v", err)
	}
}

// A client built with a credentials directory reads the token from disk
// on every request and sends it as a bearer token, because the kubelet
// refreshes that file as each token nears expiry.
func TestTheClientSendsTheServiceAccountTokenOnEveryRequest(t *testing.T) {
	credentials := t.TempDir()
	mustSucceed(t, os.WriteFile(filepath.Join(credentials, "token"), []byte("first-token"), 0o600))

	sent := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent <- r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(ReceiverList{})
	}))
	t.Cleanup(server.Close)
	client := NewClient(server.URL, server.Client(), credentials)

	_, err := ListReceivers(client)
	mustSucceed(t, err)
	mustMatch(t, <-sent, "Bearer first-token")

	mustSucceed(t, os.WriteFile(filepath.Join(credentials, "token"), []byte("second-token"), 0o600))
	_, err = ListReceivers(client)
	mustSucceed(t, err)
	mustMatch(t, <-sent, "Bearer second-token")
}

// A request the client cannot build and a token it cannot read both fail
// before anything reaches the network.
func TestTheClientFailsBeforeItSends(t *testing.T) {
	t.Run("the token is not there", func(t *testing.T) {
		client := NewClient("http://127.0.0.1:1", http.DefaultClient, filepath.Join(t.TempDir(), "absent"))
		_, err := client.Do(t.Context(), http.MethodGet, receiversPath, nil)
		mustFail(t, err)
	})
	t.Run("the method is not a method", func(t *testing.T) {
		client := NewClient("http://127.0.0.1:1", http.DefaultClient, "")
		_, err := client.Do(t.Context(), "GET RECEIVERS", receiversPath, nil)
		mustFail(t, err)
	})
}

// A certificate in PEM form, taken from a test server's own
// certificate, so the pool the client builds holds a real one.
func testCAPEM(t *testing.T) []byte {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
}

// A service account directory the test controls, holding whatever CA
// file the case wants.
func useServiceAccountDir(t *testing.T, caPEM []byte) string {
	t.Helper()
	dir := t.TempDir()
	if caPEM != nil {
		mustSucceed(t, os.WriteFile(filepath.Join(dir, "ca.crt"), caPEM, 0o644))
	}
	dirWas := serviceAccountDir
	t.Cleanup(func() { serviceAccountDir = dirWas })
	serviceAccountDir = dir
	return dir
}

// The five values a pod holds are the whole of an in-cluster config: the
// two environment variables name the server, and the mounted CA and token are
// what the client trusts and sends.
func TestInClusterClientReadsThePodsOwnConfig(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	dir := useServiceAccountDir(t, testCAPEM(t))

	client, err := InClusterClient()
	mustSucceed(t, err)
	mustMatch(t, client.base, "https://10.43.0.1:443")
	mustMatch(t, client.credentials, dir)
}

// Every config a pod cannot supply fails at startup rather than at the
// first request, so a misconfigured deployment says what is missing.
func TestInClusterClientRefusesAConfigItCannotBuild(t *testing.T) {
	cases := []struct {
		name  string
		host  string
		caPEM []byte
	}{
		{name: "the environment names no server", caPEM: []byte("ignored")},
		{name: "the CA file is not there", host: "10.43.0.1"},
		{name: "the CA file holds no certificate", host: "10.43.0.1", caPEM: []byte("not a certificate")},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Setenv("KUBERNETES_SERVICE_HOST", each.host)
			t.Setenv("KUBERNETES_SERVICE_PORT", "443")
			useServiceAccountDir(t, each.caPEM)

			_, err := InClusterClient()
			mustFail(t, err)
		})
	}
}
