package main

// Every API test builds on this fixture. It connects the API server to
// a fake control plane and to fake display and audio APIs over
// in-memory connections, so a test that runs no ffmpeg can run in a
// synctest bubble.

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

// The fixture Player and the token and subject that the control plane
// accepts. playingPlayer has only testAPISink. A test that needs a
// second sink adds testAPISecond.
const (
	testAPINamespace = "media"
	testAPIPlayer    = "studio"
	testAPIMonitor   = "boe-1080"
	testAPISink      = "hdmi-0-pch"
	testAPISecond    = "aa-bb-cc-dd-ee-ff"
	testAPIToken     = "caller-token"
	testAPISubject   = "system:serviceaccount:media:viewer"
)

// The sibling base URLs in a cluster, for tests that compare a Location
// or Link header.
const (
	testDisplayBase = "https://display-api.liken-system.svc"
	testAudioBase   = "https://audio-api.liken-system.svc"
)

// testAPIClock is fixed so the Content-Disposition file name is the
// same on every run.
var testAPIClock = time.Date(2026, 9, 16, 21, 2, 16, 0, time.UTC)

// controlPlane is a fake Kubernetes API server. It returns the
// TokenReview and SubjectAccessReview results that a test sets, and
// serves the Players that a test adds.
type controlPlane struct {
	mutex         sync.Mutex
	players       map[string]*Player
	authenticated bool
	audiences     []string
	message       string
	allowed       bool
	// tokenReviews lets a test show that a request never reached token
	// review.
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
				Error:         c.message,
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
			player, found := c.players[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]]
			if !found {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(player)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// sibling is a fake display or audio API. A test sets the response,
// a delay before the headers, or a stall that keeps the body open until
// the client cancels the request. It records each request URI and
// Authorization header.
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

	// When chunks is set, the sibling writes the body chunks times,
	// flushing and calling onChunk after each write and then sleeping
	// chunkGap. A test uses it to stream for longer than the header
	// timeout.
	chunks   int
	chunkGap time.Duration
	onChunk  func()

	// bodyDelay is the wait between the headers and the first body
	// byte, like a capture that waits for its first keyframe.
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

// siblingRoutes sends each request to the sibling whose URL it starts
// with. Any other address fails with connection refused, as for a
// sibling that is not deployed, so no test reaches the network.
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

// ended reports whether the client canceled the stalled request.
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

type apiFixture struct {
	server  *apiServer
	plane   *controlPlane
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

// projectSiblingTokens writes a token file for each sibling, as the
// kubelet's projected volume does in a pod.
func projectSiblingTokens(t *testing.T) {
	t.Helper()
	directory := t.TempDir()
	original := siblingTokenPaths
	siblingTokenPaths = map[string]string{}
	for _, upstream := range []string{upstreamDisplay, upstreamAudio} {
		path := filepath.Join(directory, upstream+"-token")
		mustSucceed(t, os.WriteFile(path, []byte("token-for-"+upstream+"-api"), 0o600))
		siblingTokenPaths[upstream] = path
	}
	t.Cleanup(func() { siblingTokenPaths = original })
}

// upstreamInstants returns the time at which each sibling's response
// headers arrive, so the offset between the siblings is always 40 ms.
func upstreamInstants() func(string) time.Time {
	return func(upstream string) time.Time {
		if upstream == upstreamDisplay {
			return testAPIClock.Add(40 * time.Millisecond)
		}
		return testAPIClock.Add(80 * time.Millisecond)
	}
}

// playingPlayer is playing, with a screen and one sink, so media.mp4
// has a video and an audio stream to compose.
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
	if _, hasAuthorization := header["Authorization"]; !hasAuthorization {
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

// playerPathFor returns the route of the fixture Player, which serves
// the info document, or the route of a suffix below it.
func playerPathFor(suffix string) string {
	path := apiBasePath + "/namespaces/" + testAPINamespace + "/players/" + testAPIPlayer
	if suffix == "" {
		return path
	}
	return path + "/" + suffix
}
