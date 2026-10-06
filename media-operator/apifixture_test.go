package main

// This file is the fixture every API test builds on: the API server
// under a stand-in control plane that answers the two reviews, one
// Player, and the Events this API posts, with two stand-in servers in
// place of the display API and the audio API. The control plane and the
// siblings answer over in-memory connections, so a test that composes
// no stream through ffmpeg runs in a synctest bubble. The clock is
// fixed, so every header field a test compares is the same bytes on
// every run.

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
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
// sets, and serves the Players a test placed. The Events this API
// posts go to an eventstest.Events in front of it.
type controlPlane struct {
	mutex         sync.Mutex
	players       map[string]*Player
	authenticated bool
	audiences     []string
	words         string
	allowed       bool
	// How many token reviews it answered, for a test that proves a
	// credential never reached the token path.
	tokenReviews int
	reviews      []SubjectAccessReview
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
			c.tokenReviews++
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
	server      *apiservertest.Server
	url         string
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

	// A body written in several parts, with a pause between them and a
	// hook the test runs as each one goes out. A stream longer than the
	// header bound is how a test tells the two bounds apart.
	chunks   int
	chunkGap time.Duration
	onChunk  func()

	// How long the sibling holds its first body byte after it has sent
	// and flushed its headers, which is what a capture waiting for its
	// first keyframe does.
	bodyDelay time.Duration
}

func newSibling(t *testing.T, url string) *sibling {
	t.Helper()
	s := &sibling{url: url, status: http.StatusOK, contentType: "video/mp4", body: []byte("payload")}
	s.server = apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mutex.Lock()
		call := r.URL.RequestURI()
		status, contentType, body, after, delay, stall := s.status, s.contentType, s.body, s.retryAfter, s.delay, s.stall
		chunks, gap, onChunk, bodyDelay := s.chunks, s.chunkGap, s.onChunk, s.bodyDelay
		for key, chosen := range s.bodies {
			if strings.Contains(call, key) {
				body = chosen
			}
		}
		s.calls = append(s.calls, call)
		s.credentials = append(s.credentials, r.Header.Get("Authorization"))
		s.mutex.Unlock()
		// A sibling whose caller gave up stops waiting, the way a
		// server's handler ends with its request.
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if after != "" {
			w.Header().Set("Retry-After", after)
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		if bodyDelay > 0 {
			_ = http.NewResponseController(w).Flush()
			time.Sleep(bodyDelay)
		}
		if chunks > 0 {
			for range chunks {
				_, _ = w.Write(body)
				w.(http.Flusher).Flush()
				if onChunk != nil {
					onChunk()
				}
				time.Sleep(gap)
			}
			return
		}
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
	return s
}

// siblingRoutes sends a request for a sibling's URL to that sibling.
// It refuses a request for any other address, the way the network
// refuses a connection to a sibling that does not run, so no test
// reaches the network or waits on a name lookup.
type siblingRoutes []*sibling

func (r siblingRoutes) RoundTrip(request *http.Request) (*http.Response, error) {
	for _, each := range r {
		if strings.HasPrefix(request.URL.String(), each.url+"/") {
			return each.server.RoundTrip(request)
		}
	}
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
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
	server *apiServer
	plane  *controlPlane
	// events holds the Events the server posted.
	events  *eventstest.Events
	display *sibling
	audio   *sibling
	lines   []apiLogLine
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	projectSiblingTokens(t)
	plane := newControlPlane()
	display := newSibling(t, "http://display-api.test")
	audio := newSibling(t, "http://audio-api.test")
	recorded := &eventstest.Events{}
	client := apiclient.New(apiservertest.Host, apiservertest.Start(t, recorded.Around(plane.handler())).Client(), "")
	fixture := &apiFixture{plane: plane, events: recorded, display: display, audio: audio}
	instants := upstreamInstants()
	fixture.server = &apiServer{
		client:  client,
		auth:    newAuthorizer(client),
		metrics: newAPIMetrics("test"),
		recorder: events.New(t.Context(), client, apiComponent,
			events.Options{Instance: "media-api-test", Log: io.Discard}),
		upstream: &upstreamClient{
			http:  &http.Client{Transport: siblingRoutes{display, audio}},
			token: siblingToken,
			clock: instants,
		},
		display:         display.url,
		audio:           audio.url,
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
			Screen:   &PlayerScreenStatus{Node: "screen-1", Monitor: testAPIMonitor},
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
