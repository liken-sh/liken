package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// testAPIClient returns a client for handler. It has no credentials,
// so it sends no bearer token and reads nothing from disk.
func testAPIClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := apiservertest.Start(t, handler)
	return NewClient(apiservertest.Host, server.Client(), "")
}

type recordedRequest struct {
	Method      string
	Path        string
	Query       url.Values
	ContentType string
	Body        []byte
}

// cannedAPI responds to each "METHOD path" with its canned status or
// object, or 404, and records every request. A test reads requests
// directly after its call returns. A caller whose context ends can
// return before the handler records its request, so a test of such a
// caller reads the count through sent, under the lock.
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
		if status, found := c.statuses[key]; found {
			w.WriteHeader(status)
			return
		}
		answer, found := c.answers[key]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(answer)
	})
}

func (c *cannedAPI) sent() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return len(c.requests)
}

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
		t.Fatalf("the list holds %+v, want one Receiver", list.Items)
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
		t.Fatalf("the client sent %+v, want one request", api.requests)
	}
	sent := api.requests[0]
	mustMatch(t, sent.Method, http.MethodPatch)
	mustMatch(t, sent.Path, "/apis/equipment.liken.sh/v1alpha1/receivers/theater/status")
	mustMatch(t, sent.ContentType, applyContentType)
	mustMatch(t, sent.Query.Get("fieldManager"), fieldManager)
	mustMatch(t, sent.Query.Get("force"), "true")

	body := map[string]any{}
	mustSucceed(t, json.Unmarshal(sent.Body, &body))
	if _, found := body["spec"]; found {
		t.Errorf("the status apply body is %s, want no spec", sent.Body)
	}
	mustMatch(t, body["apiVersion"], any(equipmentAPIVersion))
	mustMatch(t, body["kind"], any("Receiver"))
}

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
		t.Fatalf("the client sent %+v, want one request", api.requests)
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
	if _, found := body["status"]; found {
		t.Errorf("the spec apply body is %s, want no status", sent.Body)
	}
	spec, _ := body["spec"].(map[string]any)
	mustMatch(t, spec["power"], any("On"))
}

// A 404 and a 409 return the named errors apiclient.ErrNotFound and
// apiclient.ErrConflict, because callers handle both as normal results.
func TestTheClientReturnsNamedErrorsFor404And409(t *testing.T) {
	t.Parallel()
	api := &cannedAPI{statuses: map[string]int{
		"GET /apis/equipment.liken.sh/v1alpha1/receivers/theater":          http.StatusNotFound,
		"PATCH /apis/equipment.liken.sh/v1alpha1/receivers/theater/status": http.StatusConflict,
	}}
	client := testAPIClient(t, api.handler())

	t.Run("404 returns ErrNotFound", func(t *testing.T) {
		if _, err := GetReceiver(client, "theater"); err != apiclient.ErrNotFound {
			t.Fatalf("the error is %v, want %v", err, apiclient.ErrNotFound)
		}
	})
	t.Run("409 returns ErrConflict", func(t *testing.T) {
		_, err := ApplyReceiverStatus(client, "theater", ReceiverStatus{})
		if err != apiclient.ErrConflict {
			t.Fatalf("the error is %v, want %v", err, apiclient.ErrConflict)
		}
	})
}

// Any other failing status returns an error that is neither named
// error, so callers treat it as a failure.
func TestOtherFailingStatusesReturnAnUnnamedError(t *testing.T) {
	t.Parallel()
	api := &cannedAPI{statuses: map[string]int{
		"GET /apis/equipment.liken.sh/v1alpha1/receivers": http.StatusInternalServerError,
	}}

	_, err := ListReceivers(testAPIClient(t, api.handler()))
	mustFail(t, err)
	if err == apiclient.ErrNotFound || err == apiclient.ErrConflict {
		t.Fatalf("the error is %v, want one that is neither ErrNotFound nor ErrConflict", err)
	}
}

// A client with a credentials directory reads the token file on every
// request and sends it as a bearer token, because the kubelet rewrites
// the file as each token nears expiry.
func TestTheClientSendsTheServiceAccountTokenOnEveryRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		credentials := t.TempDir()
		mustSucceed(t, os.WriteFile(filepath.Join(credentials, "token"), []byte("first-token"), 0o600))

		sent := make(chan string, 4)
		server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sent <- r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(ReceiverList{})
		}))
		client := NewClient(apiservertest.Host, server.Client(), credentials)

		_, err := ListReceivers(client)
		mustSucceed(t, err)
		mustMatch(t, <-sent, "Bearer first-token")

		mustSucceed(t, os.WriteFile(filepath.Join(credentials, "token"), []byte("second-token"), 0o600))
		_, err = ListReceivers(client)
		mustSucceed(t, err)
		mustMatch(t, <-sent, "Bearer second-token")
	})
}

// useServiceAccountDir points serviceAccountDir at a new directory that
// holds ca.crt with caPEM, or no ca.crt when caPEM is nil. It restores
// the variable when the test ends.
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

func TestInClusterClientReadsThePodsOwnConfig(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	dir := useServiceAccountDir(t, testCAPEM(testCertificate(t)))

	client, err := InClusterClient(0)
	mustSucceed(t, err)
	mustMatch(t, client.base, "https://10.43.0.1:443")
	mustMatch(t, client.credentials, dir)
}

// A config that the pod cannot supply fails when the client is built,
// not at the first request, so a misconfigured deployment reports what
// is missing.
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

			_, err := InClusterClient(0)
			mustFail(t, err)
		})
	}
}
