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
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// The credentials are empty, so the client sends no bearer token and
// reads nothing from disk.
func testAPIClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := apiservertest.Start(t, handler)
	return NewClient(apiservertest.Host, server.Client(), "")
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
// request it received. A test reads requests after the call it made
// has returned. A caller whose context ends can return while the
// server still takes its request, so a test of such a caller reads
// the requests through sent, which takes the lock the handler writes
// under.
type cannedAPI struct {
	answers  map[string]any
	statuses map[string]int
	mutex    sync.Mutex
	requests []recordedRequest
}

func (c *cannedAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mutex.Lock()
		c.requests = append(c.requests, recordedRequest{
			Method:      r.Method,
			Path:        r.URL.Path,
			Query:       r.URL.Query(),
			ContentType: r.Header.Get("Content-Type"),
			Body:        body,
		})
		c.mutex.Unlock()
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

// sent answers how many requests the server has taken.
func (c *cannedAPI) sent() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return len(c.requests)
}

// A Receiver is cluster-scoped, so the collection path carries no
// namespace.
func TestListReceiversReadsTheClusterScopedCollection(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	api := &cannedAPI{answers: map[string]any{
		"GET /apis/equipment.liken.sh/v1alpha1/receivers/theater": Receiver{
			Metadata: ObjectMeta{Name: "theater", ResourceVersion: "12"},
			Spec: ReceiverSpec{
				Denon: &DenonProtocol{Address: "receiver.example"},
				Session: &ReceiverSession{
					Player: "house/theater",
					Input:  "MPLAY",
				},
			},
			Status: ReceiverStoredStatus{ReceiverStatus: ReceiverStatus{Zones: map[string]ZoneStatus{"main": {Power: "On", Input: "MPLAY", Volume: "-30.5"}}}},
		},
	}}

	receiver, err := GetReceiver(testAPIClient(t, api.handler()), "theater")
	mustSucceed(t, err)

	mustMatch(t, receiver.Metadata.ResourceVersion, "12")
	mustMatch(t, receiver.Spec.Session.Player, "house/theater")
	mustMatch(t, receiver.Status.Zones["main"].Volume, "-30.5")
	mustMatch(t, api.requests[0].Path, "/apis/equipment.liken.sh/v1alpha1/receivers/theater")
}

// The status write is a server-side apply on the status subresource:
// the apply media type, this operator's field manager, and a body
// that carries no spec.
func TestApplyReceiverStatusPatchesTheStatusSubresource(t *testing.T) {
	t.Parallel()
	api := &cannedAPI{answers: map[string]any{
		"PATCH /apis/equipment.liken.sh/v1alpha1/receivers/theater/status": Receiver{
			Metadata: ObjectMeta{Name: "theater"},
			Status:   ReceiverStoredStatus{ReceiverStatus: ReceiverStatus{Zones: map[string]ZoneStatus{"main": {Power: "On"}}}},
		},
	}}
	status := ReceiverStatus{
		Zones: map[string]ZoneStatus{"main": {Power: "On", Input: "MPLAY", Volume: "-30.5"}},
		Conditions: []Condition{{
			Type:               "Reachable",
			Status:             ConditionTrue,
			Reason:             "Answered",
			LastTransitionTime: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC),
		}},
	}

	written, err := ApplyReceiverStatus(testAPIClient(t, api.handler()), "theater", status)
	mustSucceed(t, err)
	mustMatch(t, written.Status.Zones["main"].Power, "On")

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

// The power write is a server-side apply on the main resource: the
// apply media type, this operator's field manager, and a body that
// carries the one spec field this operator owns and nothing else.
func TestApplyReceiverPowerPatchesTheMainResource(t *testing.T) {
	t.Parallel()
	api := &cannedAPI{answers: map[string]any{
		"PATCH /apis/equipment.liken.sh/v1alpha1/receivers/theater": Receiver{
			Metadata: ObjectMeta{Name: "theater"},
			Spec:     ReceiverSpec{Power: equipment.PowerOn},
		},
	}}

	written, err := ApplyReceiverPower(testAPIClient(t, api.handler()), "theater", equipment.PowerOn)
	mustSucceed(t, err)
	mustMatch(t, written.Spec.Power, equipment.PowerOn)

	if len(api.requests) != 1 {
		t.Fatalf("requests = %+v", api.requests)
	}
	sent := api.requests[0]
	mustMatch(t, sent.Method, http.MethodPatch)
	mustMatch(t, sent.Path, "/apis/equipment.liken.sh/v1alpha1/receivers/theater")
	mustMatch(t, sent.ContentType, applyContentType)
	mustMatch(t, sent.Query.Get("fieldManager"), fieldManager)
	mustMatch(t, sent.Query.Get("force"), "true")

	body := map[string]any{}
	mustSucceed(t, json.Unmarshal(sent.Body, &body))
	mustMatch(t, body["apiVersion"], any(equipmentAPIVersion))
	mustMatch(t, body["kind"], any("Receiver"))
	if _, held := body["status"]; held {
		t.Errorf("the apply body carries a status: %s", sent.Body)
	}
	spec, _ := body["spec"].(map[string]any)
	mustMatch(t, spec["power"], any("On"))
}

// An absent object and a losing write are answers, not failures; the
// callers act on both.
func TestTheClientNamesTheTwoOrdinaryAnswers(t *testing.T) {
	t.Parallel()
	api := &cannedAPI{statuses: map[string]int{
		"GET /apis/equipment.liken.sh/v1alpha1/receivers/theater":          http.StatusNotFound,
		"PATCH /apis/equipment.liken.sh/v1alpha1/receivers/theater/status": http.StatusConflict,
	}}
	client := testAPIClient(t, api.handler())

	t.Run("absent", func(t *testing.T) {
		if _, err := GetReceiver(client, "theater"); err != apiclient.ErrNotFound {
			t.Fatalf("err = %v, want %v", err, apiclient.ErrNotFound)
		}
	})
	t.Run("taken", func(t *testing.T) {
		_, err := ApplyReceiverStatus(client, "theater", ReceiverStatus{})
		if err != apiclient.ErrConflict {
			t.Fatalf("err = %v, want %v", err, apiclient.ErrConflict)
		}
	})
}

// Any other failing status carries the server's own message, so a
// broken deployment says what the API server said.
func TestAServerErrorCarriesTheServersMessage(t *testing.T) {
	t.Parallel()
	api := &cannedAPI{statuses: map[string]int{
		"GET /apis/equipment.liken.sh/v1alpha1/receivers": http.StatusInternalServerError,
	}}

	_, err := ListReceivers(testAPIClient(t, api.handler()))
	mustFail(t, err)
	if err == apiclient.ErrNotFound || err == apiclient.ErrConflict {
		t.Fatalf("err = %v", err)
	}
}

// A client built with a credentials directory reads the token from disk
// on every request and sends it as a bearer token, because the kubelet
// refreshes that file as each token nears expiry.
func TestTheClientSendsTheServiceAccountTokenOnEveryRequest(t *testing.T) {
	t.Parallel()
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
