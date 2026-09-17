package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// clusterFake stands in for the API server: the two reviews, the two
// collections, and the events the API writes. It is one server rather
// than a mock per call, so the API under test speaks the same HTTP it
// speaks in a cluster.
type clusterFake struct {
	mu sync.Mutex

	authenticated bool
	audiences     []string
	who           tokenUserInfo
	allowed       bool
	denyReason    string

	sinks   map[string]Sink
	sources map[string]Source
	events  []event

	server *httptest.Server
}

func newClusterFake(t *testing.T) *clusterFake {
	t.Helper()
	fake := &clusterFake{
		authenticated: true,
		audiences:     []string{apiAudience},
		who: tokenUserInfo{
			Username: "system:serviceaccount:liken-system:listener",
			UID:      "1f0e3dad-9990-4f65-8b2c-77b3f2a27c81",
			Groups:   []string{"system:authenticated"},
		},
		allowed: true,
		sinks:   map[string]Sink{},
		sources: map[string]Source{},
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *clusterFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == tokenReviewPath:
		var sent tokenReview
		_ = json.NewDecoder(r.Body).Decode(&sent)
		sent.Status = tokenReviewStatus{
			Authenticated: f.authenticated,
			Audiences:     f.audiences,
			User:          f.who,
			Error:         "invalid bearer token",
		}
		_ = json.NewEncoder(w).Encode(&sent)
	case r.URL.Path == accessReviewPath:
		var sent accessReview
		_ = json.NewDecoder(r.Body).Decode(&sent)
		sent.Status = accessReviewStatus{Allowed: f.allowed, Denied: !f.allowed, Reason: f.denyReason}
		_ = json.NewEncoder(w).Encode(&sent)
	case strings.HasPrefix(r.URL.Path, SinksPath+"/"):
		name := strings.TrimPrefix(r.URL.Path, SinksPath+"/")
		held, found := f.sinks[name]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(&held)
	case strings.HasPrefix(r.URL.Path, SourcesPath+"/"):
		name := strings.TrimPrefix(r.URL.Path, SourcesPath+"/")
		held, found := f.sources[name]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(&held)
	case strings.HasSuffix(r.URL.Path, "/events"):
		var written event
		_ = json.NewDecoder(r.Body).Decode(&written)
		f.events = append(f.events, written)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(&written)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *clusterFake) recorded() []event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]event(nil), f.events...)
}

func (f *clusterFake) refuses(reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allowed, f.denyReason = false, reason
}

// apiHarness is the API under test with a fake cluster behind it and a
// fake capture container beside it.
type apiHarness struct {
	cluster   *clusterFake
	container *containerFake
	server    *apiServer
	serving   *httptest.Server
}

// containerFake stands in for one node's capture container on the
// private leg.
type containerFake struct {
	mu       sync.Mutex
	status   int
	body     []byte
	headers  http.Header
	problem  *problem
	format   captureFormatDocument
	requests []*url.URL
	token    string
	server   *httptest.Server
}

func newContainerFake(t *testing.T) *containerFake {
	t.Helper()
	fake := &containerFake{
		status:  http.StatusOK,
		body:    []byte("RIFFsamples"),
		headers: http.Header{},
		format:  captureFormatDocument{Rate: 48000, Channels: 2},
	}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.requests = append(fake.requests, r.URL)
		fake.token = r.Header.Get("Authorization")
		route, name, _ := matchRoute(r.URL.Path)
		if route.Kind == routeInfo {
			fake.format.Node = name
			w.Header().Set("Content-Type", documentType)
			_ = json.NewEncoder(w).Encode(&fake.format)
			return
		}
		if fake.problem != nil {
			for key, values := range fake.headers {
				w.Header()[key] = values
			}
			writeProblem(w, r.Method, *fake.problem)
			return
		}
		for key, values := range fake.headers {
			w.Header()[key] = values
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Accept-Ranges", "none")
		w.WriteHeader(fake.status)
		_, _ = w.Write(fake.body)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *containerFake) called() []*url.URL {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*url.URL(nil), f.requests...)
}

func (f *containerFake) answers(document problem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.problem = &document
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	cluster := newClusterFake(t)
	container := newContainerFake(t)

	work := t.TempDir()
	tokenFile := filepath.Join(work, "token")
	if err := os.WriteFile(tokenFile, []byte("private.leg.token"), 0o600); err != nil {
		t.Fatal(err)
	}

	client := NewClient(cluster.server.URL, cluster.server.Client(), "")
	server := &apiServer{
		client:     client,
		review:     newReviewer(client, apiAudience),
		access:     newAuthorizer(client),
		pods:       newPodIndex(),
		readings:   newAPIMetrics("dev"),
		publicBase: "",
		now:        time.Now,
	}
	// The private leg is the container's own httptest server, reached
	// over plain HTTP: the TLS the real forwarder speaks is proved by
	// the certificate tests, and this one proves the relay.
	server.relay = &forwarder{tokenFile: tokenFile}
	containerURL, err := url.Parse(container.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	server.relay.client = container.server.Client()
	server.relay.scheme = "http"
	server.relay.port = 0
	server.relay.address = containerURL.Host
	server.record = func(kind, name, uid, aspect, format, who string, at time.Time) error {
		return recordCapture(client, kind, name, uid, aspect, format, who, at)
	}
	server.pods.replace([]pod{samplePod("node-1")})

	harness := &apiHarness{cluster: cluster, container: container, server: server}
	harness.serving = httptest.NewServer(server)
	t.Cleanup(harness.serving.Close)
	return harness
}

func samplePod(node string) pod {
	held := pod{}
	held.Metadata.Name = "audio-operator-" + node
	held.Spec.NodeName = node
	held.Status.PodIP = "10.42.0.7"
	held.Status.ContainerStatuses = []struct {
		Name  string `json:"name"`
		Ready bool   `json:"ready"`
	}{{Name: captureContainer, Ready: true}}
	return held
}

// holds puts one Sink in the fake cluster.
func (h *apiHarness) holds(name, node, nodeName string) {
	h.cluster.mu.Lock()
	defer h.cluster.mu.Unlock()
	h.cluster.sinks[name] = Sink{
		Metadata: EndpointMeta{Name: name, UID: "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"},
		Status:   EndpointStatus{Node: node, NodeName: nodeName, ConnectionType: "usb"},
	}
}

func (h *apiHarness) call(t *testing.T, method, target string, headers http.Header) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, h.serving.URL+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer a.b.c")
	for key, values := range headers {
		request.Header[key] = values
	}
	answer, err := h.serving.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = answer.Body.Close() })
	return answer
}

func TestTheDiscoveryDocumentNeedsATokenAndNoGrant(t *testing.T) {
	harness := newAPIHarness(t)
	harness.cluster.refuses("no RBAC policy allows this request")

	answer := harness.call(t, http.MethodGet, "/v1/audio", nil)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the discovery document answered %s", answer.Status)
	}
	if got := answer.Header.Get("Content-Type"); got != documentType {
		t.Errorf("the type is %q", got)
	}
	if got := answer.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("a document says Cache-Control: %q", got)
	}
	if answer.Header.Get("ETag") == "" {
		t.Error("a document carries no ETag")
	}
	var document discoveryDocument
	if err := json.NewDecoder(answer.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.OpenAPI != "/v1/audio/openapi.json" {
		t.Errorf("the discovery document names %q", document.OpenAPI)
	}
}

func TestADocumentAnswersIfNoneMatchWithA304(t *testing.T) {
	harness := newAPIHarness(t)
	first := harness.call(t, http.MethodGet, "/v1/audio", nil)
	tag := first.Header.Get("ETag")
	_, _ = io.Copy(io.Discard, first.Body)

	second := harness.call(t, http.MethodGet, "/v1/audio",
		http.Header{"If-None-Match": {tag}})
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match answered %s", second.Status)
	}
	if got := second.Header.Get("ETag"); got != tag {
		t.Errorf("the 304 says ETag: %q, want %q", got, tag)
	}
	if got := second.Header.Get("Vary"); got != "Accept" {
		t.Errorf("the 304 says Vary: %q", got)
	}
	body, _ := io.ReadAll(second.Body)
	if len(body) != 0 {
		t.Errorf("the 304 carried %d bytes", len(body))
	}
}

func TestTheServedOpenAPINamesTheRequestsOwnOrigin(t *testing.T) {
	harness := newAPIHarness(t)
	answer := harness.call(t, http.MethodGet, "/v1/audio/openapi.json", nil)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the OpenAPI document answered %s", answer.Status)
	}
	if got := answer.Header.Get("Content-Type"); got != openAPIType {
		t.Errorf("the type is %q, want %q", got, openAPIType)
	}
	var document struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(answer.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if len(document.Servers) != 1 || !strings.HasPrefix(document.Servers[0].URL, "http://") {
		t.Errorf("the served document names %v", document.Servers)
	}
}

func TestAConfiguredPublicBaseWinsOverTheRequestsOrigin(t *testing.T) {
	harness := newAPIHarness(t)
	harness.server.publicBase = "https://audio.example.test"
	answer := harness.call(t, http.MethodGet, "/v1/audio/openapi.json", nil)
	var document struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(answer.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if len(document.Servers) != 1 || document.Servers[0].URL != "https://audio.example.test" {
		t.Errorf("the served document names %v", document.Servers)
	}
}

func TestARequestWithNoTokenIsAChallengeWithTheRealmAlone(t *testing.T) {
	harness := newAPIHarness(t)
	request, _ := http.NewRequest(http.MethodGet, harness.serving.URL+"/v1/audio", nil)
	answer, err := harness.serving.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = answer.Body.Close() }()
	if answer.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request with no token answered %s", answer.Status)
	}
	if got := answer.Header.Get("WWW-Authenticate"); got != `Bearer realm="audio-api"` {
		t.Errorf("the challenge is %q", got)
	}
}

func TestARefusedTokenCarriesTheReviewsWords(t *testing.T) {
	harness := newAPIHarness(t)
	harness.cluster.mu.Lock()
	harness.cluster.authenticated = false
	harness.cluster.mu.Unlock()

	answer := harness.call(t, http.MethodGet, "/v1/audio", nil)
	if answer.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a refused token answered %s", answer.Status)
	}
	got := answer.Header.Get("WWW-Authenticate")
	if !strings.Contains(got, `error="invalid_token"`) ||
		!strings.Contains(got, "invalid bearer token") {
		t.Errorf("the challenge is %q", got)
	}
}

func TestATapWithoutTheGrantIsRefusedBeforeAnythingIsRead(t *testing.T) {
	harness := newAPIHarness(t)
	harness.cluster.refuses(`no RBAC policy matched`)
	// The Sink does not exist. A 403 must come back anyway, so nothing
	// about what exists leaks through the answer.
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusForbidden {
		t.Fatalf("a request with no grant answered %s", answer.Status)
	}
	want := `Bearer realm="audio-api", error="insufficient_scope", scope="sinks/audio"`
	if got := answer.Header.Get("WWW-Authenticate"); got != want {
		t.Errorf("the challenge is %q, want %q", got, want)
	}
	if len(harness.container.called()) != 0 {
		t.Error("a refused request reached the capture container")
	}
}

// The plan and the rulings both say Vary and the two service links are
// on every response. These are the answers that carry no body a client
// could read them from, so they are the ones worth driving.
func TestEveryErrorCarriesVaryAndTheServiceLinks(t *testing.T) {
	cases := []struct {
		name   string
		status int
		drive  func(*apiHarness) *http.Response
	}{
		{"an unmatched path", http.StatusNotFound, func(h *apiHarness) *http.Response {
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/levels", nil)
		}},
		{"a method the API does not answer", http.StatusMethodNotAllowed, func(h *apiHarness) *http.Response {
			return h.call(t, http.MethodPost, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
		{"a request with no token", http.StatusUnauthorized, func(h *apiHarness) *http.Response {
			request, _ := http.NewRequest(http.MethodGet, h.serving.URL+"/v1/audio", nil)
			answer, err := h.serving.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = answer.Body.Close() })
			return answer
		}},
		{"a request with no grant", http.StatusForbidden, func(h *apiHarness) *http.Response {
			h.cluster.refuses("no RBAC policy matched")
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
		{"a query the grammar refuses", http.StatusBadRequest, func(h *apiHarness) *http.Response {
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav?t=7,5", nil)
		}},
		{"a name the cluster does not hold", http.StatusNotFound, func(h *apiHarness) *http.Response {
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
		{"an endpoint that is away", http.StatusConflict, func(h *apiHarness) *http.Response {
			h.holds("kitchen", "", "")
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
		{"a node with no container", http.StatusServiceUnavailable, func(h *apiHarness) *http.Response {
			h.holds("kitchen", "alsa_output.kitchen", "node-9")
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
		{"an Accept the route cannot serve", http.StatusNotAcceptable, func(h *apiHarness) *http.Response {
			h.holds("kitchen", "alsa_output.kitchen", "node-1")
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio",
				http.Header{"Accept": {"audio/mpeg"}})
		}},
		{"a problem the container answered", http.StatusInternalServerError, func(h *apiHarness) *http.Response {
			h.holds("kitchen", "alsa_output.kitchen", "node-1")
			h.container.answers(problem{
				Type: problemWrongTarget, Title: "Wrong target",
				Status: http.StatusInternalServerError, Detail: "pw-record linked elsewhere",
			})
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
		{"OPTIONS", http.StatusNoContent, func(h *apiHarness) *http.Response {
			return h.call(t, http.MethodOptions, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
	}
	for _, row := range cases {
		harness := newAPIHarness(t)
		answer := row.drive(harness)
		if answer.StatusCode != row.status {
			t.Errorf("%s answered %s, want %d", row.name, answer.Status, row.status)
		}
		if got := answer.Header.Get("Vary"); got != "Accept" {
			t.Errorf("%s says Vary: %q", row.name, got)
		}
		link := answer.Header.Get("Link")
		if !strings.Contains(link, `rel="service-desc"`) ||
			!strings.Contains(link, `rel="service-doc"`) {
			t.Errorf("%s says Link: %q", row.name, link)
		}
		_, _ = io.Copy(io.Discard, answer.Body)
	}
}
