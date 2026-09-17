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
)

// The cluster a test API reads is a real HTTP server answering the
// real JSON, so the reviews, the reads, and the Event writes are the
// ones a cluster would answer.
type testCluster struct {
	*httptest.Server

	mu            sync.Mutex
	authenticated bool
	allowed       bool
	screens       map[string]Display
	events        []Event
	reviews       []accessReview
}

func newTestCluster(t *testing.T) *testCluster {
	t.Helper()
	cluster := &testCluster{
		authenticated: true,
		allowed:       true,
		screens: map[string]Display{
			"HDMI-A-1": {
				Metadata: DisplayMeta{Name: "HDMI-A-1"},
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
		},
	}
	cluster.Server = httptest.NewServer(http.HandlerFunc(cluster.answer))
	t.Cleanup(cluster.Close)
	return cluster
}

func (c *testCluster) answer(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == tokenReviewsPath:
		fmt.Fprintf(w, `{"status":{"authenticated":%t,"audiences":["%s"],"error":"%s","user":{"username":"system:serviceaccount:liken-system:viewer","uid":"u-1","groups":["system:authenticated"],"extra":{"scopes":["all"]}}}}`,
			c.authenticated, apiAudience, refusalWords(c.authenticated))
	case r.URL.Path == accessReviewsPath:
		var review accessReview
		_ = json.NewDecoder(r.Body).Decode(&review)
		c.reviews = append(c.reviews, review)
		fmt.Fprintf(w, `{"status":{"allowed":%t,"reason":"no RBAC rule grants displays/screen"}}`, c.allowed)
	case strings.HasPrefix(r.URL.Path, DisplaysPath+"/"):
		screen, held := c.screens[strings.TrimPrefix(r.URL.Path, DisplaysPath+"/")]
		if !held {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"kind":"Status","code":404}`)
			return
		}
		_ = json.NewEncoder(w).Encode(screen)
	case strings.HasSuffix(r.URL.Path, "/events"):
		var event Event
		_ = json.NewDecoder(r.Body).Decode(&event)
		c.events = append(c.events, event)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"kind":"Status","code":404,"message":%q}`, r.URL.Path)
	}
}

// A refused review carries the API server's own words, which is what
// the 401 has to repeat.
func refusalWords(authenticated bool) string {
	if authenticated {
		return ""
	}
	return "[invalid bearer token, token expired]"
}

// The part of a SubjectAccessReview a drill reads back: which rule
// the API asked RBAC to match.
type accessReview struct {
	Spec struct {
		ResourceAttributes struct {
			Group       string `json:"group"`
			Resource    string `json:"resource"`
			Subresource string `json:"subresource"`
			Verb        string `json:"verb"`
			Name        string `json:"name"`
		} `json:"resourceAttributes"`
	} `json:"spec"`
}

func (c *testCluster) reviewed() []accessReview {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]accessReview(nil), c.reviews...)
}

func (c *testCluster) recorded() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.events...)
}

// The sidecar in a test is a real HTTPS server on the private leg,
// so the API's own client makes a real handshake and carries a real
// token to it.
type sidecarFixture struct {
	*httptest.Server

	mu     sync.Mutex
	calls  int
	answer func(w http.ResponseWriter, r *http.Request)
}

func newSidecarFixture(t *testing.T) *sidecarFixture {
	t.Helper()
	sidecar := &sidecarFixture{answer: servePNG}
	sidecar.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sidecar.mu.Lock()
		sidecar.calls++
		answer := sidecar.answer
		sidecar.mu.Unlock()
		answer(w, r)
	}))
	t.Cleanup(sidecar.Close)
	return sidecar
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

func (s *sidecarFixture) answers(answer func(w http.ResponseWriter, r *http.Request)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answer = answer
}

func newTestAPI(t *testing.T, cluster *testCluster, sidecar *sidecarFixture) *apiServer {
	t.Helper()
	client := NewClient(cluster.URL, cluster.Client(), "")
	tokenFile := t.TempDir() + "/token"
	if err := os.WriteFile(tokenFile, []byte("the-api-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	address, err := url.Parse(sidecar.URL)
	if err != nil {
		t.Fatal(err)
	}
	port := 0
	if _, err := fmt.Sscanf(address.Port(), "%d", &port); err != nil {
		t.Fatal(err)
	}
	index := newSidecarIndex()
	index.hold(Pod{
		Metadata: PodMeta{Namespace: sidecarNamespace, Name: "display-operator-abc"},
		Spec:     PodSpec{NodeName: "node-1"},
		Status: PodStatus{
			PodIP:      address.Hostname(),
			Conditions: []PodCondition{{Type: "Ready", Status: conditionTrue}},
		},
	})
	return &apiServer{
		client:   client,
		sidecars: index,
		tokens: newTokenCache(func(token string) (*reviewedToken, *fault) {
			return reviewToken(client, token, apiAudience)
		}),
		sidecar: &sidecarClient{http: sidecar.Client(), tokenPath: tokenFile, port: port},
		record: func(name, subject, aspect, form string) {
			recordCapture(client, name, subject, aspect, form)
		},
		now: time.Now,
	}
}

// One call is one request through the whole server, headers and
// body, the way a caller meets it.
func call(t *testing.T, server *apiServer, method, target string, header http.Header) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	request.Header = header
	if request.Header == nil {
		request.Header = http.Header{}
	}
	// A test that states no Authorization gets the ordinary caller's
	// token. A test that states an empty one is the caller who sent
	// none.
	if _, given := request.Header["Authorization"]; !given {
		request.Header.Set("Authorization", "Bearer a-caller-token")
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder.Result()
}

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
		t.Fatal(err)
	}
	return string(held)
}

// Every row of the manual's route table is answered here, in the
// method, status, and type it states.
func TestTheRouteTableAnswersEveryRow(t *testing.T) {
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
		{"a clip", http.MethodGet, apiRoot + "/displays/HDMI-A-1/screen.mp4", 200, "video/mp4"},
		{"the low-end stream", http.MethodGet, apiRoot + "/displays/HDMI-A-1/screen.mjpeg", 200, mjpegContentType},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
			resp := call(t, server, row.method, row.target, nil)
			if resp.StatusCode != row.status {
				t.Errorf("%s %s answered %d, want %d", row.method, row.target, resp.StatusCode, row.status)
			}
			if got := resp.Header.Get("Content-Type"); got != row.contentType {
				t.Errorf("%s %s answered %q, want %q", row.method, row.target, got, row.contentType)
			}
			if got := resp.Header.Get("Vary"); got != "Accept" {
				t.Errorf("%s %s answered Vary %q, want Accept", row.method, row.target, got)
			}
			if links := resp.Header.Values("Link"); len(links) < 2 {
				t.Errorf("%s %s carries %v, want the service-desc and service-doc relations", row.method, row.target, links)
			}
		})
	}
}

// A capture answer carries no-store, Accept-Ranges: none, a file
// name with no colon, no Content-Length, and, on the negotiated
// route, Content-Location and the alternate links. A document
// carries none of these.
func TestACaptureCarriesItsOwnConduct(t *testing.T) {
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
		t.Errorf("a stream carries Content-Length %q, want none", got)
	}
	disposition := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(disposition, `inline; filename="HDMI-A-1-`) || !strings.HasSuffix(disposition, `.png"`) {
		t.Errorf("Content-Disposition is %q", disposition)
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
		t.Errorf("the negotiated route carries %d alternate links, want %d", alternates, len(screenForms))
	}
}

// An extension route names one representation, so it carries no
// Content-Location and no alternate links.
func TestAnExtensionRouteNamesOneRepresentation(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if got := resp.Header.Get("Content-Location"); got != "" {
		t.Errorf("an extension route carries Content-Location %q, want none", got)
	}
	for _, link := range resp.Header.Values("Link") {
		if strings.Contains(link, `rel="alternate"`) {
			t.Errorf("an extension route carries %q", link)
		}
	}
}

// Every row of the manual's negotiation table is answered here by
// the whole router, not by the Accept parser alone.
func TestNegotiationAnswersEveryRow(t *testing.T) {
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
		{"a q-value chooses the clip", "/screen", "video/mp4, image/png;q=0.5", 200, "video/mp4"},
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
				t.Fatalf("Accept %q answered %d, want %d", row.accept, resp.StatusCode, row.status)
			}
			if got := resp.Header.Get("Content-Type"); got != row.served {
				t.Errorf("Accept %q was served %q, want %q", row.accept, got, row.served)
			}
			if row.status != http.StatusNotAcceptable {
				return
			}
			var document problemDocument
			if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
				t.Fatal(err)
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

// A HEAD takes no frame, which the sidecar never being called
// proves.
func TestHeadTakesNoFrame(t *testing.T) {
	sidecar := newSidecarFixture(t)
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodHead, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD answered %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("HEAD answered %q, want image/png", got)
	}
	if held := body(t, resp); held != "" {
		t.Errorf("HEAD answered a body of %d bytes, want none", len(held))
	}
	if sidecar.called() != 0 {
		t.Errorf("HEAD called the sidecar %d times, want none", sidecar.called())
	}
}

// An error answered to a HEAD carries the status and the fields and
// no document (RFC 9110 section 15.5).
func TestHeadOnAnErrorCarriesNoBody(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))

	resp := call(t, server, http.MethodHead, apiRoot+"/displays/NOPE/screen.png", nil)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("HEAD on an absent Display answered %d, want 404", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != problemMediaType {
		t.Errorf("the refusal is typed %q, want %q", got, problemMediaType)
	}
	if held := body(t, resp); held != "" {
		t.Errorf("HEAD on an error answered %q, want no body", held)
	}
}

// OPTIONS answers the methods in Allow and nothing else.
func TestOptionsAnswersTheMethods(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	resp := call(t, server, http.MethodOptions, apiRoot+"/displays/HDMI-A-1/screen", nil)

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("OPTIONS answered %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != allowedMethods {
		t.Errorf("OPTIONS answered Allow %q, want %q", got, allowedMethods)
	}
	if held := body(t, resp); held != "" {
		t.Errorf("OPTIONS answered %q, want no body", held)
	}
}

// The two rules the owner-facing ClusterRole carries, from the
// router's side. A capture asks RBAC to match displays/screen, and
// the info route beside it asks for displays, so a caller bound to
// display-capture-viewer alone can run the manual's recipe from end
// to end.
func TestEachRouteAsksForTheGrantItNeeds(t *testing.T) {
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

// The two documents need authentication and no authorization, so
// neither one costs an access review.
func TestTheDocumentsAskForNoGrant(t *testing.T) {
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
