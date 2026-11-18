package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// clusterFake is a fake Kubernetes API server for the API under test.
// It reviews tokens and access, serves Sinks and Sources, and records
// the Events that the API posts. It is an HTTP server and not a mock of
// each call, so the API makes the same HTTP requests as in a cluster.
type clusterFake struct {
	mu sync.Mutex

	authenticated bool
	audiences     []string
	who           tokenUserInfo
	allowed       bool
	denyReason    string

	sinks    map[string]Sink
	sources  map[string]Source
	accesses []accessReview

	events *eventstest.Events

	server *apiservertest.Server
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
		events:  &eventstest.Events{},
	}
	fake.server = apiservertest.Start(t, fake.events.Around(http.HandlerFunc(fake.serve)))
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
		f.accesses = append(f.accesses, sent)
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
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// recorded returns the Events that the API posted, after the recorder
// writes every queued Event. synctest.Wait returns when the recorder's
// goroutine blocks on an empty queue, so the caller must run inside a
// synctest bubble.
func (f *clusterFake) recorded() []events.Event {
	synctest.Wait()
	return f.events.List()
}

// reviewed returns the SubjectAccessReviews that the API sent, so a
// test can check which caller they name.
func (f *clusterFake) reviewed() []accessReview {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]accessReview(nil), f.accesses...)
}

func (f *clusterFake) refuses(reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allowed, f.denyReason = false, reason
}

// apiHarness is the API under test, with a fake cluster and a fake
// capture container that it talks to.
type apiHarness struct {
	cluster   *clusterFake
	container *containerFake
	server    *apiServer
	serving   *apiservertest.Server
	lines     *loggedLines
}

// loggedLines collects the request log lines of the API, so a test
// reads them instead of the process's standard output.
type loggedLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *loggedLines) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
}

func (l *loggedLines) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.lines) == 0 {
		return ""
	}
	return l.lines[len(l.lines)-1]
}

// containerFake is a fake capture container that the forwarder reaches
// in place of a pod's IP address.
type containerFake struct {
	mu       sync.Mutex
	status   int
	body     []byte
	headers  http.Header
	problem  *problem
	format   captureFormatDocument
	requests []*url.URL
	token    string
	stream   func(http.ResponseWriter)
	server   *apiservertest.Server
}

func newContainerFake(t *testing.T) *containerFake {
	t.Helper()
	fake := &containerFake{
		status:  http.StatusOK,
		body:    []byte("RIFFsamples"),
		headers: http.Header{},
		format:  captureFormatDocument{Rate: 48000, Channels: 2},
	}
	fake.server = apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.requests = append(fake.requests, r.URL)
		fake.token = r.Header.Get("Authorization")
		// A test that sets stream writes the whole response. The lock is
		// released first, so a long stream does not block the fake's
		// other methods.
		if write := fake.stream; write != nil {
			fake.mu.Unlock()
			write(w)
			return
		}
		defer fake.mu.Unlock()
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
	return fake
}

func (f *containerFake) called() []*url.URL {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*url.URL(nil), f.requests...)
}

func (f *containerFake) returnsProblem(document problem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.problem = &document
}

// streams makes write produce the response to each request that the
// fake receives, for a test that controls when the bytes arrive.
func (f *containerFake) streams(write func(http.ResponseWriter)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stream = write
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	cluster := newClusterFake(t)
	container := newContainerFake(t)

	work := t.TempDir()
	tokenFile := filepath.Join(work, "token")
	if err := os.WriteFile(tokenFile, []byte("private.leg.token"), 0o600); err != nil {
		t.Fatalf("writing the capture token: %v", err)
	}

	client := apiclient.New(apiservertest.Host, cluster.server.Client(), "")
	server := &apiServer{
		client:     client,
		review:     newReviewer(client, apiAudience),
		access:     newAuthorizer(client),
		pods:       newPodIndex(),
		readings:   newAPIMetrics("dev"),
		publicBase: "",
		now:        time.Now,
	}
	// The forwarder reaches the fake container over plain HTTP. The
	// certificate tests cover the forwarder's TLS.
	server.relay = &forwarder{tokenFile: tokenFile}
	server.relay.client = container.server.Client()
	server.relay.scheme = "http"
	server.relay.address = strings.TrimPrefix(apiservertest.Host, "http://")
	server.recorder = events.New(t.Context(), client, apiComponent, events.Options{Log: io.Discard})
	harnessLines := &loggedLines{}
	server.log = harnessLines.add
	server.pods.put(samplePod("node-1"))

	harness := &apiHarness{
		cluster:   cluster,
		container: container,
		server:    server,
		lines:     harnessLines,
	}
	harness.serving = apiservertest.Start(t, server)
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

// addSink adds a Sink to the fake cluster. machine is status.node, the
// Kubernetes node of the endpoint, and pipewireNode is status.nodeName,
// the PipeWire node that a stream targets.
func (h *apiHarness) addSink(name, machine, pipewireNode string) {
	h.cluster.mu.Lock()
	defer h.cluster.mu.Unlock()
	h.cluster.sinks[name] = Sink{
		Metadata: EndpointMeta{Name: name, UID: "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"},
		Status: SinkStatus{EndpointStatus: EndpointStatus{
			Node:           machine,
			NodeName:       pipewireNode,
			ConnectionType: "usb",
		}},
	}
}

// drillMachine and drillPipeWireNode are a status.node and a
// status.nodeName that a Sink on liken-1 reported. The values differ in
// form, so a test fails when the API uses one in place of the other.
const (
	drillMachine      = "liken-1"
	drillPipeWireNode = "liken.audio.card1-pcm0"
)

func (h *apiHarness) call(t *testing.T, method, target string, headers http.Header) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, apiservertest.Host+target, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer a.b.c")
	for key, values := range headers {
		request.Header[key] = values
	}
	answer, err := h.serving.Client().Do(request)
	if err != nil {
		t.Fatalf("calling %s %s: %v", method, target, err)
	}
	t.Cleanup(func() { _ = answer.Body.Close() })
	return answer
}

func TestTheDiscoveryDocumentNeedsATokenAndNoGrant(t *testing.T) {
	harness := newAPIHarness(t)
	harness.cluster.refuses("no RBAC policy allows this request")

	answer := harness.call(t, http.MethodGet, "/v1/audio", nil)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the discovery document returned %s, want 200 OK", answer.Status)
	}
	if got := answer.Header.Get("Content-Type"); got != documentType {
		t.Errorf("the type is %q, want %s", got, documentType)
	}
	if got := answer.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("a document says Cache-Control: %q, want no-cache", got)
	}
	if answer.Header.Get("ETag") == "" {
		t.Error("a document carries no ETag")
	}
	var document discoveryDocument
	if err := json.NewDecoder(answer.Body).Decode(&document); err != nil {
		t.Fatalf("decoding the discovery document: %v", err)
	}
	if document.OpenAPI != "/v1/audio/openapi.json" {
		t.Errorf("the discovery document names %q, want /v1/audio/openapi.json", document.OpenAPI)
	}
}

func TestADocumentRespondsToIfNoneMatchWithA304(t *testing.T) {
	harness := newAPIHarness(t)
	first := harness.call(t, http.MethodGet, "/v1/audio", nil)
	tag := first.Header.Get("ETag")
	_, _ = io.Copy(io.Discard, first.Body)

	second := harness.call(t, http.MethodGet, "/v1/audio",
		http.Header{"If-None-Match": {tag}})
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match returned %s, want 304 Not Modified", second.Status)
	}
	if got := second.Header.Get("ETag"); got != tag {
		t.Errorf("the 304 says ETag: %q, want %q", got, tag)
	}
	if got := second.Header.Get("Vary"); got != "Accept" {
		t.Errorf("the 304 says Vary: %q, want Accept", got)
	}
	body, _ := io.ReadAll(second.Body)
	if len(body) != 0 {
		t.Errorf("the 304 carried %d bytes, want none", len(body))
	}
}

func TestTheServedOpenAPINamesTheRequestsOwnOrigin(t *testing.T) {
	harness := newAPIHarness(t)
	answer := harness.call(t, http.MethodGet, "/v1/audio/openapi.json", nil)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the OpenAPI document returned %s, want 200 OK", answer.Status)
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
		t.Fatalf("decoding the OpenAPI document: %v", err)
	}
	if len(document.Servers) != 1 || !strings.HasPrefix(document.Servers[0].URL, "http://") {
		t.Errorf("the served document names the servers %v, want one http:// origin", document.Servers)
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
		t.Fatalf("decoding the OpenAPI document: %v", err)
	}
	if len(document.Servers) != 1 || document.Servers[0].URL != "https://audio.example.test" {
		t.Errorf("the served document names the servers %v, want https://audio.example.test", document.Servers)
	}
}

func TestARequestWithNoTokenIsAChallengeWithTheRealmAlone(t *testing.T) {
	harness := newAPIHarness(t)
	request, _ := http.NewRequest(http.MethodGet, apiservertest.Host+"/v1/audio", nil)
	answer, err := harness.serving.Client().Do(request)
	if err != nil {
		t.Fatalf("calling with no token: %v", err)
	}
	defer func() { _ = answer.Body.Close() }()
	if answer.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request with no token returned %s, want 401 Unauthorized", answer.Status)
	}
	if got := answer.Header.Get("WWW-Authenticate"); got != `Bearer realm="audio-api"` {
		t.Errorf("the challenge is %q, want Bearer realm=\"audio-api\"", got)
	}
}

func TestARefusedTokenCarriesTheReviewsWords(t *testing.T) {
	harness := newAPIHarness(t)
	harness.cluster.mu.Lock()
	harness.cluster.authenticated = false
	harness.cluster.mu.Unlock()

	answer := harness.call(t, http.MethodGet, "/v1/audio", nil)
	if answer.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a refused token returned %s, want 401 Unauthorized", answer.Status)
	}
	got := answer.Header.Get("WWW-Authenticate")
	if !strings.Contains(got, `error="invalid_token"`) ||
		!strings.Contains(got, "invalid bearer token") {
		t.Errorf("the challenge is %q, want error=\"invalid_token\" with the review's words", got)
	}
}

func TestATapWithoutTheGrantIsRefusedBeforeAnythingIsRead(t *testing.T) {
	harness := newAPIHarness(t)
	harness.cluster.refuses(`no RBAC policy matched`)
	// The Sink does not exist. The response must still be 403, so it
	// does not reveal which names exist.
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusForbidden {
		t.Fatalf("a request with no grant returned %s, want 403 Forbidden", answer.Status)
	}
	want := `Bearer realm="audio-api", error="insufficient_scope", scope="sinks/audio"`
	if got := answer.Header.Get("WWW-Authenticate"); got != want {
		t.Errorf("the challenge is %q, want %q", got, want)
	}
	if len(harness.container.called()) != 0 {
		t.Error("a refused request reached the capture container")
	}
}

// Every response carries a Vary header and the service-desc and
// service-doc Link headers (plans/completed/09-capture-audio-over-http.md).
// The body of an error response does not carry the links, so the
// headers are the only place a client finds them.
func TestEveryErrorCarriesVaryAndTheServiceLinks(t *testing.T) {
	cases := []struct {
		name   string
		status int
		drive  func(*apiHarness) *http.Response
	}{
		{"an unmatched path", http.StatusNotFound, func(h *apiHarness) *http.Response {
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/levels", nil)
		}},
		{"a method the API does not serve", http.StatusMethodNotAllowed, func(h *apiHarness) *http.Response {
			return h.call(t, http.MethodPost, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
		{"a request with no token", http.StatusUnauthorized, func(h *apiHarness) *http.Response {
			request, _ := http.NewRequest(http.MethodGet, apiservertest.Host+"/v1/audio", nil)
			answer, err := h.serving.Client().Do(request)
			if err != nil {
				t.Fatalf("calling with no token: %v", err)
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
		{"a name the cluster does not have", http.StatusNotFound, func(h *apiHarness) *http.Response {
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
		{"an endpoint that is away", http.StatusConflict, func(h *apiHarness) *http.Response {
			h.addSink("kitchen", "", "")
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
		{"a node with no container", http.StatusServiceUnavailable, func(h *apiHarness) *http.Response {
			h.addSink("kitchen", "node-9", drillPipeWireNode)
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
		{"an Accept the route cannot serve", http.StatusNotAcceptable, func(h *apiHarness) *http.Response {
			h.addSink("kitchen", "node-1", drillPipeWireNode)
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio",
				http.Header{"Accept": {"audio/mpeg"}})
		}},
		{"a problem the container returned", http.StatusInternalServerError, func(h *apiHarness) *http.Response {
			h.addSink("kitchen", "node-1", drillPipeWireNode)
			h.container.returnsProblem(problem{
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
			t.Errorf("%s returned %s, want %d", row.name, answer.Status, row.status)
		}
		if got := answer.Header.Get("Vary"); got != "Accept" {
			t.Errorf("%s says Vary: %q, want Accept", row.name, got)
		}
		link := answer.Header.Get("Link")
		if !strings.Contains(link, `rel="service-desc"`) ||
			!strings.Contains(link, `rel="service-doc"`) {
			t.Errorf("%s says Link: %q, want service-desc and service-doc links", row.name, link)
		}
		_, _ = io.Copy(io.Discard, answer.Body)
	}
}

// Authentication runs before the query is parsed, so the log line of a
// 400 for a bad query still names the caller.
func TestTheLogNamesTheCallerOnEveryStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		drive  func(*apiHarness) *http.Response
	}{
		{"a query the grammar refuses", http.StatusBadRequest, func(h *apiHarness) *http.Response {
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav?t=7,5", nil)
		}},
		{"a request with no grant", http.StatusForbidden, func(h *apiHarness) *http.Response {
			h.cluster.refuses("no RBAC policy matched")
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
		{"an Accept the route cannot serve", http.StatusNotAcceptable, func(h *apiHarness) *http.Response {
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio",
				http.Header{"Accept": {"audio/mpeg"}})
		}},
		{"a name the cluster does not have", http.StatusNotFound, func(h *apiHarness) *http.Response {
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
		{"an endpoint that is away", http.StatusConflict, func(h *apiHarness) *http.Response {
			h.addSink("kitchen", "", drillPipeWireNode)
			return h.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		}},
	}
	for _, row := range cases {
		harness := newAPIHarness(t)
		answer := row.drive(harness)
		if answer.StatusCode != row.status {
			t.Errorf("%s returned %s, want %d", row.name, answer.Status, row.status)
		}
		_, _ = io.Copy(io.Discard, answer.Body)
		line := harness.lines.last()
		if !strings.Contains(line, "user=system:serviceaccount:liken-system:listener") {
			t.Errorf("%s logged %q, want user=system:serviceaccount:liken-system:listener", row.name, line)
		}
		if strings.Contains(line, "user=-") {
			t.Errorf("%s logged %q, want a line that names the caller and not user=-", row.name, line)
		}
	}
}
