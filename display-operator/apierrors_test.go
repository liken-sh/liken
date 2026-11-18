package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
)

// Each case is a row of the error table in the manual.
func TestTheErrorTable(t *testing.T) {
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
			name:   "a method this API does not accept",
			method: http.MethodPost,
			target: apiRoot + "/displays/HDMI-A-1/screen.png",
			status: http.StatusMethodNotAllowed,
			kind:   problemBlank,
			field:  [2]string{"Allow", allowedMethods},
		},
		{
			name: "the compositor denied the capture",
			set: func(_ *testCluster, sidecar *sidecarFixture) {
				sidecar.respondWith(sidecarProblem(http.StatusInternalServerError, problemCaptureDenied, "unauthorized", ""))
			},
			method: http.MethodGet,
			target: apiRoot + "/displays/HDMI-A-1/screen.png",
			status: http.StatusInternalServerError,
			kind:   problemCaptureDenied,
			detail: "unauthorized",
		},
		{
			name: "a response that is not a problem document",
			set: func(_ *testCluster, sidecar *sidecarFixture) {
				sidecar.respondWith(func(w http.ResponseWriter, _ *http.Request) {
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
			name:   "a screen with no panel on its connector",
			method: http.MethodGet,
			target: apiRoot + "/displays/HDMI-A-4/screen.png",
			status: http.StatusConflict,
			kind:   problemAway,
			detail: "No panel is connected to HDMI-A-4",
		},
		{
			name: "the output is being captured",
			set: func(_ *testCluster, sidecar *sidecarFixture) {
				sidecar.respondWith(sidecarProblem(http.StatusServiceUnavailable, problemCaptureBusy,
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
				t.Fatalf("%s %s responded with %d, want %d", row.method, row.target, resp.StatusCode, row.status)
			}
			if got := resp.Header.Get("Content-Type"); got != problemMediaType {
				t.Errorf("the refusal is typed %q, want %q", got, problemMediaType)
			}
			// A refusal has the Vary and Link headers of every other
			// response, because a client that was refused needs the
			// links to the description and the manual most.
			if got := resp.Header.Get("Vary"); got != "Accept" {
				t.Errorf("the refusal has Vary %q, want Accept", got)
			}
			if links := resp.Header.Values("Link"); len(links) < 2 {
				t.Errorf("the refusal has %v, want the service-desc and service-doc relations", links)
			}
			if row.field[0] != "" {
				if got := resp.Header.Get(row.field[0]); got != row.field[1] {
					t.Errorf("%s is %q, want %q", row.field[0], got, row.field[1])
				}
			}
			var document problemDocument
			if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
				t.Fatalf("decoding the problem document: %v", err)
			}
			if document.Type != row.kind {
				t.Errorf("the problem type is %q, want %q", document.Type, row.kind)
			}
			if document.Status != row.status {
				t.Errorf("the problem document has status %d, want %d", document.Status, row.status)
			}
			if !strings.Contains(document.Instance, "#") || !strings.HasPrefix(document.Instance, strings.Split(row.target, "?")[0]) {
				t.Errorf("the instance is %q, want the request path and the request id", document.Instance)
			}
			if row.detail != "" && !strings.Contains(document.Detail, row.detail) {
				t.Errorf("the detail is %q, want it to include %q", document.Detail, row.detail)
			}
		})
	}
}

// sidecarProblem returns a handler that refuses with a problem
// document, because the API relays a sidecar's problem document to
// the caller unchanged.
func sidecarProblem(status int, kind, detail, retry string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		if retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		w.Header().Set("Content-Type", problemMediaType)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(problemDocument{
			Type: kind, Title: "a problem the sidecar responded with", Status: status, Detail: detail, Instance: "/x#1",
		})
	}
}

func TestASlowSidecarIsAGatewayTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sidecar := newPipedSidecar(t)
		sidecar.respondWith(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(2 * headerDeadline):
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(http.StatusOK)
		})
		server := newTestAPI(t, newTestCluster(t), sidecar)
		start := time.Now()

		resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

		if resp.StatusCode != http.StatusGatewayTimeout {
			t.Fatalf("a sidecar that sent no headers responded with %d, want 504", resp.StatusCode)
		}
		if waited := time.Since(start); waited != headerDeadline {
			t.Errorf("the API waited %v for the headers, want %v", waited, headerDeadline)
		}
	})
}

func TestAnAbsentSidecarIsUnavailable(t *testing.T) {
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))
	server.sidecars = newSidecarIndex()

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a screen with no sidecar responded with %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != retryAfterSeconds {
		t.Errorf("Retry-After is %q, want %q", got, retryAfterSeconds)
	}
}

// A route authorizes the caller before it looks up the Display, so a
// refused caller cannot tell whether a name exists.
func TestARefusedCallerCannotTellWhetherANameExists(t *testing.T) {
	cluster := newTestCluster(t)
	cluster.allowed = false
	server := newTestAPI(t, cluster, newSidecarFixture(t))

	absent := call(t, server, http.MethodGet, apiRoot+"/displays/NOPE/screen.png", nil)
	present := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if absent.StatusCode != present.StatusCode {
		t.Errorf("an absent name responded with %d and a present one %d, want the same refusal",
			absent.StatusCode, present.StatusCode)
	}
	if present.StatusCode != http.StatusForbidden {
		t.Errorf("a refused subject responded with %d, want 403", present.StatusCode)
	}
}

// The Event names the Display's uid, because kubectl describe finds
// Events by uid and does not show an Event without one.
func TestACaptureWritesItsEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newTestCluster(t)
		server := newTestAPI(t, cluster, newPipedSidecar(t))

		resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)
		if got := body(t, resp); got == "" {
			t.Fatal("the capture responded with no bytes")
		}
		synctest.Wait()

		want := []string{"Normal Captured system:serviceaccount:liken-system:viewer captured the screen of HDMI-A-1 as image/png"}
		if got := eventLines(cluster.events.About("Display", "HDMI-A-1")); !slices.Equal(got, want) {
			t.Errorf("the Events are %q, want %q", got, want)
		}
		named := cluster.recorded()[0].InvolvedObject
		wantObject := events.ObjectReference{APIVersion: DisplayAPIVersion, Kind: "Display", Name: "HDMI-A-1", UID: "e740343c-8168-4b59-af5b-4a747367fcf8"}
		if named != wantObject {
			t.Errorf("the Event names %+v, want %+v", named, wantObject)
		}
	})
}

func TestHeadWritesNoEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newTestCluster(t)
		server := newTestAPI(t, cluster, newPipedSidecar(t))

		call(t, server, http.MethodHead, apiRoot+"/displays/HDMI-A-1/screen.png", nil)
		synctest.Wait()

		if events := cluster.recorded(); len(events) != 0 {
			t.Errorf("a HEAD wrote %d events, want none", len(events))
		}
	})
}

// RFC 9110 section 15.4.5 requires a 304 to have the Vary and
// Cache-Control fields that a 200 would have.
func TestADocumentReturns304ForAMatchingETag(t *testing.T) {
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
		t.Fatalf("a client holding the tag responded with %d, want 304", again.StatusCode)
	}
	if got := again.Header.Get("Vary"); got != "Accept" {
		t.Errorf("the 304 has Vary %q, want Accept", got)
	}
	if got := again.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("the 304 has Cache-Control %q, want no-cache", got)
	}
}

// A path outside the route table returns 404 and not 405, because the
// API matches the path before it checks the method.
func TestAPathOutsideTheTableIsNotFound(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	resp := call(t, server, http.MethodGet, "/v1/display/screens/HDMI-A-1", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown path responded with %d, want 404", resp.StatusCode)
	}
}

func TestTheInfoDocumentNamesTheScreen(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1", nil)

	var info screenInfo
	if err := json.Unmarshal([]byte(body(t, resp)), &info); err != nil {
		t.Fatalf("decoding the info document: %v", err)
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
		t.Errorf("the info document has %d related links, want %d", related, len(screenForms))
	}
}

func TestTheCaptureStreamsWhatTheSidecarSent(t *testing.T) {
	sidecar := newSidecarFixture(t)
	sidecar.respondWith(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		for range 4 {
			_, _ = w.Write([]byte("fragment"))
		}
	})
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.mp4?t=,10", nil)
	if got := body(t, resp); got != "fragmentfragmentfragmentfragment" {
		t.Errorf("the API responded with %q, want every fragment the sidecar sent", got)
	}
}

func TestTheRecordAndLogLineNameTheCaller(t *testing.T) {
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))
	server.record = func(screen *Display, subject, aspect, form string) {
		if subject != "system:serviceaccount:liken-system:viewer" {
			t.Errorf("the record names %q, want the subject that the review returned", subject)
		}
	}

	request := httptest.NewRequest(http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)
	request.Header.Set("Authorization", "Bearer a-caller-token")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("the capture responded with %d", recorder.Code)
	}
}

// Every refusal links to the description and the manual with the
// service-desc and service-doc relations of RFC 8631.
func TestARefusalNamesTheDescriptionAndTheManual(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	resp := call(t, server, http.MethodGet, "/v1/display/nothing", nil)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown path responded with %d, want 404", resp.StatusCode)
	}
	links := strings.Join(resp.Header.Values("Link"), " ")
	for _, relation := range []string{`rel="service-desc"`, `rel="service-doc"`} {
		if !strings.Contains(links, relation) {
			t.Errorf("the 404 has %q, want %s", links, relation)
		}
	}
	if got := resp.Header.Get("Vary"); got != "Accept" {
		t.Errorf("the 404 has Vary %q, want Accept", got)
	}
}

// The idle timeout of a capture starts at its t= begin, so t=45,50 is
// not cut off before its first byte. The sidecar here sends its first
// byte more than idleDeadline after the request.
func TestTheIdleTimeoutStartsAtTheBegin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const begin = 45 * time.Second
		sidecar := newPipedSidecar(t)
		sidecar.respondWith(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "video/mp4")
			w.WriteHeader(http.StatusOK)
			_ = http.NewResponseController(w).Flush()
			select {
			case <-time.After(begin + idleDeadline/2):
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write([]byte("fragment"))
		})
		server := newTestAPI(t, newTestCluster(t), sidecar)

		resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.mp4?t=45,50", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the clip responded with %d", resp.StatusCode)
		}
		if got := body(t, resp); got != "fragment" {
			t.Errorf("the body has %q, want the fragment that arrived after the beginning", got)
		}
	})
}

// When the caller disconnects, the API writes no problem document.
func TestACallerThatHungUpGetsNoProblemDocument(t *testing.T) {
	reached := make(chan struct{})
	sidecar := newSidecarFixture(t)
	sidecar.respondWith(func(w http.ResponseWriter, r *http.Request) {
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

// A sidecar certificate that this API cannot verify is a 503 with
// Retry-After, not a 502. While its Secret volume is empty, the
// sidecar serves a self-signed certificate, and the API creates the
// Secret, so a retry can succeed.
func TestASidecarWithAnUnknownCertificateIsUnavailable(t *testing.T) {
	sidecar := newSidecarFixture(t)
	server := newTestAPI(t, newTestCluster(t), sidecar)
	// A client with no trust anchors fails the handshake the same way
	// a self-signed certificate does.
	server.sidecar.http = &http.Client{}

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a sidecar with an unknown certificate responded with %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != retryAfterSeconds {
		t.Errorf("Retry-After is %q, want %q", got, retryAfterSeconds)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatalf("decoding the problem document: %v", err)
	}
	if !strings.Contains(document.Detail, "certificate") {
		t.Errorf("the detail is %q, want the handshake's own words", document.Detail)
	}
}

func TestEveryRefusalHasADetail(t *testing.T) {
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
				t.Fatalf("decoding the problem document: %v", err)
			}
			if document.Detail != row.want {
				t.Errorf("the refusal has %q, want %q", document.Detail, row.want)
			}
		})
	}
}

// When the compositor is down, the info route returns 200 with what
// the Display records and a detail, and omits the fields that only the
// node can report.
func TestTheInfoRouteServesAScreenThatIsDown(t *testing.T) {
	sidecar := newSidecarFixture(t)
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-3", nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a screen whose compositor is down responded with %d, want 200", resp.StatusCode)
	}
	document := body(t, resp)
	var info screenInfo
	if err := json.Unmarshal([]byte(document), &info); err != nil {
		t.Fatalf("decoding the info document: %v", err)
	}
	if info.Name != "HDMI-A-3" || info.Node != "node-1" {
		t.Errorf("the document names %s on %s", info.Name, info.Node)
	}
	if info.Width != 1280 || info.Height != 720 || info.Refresh != 60 {
		t.Errorf("the document reports %dx%d at %d, want the mode in the Display's spec",
			info.Width, info.Height, info.Refresh)
	}
	if info.Compositor != "down" {
		t.Errorf("the document reports compositor %q, want down", info.Compositor)
	}
	if !strings.Contains(info.Detail, "no such file or directory") {
		t.Errorf("the document's detail is %q, want the condition's own words", info.Detail)
	}
	for _, absent := range []string{`"scale"`, `"formats"`, `"conversion"`} {
		if strings.Contains(document, absent) {
			t.Errorf("the document has %s, which only the node can supply", absent)
		}
	}
	if sidecar.called() != 0 {
		t.Errorf("the info route called the node %d times for a screen that is down", sidecar.called())
	}
}

func TestTheInfoRouteServesAnUnreachableSidecar(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	server.sidecars = newSidecarIndex()

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1", nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a screen whose node cannot be reached responded with %d, want 200", resp.StatusCode)
	}
	var info screenInfo
	if err := json.Unmarshal([]byte(body(t, resp)), &info); err != nil {
		t.Fatalf("decoding the info document: %v", err)
	}
	if info.Sidecar != "unreachable" {
		t.Errorf("the document reports sidecar %q, want unreachable", info.Sidecar)
	}
	if info.Width != 1920 || info.Height != 1080 {
		t.Errorf("the document reports %dx%d, want the mode in the Display's spec", info.Width, info.Height)
	}
	if !strings.Contains(info.Detail, "node-1") {
		t.Errorf("the document's detail is %q, want the node it could not reach", info.Detail)
	}
}

// The address, port, and path of the connection to a sidecar go to
// the log line only, because a caller who may read screens does not
// need the cluster's layout.
func TestASidecarRefusalNamesTheNodeAndNotTheAddress(t *testing.T) {
	sidecar := newSidecarFixture(t)
	server := newTestAPI(t, newTestCluster(t), sidecar)
	server.sidecar.http = &http.Client{}

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", nil)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a sidecar with an unknown certificate responded with %d, want 503", resp.StatusCode)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatalf("decoding the problem document: %v", err)
	}
	if document.Detail != "the capture sidecar on node-1 did not present a certificate this API trusts" {
		t.Errorf("the detail is %q, want the node and the reason", document.Detail)
	}
	for _, leaked := range []string{"127.0.0.1", "9201", "/v1/display/displays/HDMI-A-1", "x509"} {
		if strings.Contains(document.Detail, leaked) {
			t.Errorf("the detail has %q, which is the cluster's own shape", leaked)
		}
	}
}

func TestAnEncoderFailureIsTyped(t *testing.T) {
	sidecar := newSidecarFixture(t)
	sidecar.respondWith(sidecarProblem(http.StatusInternalServerError, problemEncoderFailed,
		"/usr/bin/ffmpeg: exit status 251: Conversion failed!", ""))
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.mp4", nil)

	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatalf("decoding the problem document: %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError || document.Type != problemEncoderFailed {
		t.Errorf("the API responded with %d %s, want 500 %s", resp.StatusCode, document.Type, problemEncoderFailed)
	}
	if got := resp.Header.Get("Retry-After"); got != "" {
		t.Errorf("an encoder failure has Retry-After %q; a retry never clears it", got)
	}
}

// A HEAD request takes no frame, so the API computes its Content-Type
// from the Display's mode.
func TestAHeadNamesTheCodecItWouldServe(t *testing.T) {
	sidecar := newSidecarFixture(t)
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodHead, apiRoot+"/displays/HDMI-A-1/screen.mp4", nil)

	if got := resp.Header.Get("Content-Type"); got != clipContentType {
		t.Errorf("the HEAD responded with %q, want %q", got, clipContentType)
	}
	if sidecar.called() != 0 {
		t.Errorf("the HEAD called the node %d times", sidecar.called())
	}
}

// The codecs parameter has the H.264 level the sidecar chose, so the
// API passes the Content-Type through and does not compute it again.
func TestTheAPIReturnsTheContentTypeTheNodeSent(t *testing.T) {
	sidecar := newSidecarFixture(t)
	sidecar.respondWith(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", `video/mp4; codecs="avc1.640033"`)
		_, _ = w.Write([]byte("fragment"))
	})
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.mp4", nil)

	if got := resp.Header.Get("Content-Type"); got != `video/mp4; codecs="avc1.640033"` {
		t.Errorf("the API responded with %q, want the node's own type", got)
	}
}

// net/http does not send headers until the body has bytes, and the
// first byte of a capture waits for the t= begin. Without a flush, the
// headers of t=1,3 would arrive one second late, and media-api would
// shift the picture against the sound by that second.
func TestTheAPIFlushesItsHeadersBeforeTheFirstByte(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const begin = 600 * time.Millisecond
		sidecar := newPipedSidecar(t)
		sidecar.respondWith(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "video/mp4")
			w.WriteHeader(http.StatusOK)
			_ = http.NewResponseController(w).Flush()
			select {
			case <-time.After(begin):
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write([]byte("fragment"))
		})
		api := apiservertest.Start(t, newTestAPI(t, newTestCluster(t), sidecar))

		request, err := http.NewRequest(http.MethodGet,
			apiservertest.Host+apiRoot+"/displays/HDMI-A-1/screen.mp4?t=0.6,2", nil)
		if err != nil {
			t.Fatalf("building the request: %v", err)
		}
		request.Header.Set("Authorization", "Bearer a-caller-token")

		sent := time.Now()
		resp, err := api.Client().Do(request)
		if err != nil {
			t.Fatalf("requesting the clip: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		headers := time.Since(sent)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the clip responded with %d", resp.StatusCode)
		}
		if headers != 0 {
			t.Errorf("the headers reached the caller %s after the request, want at once", headers)
		}
		one := make([]byte, 1)
		if _, err := io.ReadFull(resp.Body, one); err != nil {
			t.Fatalf("reading the first body byte: %v", err)
		}
		if body := time.Since(sent); body != begin {
			t.Errorf("the first body byte arrived %s after the request, want it at the %s beginning", body, begin)
		}
	})
}

// A retry cannot reconnect an unplugged monitor, so this refusal has
// no Retry-After.
func TestAScreenWithNoPanelHasNoRetryAfter(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-4/screen.png", nil)

	if got := resp.Header.Get("Retry-After"); got != "" {
		t.Errorf("the refusal has Retry-After %q for a monitor that is not connected", got)
	}
}

func TestTheInfoRouteServesAScreenWithNoPanel(t *testing.T) {
	sidecar := newSidecarFixture(t)
	server := newTestAPI(t, newTestCluster(t), sidecar)

	resp := call(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-4", nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a screen with no panel responded with %d, want 200", resp.StatusCode)
	}
	var info screenInfo
	if err := json.Unmarshal([]byte(body(t, resp)), &info); err != nil {
		t.Fatalf("decoding the info document: %v", err)
	}
	if info.Panel != "disconnected" || info.Compositor != "" || info.Sidecar != "" {
		t.Errorf("the document reports panel %q, compositor %q, sidecar %q, want panel disconnected alone",
			info.Panel, info.Compositor, info.Sidecar)
	}
	if info.Detail != "No panel is connected to HDMI-A-4" {
		t.Errorf("the document's detail is %q, want the Connected condition's own words", info.Detail)
	}
	if sidecar.called() != 0 {
		t.Errorf("the info route called the node %d times for a screen with no panel", sidecar.called())
	}
}
