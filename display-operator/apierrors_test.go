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
			name:   "a screen whose compositor is not serving",
			method: http.MethodGet,
			target: apiRoot + "/displays/HDMI-A-3/screen.png",
			status: http.StatusServiceUnavailable,
			kind:   problemCompositorDown,
			field:  [2]string{"Retry-After", retryAfterSeconds},
			detail: "connect: no such file or directory",
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
	server.record = func(screen *Display, subject, aspect, form string) {
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

// A sidecar serving a certificate this API cannot verify is a 503
// with Retry-After, not a 502: it is serving one of its own making
// because the Secret has not reached it, and the API puts that
// Secret back within the minute.
func TestASidecarWithAnUnknownCertificateIsUnavailable(t *testing.T) {
	sidecar := newSidecarFixture(t)
	server := newTestAPI(t, newTestCluster(t), sidecar)
	// A client that trusts nothing is what a sidecar serving its own
	// leaf looks like from here.
	server.sidecar.http = &http.Client{}

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a sidecar with an unknown certificate answered %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != retryAfterSeconds {
		t.Errorf("Retry-After is %q, want %q", got, retryAfterSeconds)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(document.Detail, "certificate") {
		t.Errorf("the detail is %q, want the handshake's own words", document.Detail)
	}
}

// The Captured Event names the object by uid as well as by name,
// because kubectl describe searches a resource's events by uid and
// finds none written without one.
func TestTheCapturedEventNamesTheObjectsUID(t *testing.T) {
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))

	call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	events := cluster.recorded()
	if len(events) != 1 {
		t.Fatalf("the capture wrote %d events, want one", len(events))
	}
	named := events[0].InvolvedObject
	if named.UID != "e740343c-8168-4b59-af5b-4a747367fcf8" {
		t.Errorf("the event names uid %q, want the Display's own", named.UID)
	}
	if named.APIVersion != DisplayAPIVersion || named.Kind != "Display" {
		t.Errorf("the event names %s %s, want the Display's own kind", named.APIVersion, named.Kind)
	}
}

// Every refusal carries a detail, the two that used to answer with
// none included: a request with no token names the field it wants,
// and a subject RBAC matched no rule for is told which rule was
// asked for.
func TestEveryRefusalCarriesADetail(t *testing.T) {
	cases := []struct {
		name   string
		set    func(cluster *testCluster)
		header http.Header
		target string
		want   string
	}{
		{
			name:   "no token at all",
			header: bearer(""),
			target: apiRoot + "/displays/HDMI-A-1/screen.png",
			want:   noTokenDetail,
		},
		{
			name:   "a subject no rule matched",
			set:    func(cluster *testCluster) { cluster.allowed, cluster.reason = false, "" },
			target: apiRoot + "/displays/HDMI-A-1/screen.png",
			want:   "not allowed to get displays/screen on HDMI-A-1",
		},
		{
			name:   "a subject no rule matched, on the info route",
			set:    func(cluster *testCluster) { cluster.allowed, cluster.reason = false, "" },
			target: apiRoot + "/displays/HDMI-A-1",
			want:   "not allowed to get displays on HDMI-A-1",
		},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			cluster := newTestCluster(t)
			if row.set != nil {
				row.set(cluster)
			}
			server := newTestAPI(t, cluster, newSidecarFixture(t))

			resp := call(t, server, http.MethodGet, row.target, row.header)

			var document problemDocument
			if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
				t.Fatal(err)
			}
			if document.Detail != row.want {
				t.Errorf("the refusal carries %q, want %q", document.Detail, row.want)
			}
		})
	}
}

// A caller asking what a screen is must not need the screen to be
// up. A screen whose compositor is not serving answers 200 with what
// the Display object states, and says what is wrong; the members
// that come from the node are left out rather than guessed.
func TestTheInfoRouteAnswersAScreenThatIsDown(t *testing.T) {
	sidecar := newSidecarFixture(t)
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-3", nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a screen whose compositor is down answered %d, want 200", resp.StatusCode)
	}
	held := body(t, resp)
	var info screenInfo
	if err := json.Unmarshal([]byte(held), &info); err != nil {
		t.Fatal(err)
	}
	if info.Name != "HDMI-A-3" || info.Node != "node-1" {
		t.Errorf("the document names %s on %s", info.Name, info.Node)
	}
	if info.Width != 1280 || info.Height != 720 || info.Refresh != 60 {
		t.Errorf("the document reports %dx%d at %d, want the mode the Display states",
			info.Width, info.Height, info.Refresh)
	}
	if info.Compositor != "down" {
		t.Errorf("the document says compositor %q, want down", info.Compositor)
	}
	if !strings.Contains(info.Detail, "no such file or directory") {
		t.Errorf("the document's detail is %q, want the condition's own words", info.Detail)
	}
	for _, absent := range []string{`"scale"`, `"formats"`, `"conversion"`} {
		if strings.Contains(held, absent) {
			t.Errorf("the document carries %s, which only the node can answer", absent)
		}
	}
	if sidecar.called() != 0 {
		t.Errorf("the info route called the node %d times for a screen that is down", sidecar.called())
	}
}

// The same for a node this API cannot reach: the screen's facts, and
// what is wrong beside them.
func TestTheInfoRouteAnswersAnUnreachableSidecar(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	server.sidecars = newSidecarIndex()

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1", nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a screen whose node cannot be reached answered %d, want 200", resp.StatusCode)
	}
	var info screenInfo
	if err := json.Unmarshal([]byte(body(t, resp)), &info); err != nil {
		t.Fatal(err)
	}
	if info.Sidecar != "unreachable" {
		t.Errorf("the document says sidecar %q, want unreachable", info.Sidecar)
	}
	if info.Width != 1920 || info.Height != 1080 {
		t.Errorf("the document reports %dx%d, want the mode the Display states", info.Width, info.Height)
	}
	if !strings.Contains(info.Detail, "node-1") {
		t.Errorf("the document's detail is %q, want the node it could not reach", info.Detail)
	}
}

// A refusal that could not reach a node names the node and what went
// wrong with it, and never the address, the port, or the path on the
// private leg. Those are the shape of the cluster, and the log line
// is where they belong.
func TestASidecarRefusalNamesTheNodeAndNotTheAddress(t *testing.T) {
	sidecar := newSidecarFixture(t)
	server := newTestAPI(t, newTestCluster(t), sidecar)
	server.sidecar.http = &http.Client{}

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a sidecar with an unknown certificate answered %d, want 503", resp.StatusCode)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatal(err)
	}
	if document.Detail != "the capture sidecar on node-1 did not present a certificate this API trusts" {
		t.Errorf("the detail is %q, want the node and the reason", document.Detail)
	}
	for _, leaked := range []string{"127.0.0.1", "9201", "/v1/display/displays/HDMI-A-1", "x509"} {
		if strings.Contains(document.Detail, leaked) {
			t.Errorf("the detail carries %q, which is the cluster's own shape", leaked)
		}
	}
}

// The encoder's own failure is a typed problem, so a client matches
// on it, and its detail is the one line ffmpeg ended with.
func TestAnEncoderFailureIsTyped(t *testing.T) {
	sidecar := newSidecarFixture(t)
	sidecar.answers(answerProblem(http.StatusInternalServerError, problemEncoderFailed,
		"/usr/bin/ffmpeg: exit status 251: Conversion failed!", ""))
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.mp4", nil)

	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusInternalServerError || document.Type != problemEncoderFailed {
		t.Errorf("the API answered %d %s, want 500 %s", resp.StatusCode, document.Type, problemEncoderFailed)
	}
	if got := resp.Header.Get("Retry-After"); got != "" {
		t.Errorf("an encoder failure carries Retry-After %q; a retry never clears it", got)
	}
}

// A HEAD takes no frame, so the Content-Type it answers with is
// worked out from the Display's own mode. It names the same codec a
// GET would, because a client that asked what it would get must be
// able to act on the answer.
func TestAHeadNamesTheCodecItWouldServe(t *testing.T) {
	sidecar := newSidecarFixture(t)
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodHead, apiRoot+"/displays/HDMI-A-1/screen.mp4", nil)

	if got := resp.Header.Get("Content-Type"); got != clipContentType {
		t.Errorf("the HEAD answered %q, want %q", got, clipContentType)
	}
	if sidecar.called() != 0 {
		t.Errorf("the HEAD called the node %d times", sidecar.called())
	}
}

// The API answers the type the node is encoding, parameters and all,
// so the codecs parameter names the level the encoder pinned rather
// than one the API worked out a second time.
func TestTheAPIAnswersTheTypeTheNodeEncodes(t *testing.T) {
	sidecar := newSidecarFixture(t)
	sidecar.answers(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", `video/mp4; codecs="avc1.640033"`)
		_, _ = w.Write([]byte("fragment"))
	})
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.mp4", nil)

	if got := resp.Header.Get("Content-Type"); got != `video/mp4; codecs="avc1.640033"` {
		t.Errorf("the API answered %q, want the node's own type", got)
	}
}
