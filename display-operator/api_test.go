package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// testCluster is a fake Kubernetes API server for TokenReviews,
// SubjectAccessReviews, Display reads, and Event writes. It runs on
// the in-memory connections of apiservertest, so a test of the API can
// run in a synctest bubble.
type testCluster struct {
	server *apiservertest.Server

	mu            sync.Mutex
	authenticated bool
	allowed       bool
	reason        string
	screens       map[string]Display
	reviews       []accessReview
	// events receives the Event writes before respond sees a request.
	events *eventstest.Events
}

func newTestCluster(t *testing.T) *testCluster {
	t.Helper()
	cluster := &testCluster{
		authenticated: true,
		allowed:       true,
		reason:        "no RBAC rule grants displays/screen",
		screens: map[string]Display{
			"HDMI-A-1": {
				Metadata: DisplayMeta{Name: "HDMI-A-1", UID: "e740343c-8168-4b59-af5b-4a747367fcf8"},
				Status: DisplayStatus{
					Node:      "node-1",
					Connector: "HDMI-A-1",
					Mode:      &DisplayMode{Kernel: "1920x1080@60"},
				},
			},
			"HDMI-A-2": {
				Metadata: DisplayMeta{Name: "HDMI-A-2"},
				Status:   DisplayStatus{},
			},
			// A Display whose compositor is not serving.
			"HDMI-A-3": {
				Metadata: DisplayMeta{Name: "HDMI-A-3"},
				Status: DisplayStatus{
					Node:      "node-1",
					Connector: "HDMI-A-3",
					Mode:      &DisplayMode{Kernel: "1280x720@60"},
					Conditions: []DisplayCondition{{
						Type:    CompositorServingCondition,
						Status:  conditionFalse,
						Reason:  CompositorDownReason,
						Message: "dial unix /var/run/display.liken.sh/wayland-0: connect: no such file or directory",
					}},
				},
			},
			// A Display whose monitor was unplugged. It keeps the
			// CompositorServing condition of the stopped compositor.
			"HDMI-A-4": {
				Metadata: DisplayMeta{Name: "HDMI-A-4"},
				Status: DisplayStatus{
					Node:      "node-1",
					Connector: "HDMI-A-4",
					Mode:      &DisplayMode{Kernel: "3840x1600@60"},
					Conditions: []DisplayCondition{
						{
							Type:    ConnectedCondition,
							Status:  conditionFalse,
							Reason:  "NoPanel",
							Message: "No panel is connected to HDMI-A-4",
						},
						{
							Type:    CompositorServingCondition,
							Status:  conditionFalse,
							Reason:  CompositorDownReason,
							Message: "dial unix /var/run/display.liken.sh/wayland-0: connect: no such file or directory",
						},
					},
				},
			},
		},
	}
	cluster.events = &eventstest.Events{}
	cluster.server = apiservertest.Start(t, cluster.events.Around(http.HandlerFunc(cluster.respond)))
	return cluster
}

func (c *testCluster) respond(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == tokenReviewsPath:
		fmt.Fprintf(w, `{"status":{"authenticated":%t,"audiences":["%s"],"error":"%s","user":{"username":"system:serviceaccount:liken-system:viewer","uid":"u-1","groups":["system:authenticated"],"extra":{"scopes":["all"]}}}}`,
			c.authenticated, apiAudience, tokenReviewError(c.authenticated))
	case r.URL.Path == accessReviewsPath:
		var review accessReview
		_ = json.NewDecoder(r.Body).Decode(&review)
		c.reviews = append(c.reviews, review)
		fmt.Fprintf(w, `{"status":{"allowed":%t,"reason":%q}}`, c.allowed, c.reason)
	case strings.HasPrefix(r.URL.Path, DisplaysPath+"/"):
		screen, held := c.screens[strings.TrimPrefix(r.URL.Path, DisplaysPath+"/")]
		if !held {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"kind":"Status","code":404}`)
			return
		}
		_ = json.NewEncoder(w).Encode(screen)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"kind":"Status","code":404,"message":%q}`, r.URL.Path)
	}
}

// tokenReviewError returns status.error of the fake TokenReview, which
// the API's 401 repeats.
func tokenReviewError(authenticated bool) string {
	if authenticated {
		return ""
	}
	return "[invalid bearer token, token expired]"
}

// accessReview is the part of a SubjectAccessReview that a test reads
// back.
type accessReview struct {
	Spec struct {
		ResourceAttributes struct {
			Group       string `json:"group"`
			Resource    string `json:"resource"`
			Subresource string `json:"subresource"`
			Verb        string `json:"verb"`
			Name        string `json:"name"`
		} `json:"resourceAttributes"`
		User   string   `json:"user"`
		Groups []string `json:"groups"`
	} `json:"spec"`
}

func (c *testCluster) reviewed() []accessReview {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]accessReview(nil), c.reviews...)
}

// recorded returns the Events that the API wrote. The recorder writes
// on its own goroutine, so a test calls this after synctest.Wait.
func (c *testCluster) recorded() []events.Event {
	return c.events.List()
}

// sidecarFixture is a fake capture sidecar on a real HTTPS server, so
// the API's client does a TLS handshake and sends its token. The
// sidecar index in newTestAPI points at its host and port.
type sidecarFixture struct {
	client *http.Client
	host   string
	port   int

	mu      sync.Mutex
	calls   int
	respond func(w http.ResponseWriter, r *http.Request)
}

func newSidecarFixture(t *testing.T) *sidecarFixture {
	t.Helper()
	sidecar := &sidecarFixture{respond: servePNG}
	server := httptest.NewTLSServer(sidecar)
	t.Cleanup(server.Close)
	address, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parsing the address of the sidecar server: %v", err)
	}
	if _, err := fmt.Sscanf(address.Port(), "%d", &sidecar.port); err != nil {
		t.Fatalf("reading the port of the sidecar server: %v", err)
	}
	sidecar.client, sidecar.host = server.Client(), address.Hostname()
	return sidecar
}

// newPipedSidecar serves the fake sidecar over the in-memory
// connections of apiservertest, without TLS, so a test in a synctest
// bubble can check the API's timeouts against the fake clock.
func newPipedSidecar(t *testing.T) *sidecarFixture {
	t.Helper()
	sidecar := &sidecarFixture{respond: servePNG, host: "10.42.0.9", port: sidecarPort}
	sidecar.client = apiservertest.Start(t, sidecar).Client()
	return sidecar
}

func (s *sidecarFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls++
	handler := s.respond
	s.mu.Unlock()
	handler(w, r)
}

func servePNG(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.URL.Path, screenAspect) {
		w.Header().Set("Content-Type", jsonMediaType)
		fmt.Fprint(w, `{"width":1920,"height":1080,"scale":2,"refresh":60,"formats":["image/png"]}`)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nframe"))
}

func (s *sidecarFixture) called() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *sidecarFixture) respondWith(handler func(w http.ResponseWriter, r *http.Request)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond = handler
}

func newTestAPI(t *testing.T, cluster *testCluster, sidecar *sidecarFixture) *apiServer {
	t.Helper()
	client := apiclient.New(apiservertest.Host, cluster.server.Client(), "")
	tokenFile := t.TempDir() + "/token"
	if err := os.WriteFile(tokenFile, []byte("the-api-token"), 0o600); err != nil {
		t.Fatalf("writing the token file: %v", err)
	}
	recorder := events.New(t.Context(), client, apiComponent, events.Options{Instance: "display-api-test", Log: io.Discard})
	index := newSidecarIndex()
	index.add(Pod{
		Metadata: PodMeta{Namespace: sidecarNamespace, Name: "display-operator-abc"},
		Spec:     PodSpec{NodeName: "node-1"},
		Status: PodStatus{
			PodIP:      sidecar.host,
			Conditions: []PodCondition{{Type: "Ready", Status: conditionTrue}},
		},
	})
	return &apiServer{
		client:   client,
		sidecars: index,
		tokens: newTokenCache(func(token string) (*caller, *fault) {
			return reviewToken(client, token, apiAudience)
		}),
		sidecar: &sidecarClient{http: sidecar.client, tokenPath: tokenFile, port: sidecar.port},
		record: func(screen *Display, subject, aspect, form string) {
			recordCapture(recorder, screen, subject, aspect, form)
		},
		now: time.Now,
	}
}

// call sends a request through ServeHTTP.
func call(t *testing.T, server *apiServer, method, target string, header http.Header) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	request.Header = header
	if request.Header == nil {
		request.Header = http.Header{}
	}
	// With no Authorization key, call sends a default token. An empty
	// value sends none.
	if _, given := request.Header["Authorization"]; !given {
		request.Header.Set("Authorization", "Bearer a-caller-token")
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder.Result()
}

// clipContentType is the Content-Type that captureContentType returns
// for a clip of the 1920x1080 test Display.
const clipContentType = `video/mp4; codecs="avc1.640029"`

func bearer(token string) http.Header {
	return http.Header{"Authorization": []string{token}}
}

func accepting(types string) http.Header {
	return http.Header{
		"Authorization": []string{"Bearer a-caller-token"},
		"Accept":        []string{types},
	}
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	held, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the response body: %v", err)
	}
	return string(held)
}

// Each row of the route table in the manual has the method, status,
// and type that the table lists.
func TestEachRowOfTheRouteTableReturnsItsStatusAndType(t *testing.T) {
	cases := []struct {
		name        string
		method      string
		target      string
		status      int
		contentType string
	}{
		{"the discovery document", http.MethodGet, apiRoot, 200, jsonMediaType},
		{"the discovery document by HEAD", http.MethodHead, apiRoot, 200, jsonMediaType},
		{"the OpenAPI document", http.MethodGet, apiRoot + "/openapi.json", 200, openAPIMediaType},
		{"the OpenAPI document by HEAD", http.MethodHead, apiRoot + "/openapi.json", 200, openAPIMediaType},
		{"the screen's own numbers", http.MethodGet, apiRoot + "/displays/HDMI-A-1", 200, jsonMediaType},
		{"the negotiated screen", http.MethodGet, apiRoot + "/displays/HDMI-A-1/screen", 200, "image/png"},
		{"one frame as PNG", http.MethodGet, apiRoot + "/displays/HDMI-A-1/screen.png", 200, "image/png"},
		{"one frame as JPEG", http.MethodGet, apiRoot + "/displays/HDMI-A-1/screen.jpg", 200, "image/jpeg"},
		{"a clip", http.MethodGet, apiRoot + "/displays/HDMI-A-1/screen.mp4", 200, clipContentType},
		{"the low-end stream", http.MethodGet, apiRoot + "/displays/HDMI-A-1/screen.mjpeg", 200, mjpegContentType},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
			resp := call(t, server, row.method, row.target, nil)
			if resp.StatusCode != row.status {
				t.Errorf("%s %s responded with %d, want %d", row.method, row.target, resp.StatusCode, row.status)
			}
			if got := resp.Header.Get("Content-Type"); got != row.contentType {
				t.Errorf("%s %s responded with %q, want %q", row.method, row.target, got, row.contentType)
			}
			if got := resp.Header.Get("Vary"); got != "Accept" {
				t.Errorf("%s %s responded with Vary %q, want Accept", row.method, row.target, got)
			}
			if links := resp.Header.Values("Link"); len(links) < 2 {
				t.Errorf("%s %s has %v, want the service-desc and service-doc relations", row.method, row.target, links)
			}
		})
	}
}

// A capture response has no-store, Accept-Ranges: none, a file name
// with no colon, and no Content-Length. On the negotiated route it
// also has Content-Location and the alternate links.
func TestACaptureSetsItsOwnHeaders(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen", nil)

	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control is %q, want no-store", got)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "none" {
		t.Errorf("Accept-Ranges is %q, want none", got)
	}
	if got := resp.Header.Get("Content-Location"); got != apiRoot+"/displays/HDMI-A-1/screen.png" {
		t.Errorf("Content-Location is %q, want the extension form served", got)
	}
	if got := resp.Header.Get("Content-Length"); got != "" {
		t.Errorf("a stream has Content-Length %q, want none", got)
	}
	disposition := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(disposition, `inline; filename="HDMI-A-1-`) || !strings.HasSuffix(disposition, `.png"`) {
		t.Errorf("Content-Disposition is %q, want inline with a file name that starts HDMI-A-1- and ends .png", disposition)
	}
	if strings.Contains(disposition, ":") {
		t.Errorf("Content-Disposition %q holds a colon, which a file name may not", disposition)
	}
	alternates := 0
	for _, link := range resp.Header.Values("Link") {
		if strings.Contains(link, `rel="alternate"`) {
			alternates++
		}
	}
	if alternates != len(screenForms) {
		t.Errorf("the negotiated route has %d alternate links, want %d", alternates, len(screenForms))
	}
}

// X-Content-Type-Options: nosniff stops a browser from guessing a type
// other than the Content-Type from the bytes.
func TestACaptureForbidsSniffing(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
	}{
		{"a still", http.MethodGet, "/screen.png"},
		{"a still by HEAD", http.MethodHead, "/screen.png"},
		{"a clip", http.MethodGet, "/screen.mp4"},
		{"the negotiated screen", http.MethodGet, "/screen"},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
			resp := call(t, server, row.method, apiRoot+"/displays/HDMI-A-1"+row.target, nil)
			if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("%s %s responded with X-Content-Type-Options %q, want nosniff", row.method, row.target, got)
			}
		})
	}
}

// An extension route has one fixed type, so its response has no
// Content-Location and no alternate links.
func TestAnExtensionRouteSetsNoContentLocation(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if got := resp.Header.Get("Content-Location"); got != "" {
		t.Errorf("an extension route has Content-Location %q, want none", got)
	}
	for _, link := range resp.Header.Values("Link") {
		if strings.Contains(link, `rel="alternate"`) {
			t.Errorf("an extension route has the link %q, want no alternate link", link)
		}
	}
}

// Each row of the negotiation table in the manual goes through
// ServeHTTP, not only the Accept parser.
func TestEachRowOfTheNegotiationTableServesItsType(t *testing.T) {
	cases := []struct {
		name   string
		target string
		accept string
		status int
		served string
	}{
		{"no Accept takes the default", "/screen", "", 200, "image/png"},
		{"a type asked for by name", "/screen", "image/jpeg", 200, "image/jpeg"},
		{"a wildcard takes the first image", "/screen", "image/*", 200, "image/png"},
		{"a q-value chooses the clip", "/screen", "video/mp4, image/png;q=0.5", 200, clipContentType},
		{"a zero excludes one type", "/screen", "image/png;q=0, */*", 200, "image/jpeg"},
		{"an Accept that excludes the route", "/screen", "audio/wav", 406, problemMediaType},
		{"an extension under a wildcard", "/screen.png", "image/*", 200, "image/png"},
		{"an extension the Accept excludes", "/screen.png", "video/mp4", 406, problemMediaType},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
			resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1"+row.target, accepting(row.accept))
			if resp.StatusCode != row.status {
				t.Fatalf("Accept %q responded with %d, want %d", row.accept, resp.StatusCode, row.status)
			}
			if got := resp.Header.Get("Content-Type"); got != row.served {
				t.Errorf("Accept %q was served %q, want %q", row.accept, got, row.served)
			}
			if row.status != http.StatusNotAcceptable {
				return
			}
			var document problemDocument
			if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
				t.Fatalf("decoding the 406 problem document: %v", err)
			}
			if len(document.Acceptable) != len(screenForms) {
				t.Errorf("the 406 lists %v, want every form of the aspect", document.Acceptable)
			}
			for _, form := range document.Acceptable {
				if form.Href == "" || form.Type == "" {
					t.Errorf("the 406 lists %+v, want a type and an href", form)
				}
			}
		})
	}
}

func TestHeadDoesNotCallTheSidecar(t *testing.T) {
	sidecar := newSidecarFixture(t)
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodHead, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD responded with %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("HEAD responded with %q, want image/png", got)
	}
	if held := body(t, resp); held != "" {
		t.Errorf("HEAD responded with a body of %d bytes, want none", len(held))
	}
	if sidecar.called() != 0 {
		t.Errorf("HEAD called the sidecar %d times, want none", sidecar.called())
	}
}

// An error response to a HEAD request has the status and headers but
// no problem document (RFC 9110 section 15.5).
func TestAnErrorResponseToHeadHasNoBody(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))

	resp := call(t, server, http.MethodHead, apiRoot+"/displays/NOPE/screen.png", nil)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("HEAD on an absent Display responded with %d, want 404", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != problemMediaType {
		t.Errorf("the refusal is typed %q, want %q", got, problemMediaType)
	}
	if held := body(t, resp); held != "" {
		t.Errorf("HEAD on an error responded with %q, want no body", held)
	}
}

func TestOptionsReturnsTheAllowedMethods(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	resp := call(t, server, http.MethodOptions, apiRoot+"/displays/HDMI-A-1/screen", nil)

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("OPTIONS responded with %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != allowedMethods {
		t.Errorf("OPTIONS responded with Allow %q, want %q", got, allowedMethods)
	}
	if held := body(t, resp); held != "" {
		t.Errorf("OPTIONS responded with %q, want no body", held)
	}
}

// A capture checks get on displays/screen, and the info route checks
// get on displays. The display-capture-viewer ClusterRole grants both,
// so a caller bound to that role alone can follow the manual's example.
func TestEachRouteChecksItsRBACRule(t *testing.T) {
	cases := []struct {
		name        string
		target      string
		resource    string
		subresource string
	}{
		{"the info route", apiRoot + "/displays/HDMI-A-1", displaysPlural, ""},
		{"the negotiated screen", apiRoot + "/displays/HDMI-A-1/screen", displaysPlural, screenAspect},
		{"one frame as PNG", apiRoot + "/displays/HDMI-A-1/screen.png", displaysPlural, screenAspect},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			cluster := newTestCluster(t)
			server := newTestAPI(t, cluster, newSidecarFixture(t))

			call(t, server, http.MethodGet, row.target, nil)

			reviews := cluster.reviewed()
			if len(reviews) != 1 {
				t.Fatalf("the route made %d access reviews, want one", len(reviews))
			}
			asked := reviews[0].Spec.ResourceAttributes
			if asked.Group != DisplayGroup || asked.Resource != row.resource ||
				asked.Subresource != row.subresource || asked.Verb != "get" || asked.Name != "HDMI-A-1" {
				t.Errorf("the route asked for %+v, want get on %s/%s named HDMI-A-1 in %s",
					asked, row.resource, row.subresource, DisplayGroup)
			}
		})
	}
}

// The discovery and OpenAPI documents need an authenticated caller but
// no SubjectAccessReview.
func TestTheDocumentsMakeNoAccessReview(t *testing.T) {
	for _, target := range []string{apiRoot, apiRoot + "/openapi.json"} {
		t.Run(target, func(t *testing.T) {
			cluster := newTestCluster(t)
			server := newTestAPI(t, cluster, newSidecarFixture(t))

			call(t, server, http.MethodGet, target, nil)

			if reviews := cluster.reviewed(); len(reviews) != 0 {
				t.Errorf("the document made %d access reviews, want none", len(reviews))
			}
		})
	}
}
