package main

// This file is the fixture every API test builds on: the API server
// under a stand-in control plane that answers the two reviews, one
// Player, and the events this API writes, with two httptest servers
// in place of the display API and the audio API. The clock is fixed,
// so every header field a test compares is the same bytes on every
// run.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The Player every capture test asks about: its namespace and name,
// its Display, its two Sinks, and the token and subject the control
// plane answers for.
const (
	testAPINamespace = "media"
	testAPIPlayer    = "studio"
	testAPIMonitor   = "boe-1080"
	testAPISink      = "hdmi-0-pch"
	testAPISecond    = "aa-bb-cc-dd-ee-ff"
	testAPIToken     = "caller-token"
	testAPISubject   = "system:serviceaccount:media:viewer"
)

// The sibling base URLs as a cluster names them, for the tests that
// compare a Location or a Link byte for byte with what a cluster
// would send.
const (
	testDisplayBase = "https://display-api.liken-system.svc"
	testAudioBase   = "https://audio-api.liken-system.svc"
)

// testAPIClock is the instant every capture header is stamped with,
// so a Content-Disposition file name is the same on every run.
var testAPIClock = time.Date(2026, 9, 16, 21, 2, 16, 0, time.UTC)

// controlPlane stands in for the API server: it answers the
// TokenReview and the SubjectAccessReview with the verdicts a test
// sets, serves the Players a test placed, and keeps the Events this
// API writes.
type controlPlane struct {
	mutex         sync.Mutex
	players       map[string]*Player
	authenticated bool
	audiences     []string
	words         string
	allowed       bool
	refuseEvents  bool
	reviews       []SubjectAccessReview
	events        []Event
}

func newControlPlane() *controlPlane {
	return &controlPlane{
		players:       map[string]*Player{},
		authenticated: true,
		audiences:     []string{apiAudience},
		allowed:       true,
	}
}

func (c *controlPlane) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		switch {
		case r.URL.Path == tokenReviewsPath:
			var review TokenReview
			_ = json.NewDecoder(r.Body).Decode(&review)
			review.Status = TokenReviewStatus{
				Authenticated: c.authenticated,
				Audiences:     c.audiences,
				Error:         c.words,
				User: TokenReviewUser{
					Username: testAPISubject,
					UID:      "uid-1",
					Groups:   []string{"system:authenticated"},
					Extra:    map[string][]string{"scopes.authorization.openshift.io": {"user:info"}},
				},
			}
			_ = json.NewEncoder(w).Encode(review)
		case r.URL.Path == subjectAccessReviewsPath:
			var review SubjectAccessReview
			_ = json.NewDecoder(r.Body).Decode(&review)
			c.reviews = append(c.reviews, review)
			review.Status = SubjectAccessReviewStatus{Allowed: c.allowed}
			_ = json.NewEncoder(w).Encode(review)
		case strings.HasSuffix(r.URL.Path, "/events"):
			if c.refuseEvents {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			var event Event
			_ = json.NewDecoder(r.Body).Decode(&event)
			c.events = append(c.events, event)
			_ = json.NewEncoder(w).Encode(event)
		case strings.Contains(r.URL.Path, "/players/"):
			held, standing := c.players[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]]
			if !standing {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(held)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// sibling stands in for one sibling API as this API's client meets
// it. A test sets the status, the type, the body, and the Retry-After
// it answers, a delay before its headers, or a stall that holds the
// body open until this API ends the request. It keeps every call and
// every Authorization field it was sent.
type sibling struct {
	mutex       sync.Mutex
	server      *httptest.Server
	calls       []string
	credentials []string
	status      int
	contentType string
	body        []byte
	bodies      map[string][]byte
	retryAfter  string
	delay       time.Duration
	stall       bool
	hungUp      bool
}

func newSibling(t *testing.T) *sibling {
	t.Helper()
	s := &sibling{status: http.StatusOK, contentType: "video/mp4", body: []byte("payload")}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mutex.Lock()
		call := r.URL.RequestURI()
		status, contentType, body, after, delay, stall := s.status, s.contentType, s.body, s.retryAfter, s.delay, s.stall
		for key, chosen := range s.bodies {
			if strings.Contains(call, key) {
				body = chosen
			}
		}
		s.calls = append(s.calls, call)
		s.credentials = append(s.credentials, r.Header.Get("Authorization"))
		s.mutex.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		if after != "" {
			w.Header().Set("Retry-After", after)
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write(body)
		if !stall {
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		s.mutex.Lock()
		s.hungUp = true
		s.mutex.Unlock()
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *sibling) made() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]string(nil), s.calls...)
}

// ended reports whether this API ended the request the stalled
// sibling had open, which is how a test sees a cancelled composition.
func (s *sibling) ended() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.hungUp
}

func (s *sibling) sent() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]string(nil), s.credentials...)
}

// apiFixture is the whole fixture: one API server, one control plane,
// two siblings, and the log lines the server wrote, for a test that
// reads the request id or the offset.
type apiFixture struct {
	server  *apiServer
	plane   *controlPlane
	display *sibling
	audio   *sibling
	lines   []apiLogLine
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	projectSiblingTokens(t)
	plane := newControlPlane()
	planeServer := httptest.NewServer(plane.handler())
	t.Cleanup(planeServer.Close)
	display := newSibling(t)
	audio := newSibling(t)
	client := NewClient(planeServer.URL, planeServer.Client(), "")
	fixture := &apiFixture{plane: plane, display: display, audio: audio}
	instants := upstreamInstants()
	fixture.server = &apiServer{
		client:  client,
		auth:    newAuthorizer(client),
		metrics: newAPIMetrics("test"),
		upstream: &upstreamClient{
			http:  http.DefaultClient,
			token: siblingToken,
			clock: instants,
		},
		display:         display.server.URL,
		audio:           audio.server.URL,
		version:         "test",
		ffmpeg:          "ffmpeg",
		maxCompositions: 4,
		now:             func() time.Time { return testAPIClock },
		log:             func(line apiLogLine) { fixture.lines = append(fixture.lines, line) },
	}
	fixture.server.compositions = make(chan struct{}, 4)
	fixture.server.ready.Store(true)
	plane.players[testAPIPlayer] = playingPlayer()
	return fixture
}

// projectSiblingTokens writes the two projected tokens the kubelet
// would mount, one per sibling audience, so the upstream client reads
// them from files the way it does in a pod.
func projectSiblingTokens(t *testing.T) {
	t.Helper()
	directory := t.TempDir()
	held := siblingTokenPaths
	siblingTokenPaths = map[string]string{}
	for _, upstream := range []string{upstreamDisplay, upstreamAudio} {
		path := filepath.Join(directory, upstream+"-token")
		mustSucceed(t, os.WriteFile(path, []byte("token-for-"+upstream+"-api"), 0o600))
		siblingTokenPaths[upstream] = path
	}
	t.Cleanup(func() { siblingTokenPaths = held })
}

// upstreamInstants is the clock the upstream client stamps header
// arrival with: one instant for the display sibling and one for the
// audio sibling, so the offset is 40 ms on every run.
func upstreamInstants() func(string) time.Time {
	return func(upstream string) time.Time {
		if upstream == upstreamDisplay {
			return testAPIClock.Add(40 * time.Millisecond)
		}
		return testAPIClock.Add(80 * time.Millisecond)
	}
}

// playingPlayer is a Player with a screen, one sink, and a Play
// running on it: two streams, so media.mp4 composes.
func playingPlayer() *Player {
	return &Player{
		Metadata: ObjectMeta{Namespace: testAPINamespace, Name: testAPIPlayer, UID: "player-uid"},
		Status: PlayerStatus{
			Activity: playerPlaying,
			Play:     "movie",
			Screen:   &PlayerScreenStatus{Node: "stick1", Monitor: testAPIMonitor},
			Sinks:    []PlayerSinkStatus{{Request: "audio0", Name: testAPISink}},
		},
	}
}

func (f *apiFixture) call(method, target string, header http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	for key, values := range header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if _, stated := header["Authorization"]; !stated {
		request.Header.Set("Authorization", "Bearer "+testAPIToken)
	}
	recorder := httptest.NewRecorder()
	f.server.handler().ServeHTTP(recorder, request)
	return recorder
}

func (f *apiFixture) get(target string) *httptest.ResponseRecorder {
	return f.call(http.MethodGet, target, nil)
}

func (f *apiFixture) accept(target, accept string) *httptest.ResponseRecorder {
	return f.call(http.MethodGet, target, http.Header{"Accept": {accept}})
}

// playerPathFor is the fixture Player's route, with an aspect suffix
// or without one for the info document.
func playerPathFor(suffix string) string {
	path := apiBasePath + "/namespaces/" + testAPINamespace + "/players/" + testAPIPlayer
	if suffix == "" {
		return path
	}
	return path + "/" + suffix
}
