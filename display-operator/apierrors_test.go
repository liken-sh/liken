package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Every row of the manual's error table is answered here, in the
// status, the problem type, and the extra field it names.
func TestTheErrorTableAnswersEveryRow(t *testing.T) {
	cases := []struct {
		name   string
		set    func(cluster *testCluster, sidecar *sidecarFixture)
		method string
		target string
		header http.Header
		status int
		kind   string
		field  [2]string
		detail string
	}{
		{
			name:   "a query the grammar refuses",
			method: http.MethodGet,
			target: apiRoot + "/displays/HDMI-A-1/screen.png?t=5,7",
			status: http.StatusBadRequest,
			kind:   problemBlank,
			detail: "t=5,7",
		},
		{
			name:   "no token at all",
			method: http.MethodGet,
			target: apiRoot + "/displays/HDMI-A-1/screen.png",
			header: bearer(""),
			status: http.StatusUnauthorized,
			kind:   problemBlank,
			field:  [2]string{"WWW-Authenticate", authenticateRealm},
		},
		{
			name: "a token the review refuses",
			set: func(cluster *testCluster, _ *sidecarFixture) {
				cluster.authenticated = false
			},
			method: http.MethodGet,
			target: apiRoot + "/displays/HDMI-A-1/screen.png",
			status: http.StatusUnauthorized,
			kind:   problemBlank,
			detail: "[invalid bearer token, token expired]",
		},
		{
			name: "a subject the review refuses",
			set: func(cluster *testCluster, _ *sidecarFixture) {
				cluster.allowed = false
			},
			method: http.MethodGet,
			target: apiRoot + "/displays/HDMI-A-1/screen.png",
			status: http.StatusForbidden,
			kind:   problemBlank,
			field: [2]string{"WWW-Authenticate",
				`Bearer realm="display-api", error="insufficient_scope", scope="displays/screen"`},
			detail: "no RBAC rule grants displays/screen",
		},
		{
			name:   "no Display of that name",
			method: http.MethodGet,
			target: apiRoot + "/displays/NOPE/screen.png",
			status: http.StatusNotFound,
			kind:   problemBlank,
		},
		{
			name:   "a method this API does not answer",
			method: http.MethodPost,
			target: apiRoot + "/displays/HDMI-A-1/screen.png",
			status: http.StatusMethodNotAllowed,
			kind:   problemBlank,
			field:  [2]string{"Allow", allowedMethods},
		},
		{
			name: "the compositor denied the capture",
			set: func(_ *testCluster, sidecar *sidecarFixture) {
				sidecar.answers(answerProblem(http.StatusInternalServerError, problemCaptureDenied, "unauthorized", ""))
			},
			method: http.MethodGet,
			target: apiRoot + "/displays/HDMI-A-1/screen.png",
			status: http.StatusInternalServerError,
			kind:   problemCaptureDenied,
			detail: "unauthorized",
		},
		{
			name: "an answer that is not a problem document",
			set: func(_ *testCluster, sidecar *sidecarFixture) {
				sidecar.answers(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/plain")
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, "panic: runtime error")
				})
			},
			method: http.MethodGet,
			target: apiRoot + "/displays/HDMI-A-1/screen.png",
			status: http.StatusBadGateway,
			kind:   problemUpstreamFailed,
			detail: "panic: runtime error",
		},
		{
			name:   "a Display with no node yet",
			method: http.MethodGet,
			target: apiRoot + "/displays/HDMI-A-2/screen.png",
			status: http.StatusServiceUnavailable,
			kind:   problemNoNode,
			field:  [2]string{"Retry-After", retryAfterSeconds},
		},
		{
			name: "the output is being captured",
			set: func(_ *testCluster, sidecar *sidecarFixture) {
				sidecar.answers(answerProblem(http.StatusServiceUnavailable, problemCaptureBusy,
					"HDMI-A-1 is being captured until its client closes", retryAfterSeconds))
			},
			method: http.MethodGet,
			target: apiRoot + "/displays/HDMI-A-1/screen.png",
			status: http.StatusServiceUnavailable,
			kind:   problemCaptureBusy,
			field:  [2]string{"Retry-After", retryAfterSeconds},
			detail: "until its client closes",
		},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			cluster, sidecar := newTestCluster(t), newSidecarFixture(t)
			if row.set != nil {
				row.set(cluster, sidecar)
			}
			server := newTestAPI(t, cluster, sidecar)
			resp := call(t, server, row.method, row.target, row.header)

			if resp.StatusCode != row.status {
				t.Fatalf("%s %s answered %d, want %d", row.method, row.target, resp.StatusCode, row.status)
			}
			if got := resp.Header.Get("Content-Type"); got != problemMediaType {
				t.Errorf("the refusal is typed %q, want %q", got, problemMediaType)
			}
			// A refusal carries the fields every answer of this API
			// carries, because the client that met one is the client
			// that most needs the description and the manual.
			if got := resp.Header.Get("Vary"); got != "Accept" {
				t.Errorf("the refusal carries Vary %q, want Accept", got)
			}
			if links := resp.Header.Values("Link"); len(links) < 2 {
				t.Errorf("the refusal carries %v, want the service-desc and service-doc relations", links)
			}
			if row.field[0] != "" {
				if got := resp.Header.Get(row.field[0]); got != row.field[1] {
					t.Errorf("%s is %q, want %q", row.field[0], got, row.field[1])
				}
			}
			var document problemDocument
			if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
				t.Fatal(err)
			}
			if document.Type != row.kind {
				t.Errorf("the problem type is %q, want %q", document.Type, row.kind)
			}
			if document.Status != row.status {
				t.Errorf("the problem states status %d, want %d", document.Status, row.status)
			}
			if !strings.Contains(document.Instance, "#") || !strings.HasPrefix(document.Instance, strings.Split(row.target, "?")[0]) {
				t.Errorf("the instance is %q, want the request path and the request id", document.Instance)
			}
			if row.detail != "" && !strings.Contains(document.Detail, row.detail) {
				t.Errorf("the detail is %q, want it to carry %q", document.Detail, row.detail)
			}
		})
	}
}

// The sidecar's refusals are real problem documents, because that is
// what the API relays whole.
func answerProblem(status int, kind, detail, retry string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		if retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		w.Header().Set("Content-Type", problemMediaType)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(problemDocument{
			Type: kind, Title: "a problem the sidecar answered with", Status: status, Detail: detail, Instance: "/x#1",
		})
	}
}

// A sidecar that sends no headers in time is a 504 and never a
// capture that hangs.
func TestASlowSidecarIsAGatewayTimeout(t *testing.T) {
	sidecar := newSidecarFixture(t)
	sidecar.answers(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	server := newTestAPI(t, newTestCluster(t), sidecar)

	held := headerDeadline
	headerDeadline = 10 * time.Millisecond
	defer func() { headerDeadline = held }()

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("a sidecar that sent no headers answered %d, want 504", resp.StatusCode)
	}
}

// A node whose sidecar the API does not remember is a 503 with
// Retry-After and never a wait.
func TestAnAbsentSidecarIsUnavailable(t *testing.T) {
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))
	server.sidecars = newSidecarIndex()

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a screen with no sidecar answered %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != retryAfterSeconds {
		t.Errorf("Retry-After is %q, want %q", got, retryAfterSeconds)
	}
}

// A route authorizes before it reads, so a refused subject cannot
// learn whether a name exists: an absent name and a present one get
// the same 403.
func TestAuthorizingBeforeReading(t *testing.T) {
	cluster := newTestCluster(t)
	cluster.allowed = false
	server := newTestAPI(t, cluster, newSidecarFixture(t))

	absent := call(t, server, http.MethodGet, apiRoot+"/displays/NOPE/screen.png", nil)
	present := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if absent.StatusCode != present.StatusCode {
		t.Errorf("an absent name answered %d and a present one %d, want the same refusal",
			absent.StatusCode, present.StatusCode)
	}
	if present.StatusCode != http.StatusForbidden {
		t.Errorf("a refused subject answered %d, want 403", present.StatusCode)
	}
}

// A request that produced bytes writes the Captured Event a person
// reads with kubectl describe display.
func TestACaptureWritesItsEvent(t *testing.T) {
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)
	if held := body(t, resp); held == "" {
		t.Fatal("the capture answered no bytes")
	}

	events := cluster.recorded()
	if len(events) != 1 {
		t.Fatalf("the capture wrote %d events, want one", len(events))
	}
	event := events[0]
	if event.Reason != capturedReason || event.Type != normalEvent {
		t.Errorf("the event is %s/%s, want %s/%s", event.Type, event.Reason, normalEvent, capturedReason)
	}
	if event.InvolvedObject.Name != "HDMI-A-1" || event.InvolvedObject.Kind != "Display" {
		t.Errorf("the event names %+v, want the Display it captured", event.InvolvedObject)
	}
	for _, word := range []string{"system:serviceaccount:liken-system:viewer", screenAspect, "HDMI-A-1"} {
		if !strings.Contains(event.Message, word) {
			t.Errorf("the message %q does not name %q", event.Message, word)
		}
	}
}

// A HEAD writes no Event, because it produced no bytes.
func TestHeadWritesNoEvent(t *testing.T) {
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))

	call(t, server, http.MethodHead, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if events := cluster.recorded(); len(events) != 0 {
		t.Errorf("a HEAD wrote %d events, want none", len(events))
	}
}

// A document answers a client that holds its tag with 304 and the
// fields RFC 9110 section 15.4.5 requires.
func TestADocumentAnswersIfNoneMatch(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))

	first := call(t, server, http.MethodGet, apiRoot, nil)
	tag := first.Header.Get("ETag")
	if tag != `"`+version+`"` {
		t.Fatalf("the ETag is %q, want the build version", tag)
	}

	again := call(t, server, http.MethodGet, apiRoot, http.Header{
		"Authorization": []string{"Bearer a-caller-token"},
		"If-None-Match": []string{tag},
	})
	if again.StatusCode != http.StatusNotModified {
		t.Fatalf("a client holding the tag answered %d, want 304", again.StatusCode)
	}
	if got := again.Header.Get("Vary"); got != "Accept" {
		t.Errorf("the 304 carries Vary %q, want Accept", got)
	}
	if got := again.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("the 304 carries Cache-Control %q, want no-cache", got)
	}
}

// A path outside the table is a 404 and never a 405, because the
// table is matched on the path alone.
func TestAPathOutsideTheTableIsNotFound(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	resp := call(t, server, http.MethodGet, "/v1/display/screens/HDMI-A-1", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown path answered %d, want 404", resp.StatusCode)
	}
}

// The info document carries the screen's own numbers and the related
// links to the routes that capture it.
func TestTheInfoDocumentNamesTheScreen(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1", nil)

	var info screenInfo
	if err := json.Unmarshal([]byte(body(t, resp)), &info); err != nil {
		t.Fatal(err)
	}
	if info.Name != "HDMI-A-1" || info.Node != "node-1" {
		t.Errorf("the document names %s on %s, want HDMI-A-1 on node-1", info.Name, info.Node)
	}
	if info.Width != 1920 || info.Height != 1080 || info.Scale != 2 || info.Refresh != 60 {
		t.Errorf("the document reports %+v, want the sidecar's own numbers", info)
	}
	related := 0
	for _, link := range resp.Header.Values("Link") {
		if strings.Contains(link, `rel="related"`) {
			related++
		}
	}
	if related != len(screenForms) {
		t.Errorf("the info document carries %d related links, want %d", related, len(screenForms))
	}
}

// The whole body reaches the caller, which is the one thing the API
// does with a frame.
func TestTheCaptureStreamsWhatTheSidecarSent(t *testing.T) {
	sidecar := newSidecarFixture(t)
	sidecar.answers(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		for range 4 {
			_, _ = w.Write([]byte("fragment"))
		}
	})
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.mp4?t=,10", nil)
	if held := body(t, resp); held != "fragmentfragmentfragmentfragment" {
		t.Errorf("the API answered %q, want every fragment the sidecar sent", held)
	}
}

// The caller's subject travels with the request, so the record and
// the one line a request logs name who asked for the screen.
func TestTheSubjectRidesTheRequest(t *testing.T) {
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))
	server.record = func(name, subject, aspect, form string) {
		if subject != "system:serviceaccount:liken-system:viewer" {
			t.Errorf("the record names %q, want the subject the review answered with", subject)
		}
	}

	request := httptest.NewRequest(http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)
	request.Header.Set("Authorization", "Bearer a-caller-token")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("the capture answered %d", recorder.Code)
	}
}

// A refusal names the description and the manual by the two
// relations RFC 8631 defines, whatever the status was.
func TestARefusalNamesTheDescriptionAndTheManual(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	resp := call(t, server, http.MethodGet, "/v1/display/nothing", nil)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown path answered %d, want 404", resp.StatusCode)
	}
	links := strings.Join(resp.Header.Values("Link"), " ")
	for _, relation := range []string{`rel="service-desc"`, `rel="service-doc"`} {
		if !strings.Contains(links, relation) {
			t.Errorf("the 404 carries %q, want %s", links, relation)
		}
	}
	if got := resp.Header.Get("Vary"); got != "Accept" {
		t.Errorf("the 404 carries Vary %q, want Accept", got)
	}
}

// A capture with a t= begin counts its idle time from that begin,
// so a legal t=45,50 is not cut off before its first byte.
func TestTheIdleTimerCountsFromTheBeginning(t *testing.T) {
	sidecar := newSidecarFixture(t)
	sidecar.answers(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		http.NewResponseController(w).Flush()
		time.Sleep(120 * time.Millisecond)
		_, _ = w.Write([]byte("fragment"))
	})
	server := newTestAPI(t, newTestCluster(t), sidecar)

	held := idleDeadline
	idleDeadline = 30 * time.Millisecond
	defer func() { idleDeadline = held }()

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.mp4?t=0.2,5", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the clip answered %d", resp.StatusCode)
	}
	if got := body(t, resp); got != "fragment" {
		t.Errorf("the body carried %q, want the fragment that arrived after the beginning", got)
	}
}

// A caller that hung up gets no problem document, because there is
// nobody to answer. The request ends on its log line, and the record
// carries 499 rather than a 503 that never reached anyone.
func TestACallerThatHungUpIsAnsweredWithNothing(t *testing.T) {
	reached := make(chan struct{})
	sidecar := newSidecarFixture(t)
	sidecar.answers(func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		<-r.Context().Done()
	})
	server := newTestAPI(t, newTestCluster(t), sidecar)

	request := httptest.NewRequest(http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.mp4", nil)
	request.Header.Set("Authorization", "Bearer a-caller-token")
	gone, hangUp := context.WithCancel(request.Context())
	request = request.WithContext(gone)
	go func() {
		<-reached
		hangUp()
	}()

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Body.Len() != 0 {
		t.Errorf("the API wrote %q to a caller that hung up", recorder.Body.String())
	}
	if recorder.Code != http.StatusOK {
		t.Errorf("the API wrote status %d to a caller that hung up", recorder.Code)
	}
}
