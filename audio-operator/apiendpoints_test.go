package main

// These tests drive the two routes that name an endpoint: the info
// document and the tap. The harness is the one in api_test.go, so what
// they exercise is the whole path from the public request to the
// capture container and back.

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestANameTheClusterDoesNotHoldIsANotFound(t *testing.T) {
	harness := newAPIHarness(t)
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown Sink answered %s", answer.Status)
	}
	document := readProblemBody(t, answer)
	if document.Type != problemNoNode {
		t.Errorf("the problem type is %q", document.Type)
	}
}

func TestAnEndpointWithNoNodeIsAway(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "", "")
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusConflict {
		t.Fatalf("an endpoint with no node answered %s", answer.Status)
	}
	document := readProblemBody(t, answer)
	if document.Type != problemAway {
		t.Errorf("the problem type is %q, want %q", document.Type, problemAway)
	}
	// A 409 is used only where the caller can act, and the detail says
	// what clears it.
	if !strings.Contains(document.Detail, "power the device on") {
		t.Errorf("the detail is %q", document.Detail)
	}
}

func TestANodeWithNoReadyContainerIsUnavailable(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-9")
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a node with no container answered %s", answer.Status)
	}
	if got := answer.Header.Get("Retry-After"); got != "5" {
		t.Errorf("the refusal says Retry-After: %q", got)
	}
}

func TestATapForwardsTheNodeNameAndTheQuery(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-1")
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/kitchen/audio.opus?t=5,7&bitrate=128", nil)
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the tap answered %s: %s", answer.Status, body)
	}
	_, _ = io.Copy(io.Discard, answer.Body)

	called := harness.container.called()
	if len(called) != 1 {
		t.Fatalf("the container was called %d times", len(called))
	}
	// The API forwards the node the Sink's status names, not the Sink's
	// own name, because the container resolves a PipeWire node.
	if called[0].Path != "/v1/audio/sinks/alsa_output.kitchen/audio.opus" {
		t.Errorf("the private path is %q", called[0].Path)
	}
	if called[0].RawQuery != "t=5,7&bitrate=128" {
		t.Errorf("the private query is %q", called[0].RawQuery)
	}
	if harness.container.token != "Bearer private.leg.token" {
		t.Errorf("the private leg carried %q", harness.container.token)
	}
}

func TestATapAddsTheFourHeadersTheAPIOwns(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-1")
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio", nil)
	_, _ = io.Copy(io.Discard, answer.Body)

	if got := answer.Header.Get("Vary"); got != "Accept" {
		t.Errorf("the tap says Vary: %q", got)
	}
	if got := answer.Header.Get("Content-Location"); got != "/v1/audio/sinks/kitchen/audio.wav" {
		t.Errorf("the tap says Content-Location: %q", got)
	}
	if got := answer.Header.Get("Content-Disposition"); !strings.Contains(got, `filename="kitchen-`) {
		t.Errorf("the tap says Content-Disposition: %q", got)
	}
	if got := answer.Header.Get("Link"); !strings.Contains(got, `rel="service-desc"`) {
		t.Errorf("the tap says Link: %q", got)
	}
	// The container's own headers are relayed unchanged.
	if got := answer.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("the tap says Cache-Control: %q", got)
	}
	if got := answer.Header.Get("Accept-Ranges"); got != "none" {
		t.Errorf("the tap says Accept-Ranges: %q", got)
	}
}

func TestATapThatProducedBytesWritesACapturedEvent(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-1")
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.flac", nil)
	_, _ = io.Copy(io.Discard, answer.Body)

	written := harness.cluster.recorded()
	if len(written) != 1 {
		t.Fatalf("%d events were written", len(written))
	}
	if written[0].Reason != capturedReason || written[0].Type != "Normal" {
		t.Errorf("the event is %s/%s", written[0].Reason, written[0].Type)
	}
	if written[0].InvolvedObject.Kind != SinkKind || written[0].InvolvedObject.Name != "kitchen" {
		t.Errorf("the event is about %+v", written[0].InvolvedObject)
	}
	if !strings.Contains(written[0].Message, "listener") ||
		!strings.Contains(written[0].Message, "audio") {
		t.Errorf("the message is %q, and it must name the subject and the aspect",
			written[0].Message)
	}
}

func TestAHeadTakesNoSampleAndMakesNoPrivateCall(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-1")
	answer := harness.call(t, http.MethodHead, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the HEAD answered %s", answer.Status)
	}
	if got := answer.Header.Get("Content-Type"); got != "audio/wav" {
		t.Errorf("the HEAD says Content-Type: %q", got)
	}
	if got := answer.Header.Get("Content-Disposition"); got == "" {
		t.Error("the HEAD carries no Content-Disposition")
	}
	body, _ := io.ReadAll(answer.Body)
	if len(body) != 0 {
		t.Errorf("the HEAD carried %d bytes", len(body))
	}
	if called := harness.container.called(); len(called) != 0 {
		t.Errorf("the HEAD called the container %d times", len(called))
	}
	if written := harness.cluster.recorded(); len(written) != 0 {
		t.Errorf("the HEAD wrote %d events", len(written))
	}
}

func TestAHeadOnAnErrorCarriesNoBody(t *testing.T) {
	harness := newAPIHarness(t)
	answer := harness.call(t, http.MethodHead, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusNotFound {
		t.Fatalf("the HEAD answered %s", answer.Status)
	}
	body, _ := io.ReadAll(answer.Body)
	if len(body) != 0 {
		t.Errorf("a HEAD on an error carried %d bytes", len(body))
	}
}

func TestAnAcceptTheRouteCannotServeIsA406WithItsSiblings(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-1")
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio",
		http.Header{"Accept": {"audio/mpeg"}})
	if answer.StatusCode != http.StatusNotAcceptable {
		t.Fatalf("Accept: audio/mpeg answered %s", answer.Status)
	}
	if got := answer.Header.Get("Vary"); got != "Accept" {
		t.Errorf("the 406 says Vary: %q", got)
	}
	document := readProblemBody(t, answer)
	if document.Type != problemNotAcceptable {
		t.Errorf("the problem type is %q", document.Type)
	}
	want := []acceptableType{
		{Type: "audio/wav", Href: "/v1/audio/sinks/kitchen/audio.wav"},
		{Type: "audio/flac", Href: "/v1/audio/sinks/kitchen/audio.flac"},
		{Type: "audio/ogg", Href: "/v1/audio/sinks/kitchen/audio.opus"},
	}
	if len(document.Acceptable) != 3 {
		t.Fatalf("acceptable is %v", document.Acceptable)
	}
	for index, entry := range want {
		if document.Acceptable[index] != entry {
			t.Errorf("acceptable[%d] is %v, want %v", index, document.Acceptable[index], entry)
		}
	}
	if len(harness.container.called()) != 0 {
		t.Error("a 406 reached the capture container")
	}
}

func TestTheNegotiationChoosesTheForwardedExtension(t *testing.T) {
	cases := map[string]string{
		"":    "audio.wav",
		"*/*": "audio.wav",
		"audio/ogg; codecs=opus, audio/flac;q=0.5": "audio.opus",
		"audio/flac;q=0.5, audio/ogg;q=0.5":        "audio.flac",
		"audio/wav;q=0, */*":                       "audio.flac",
	}
	for accept, want := range cases {
		harness := newAPIHarness(t)
		harness.holds("kitchen", "alsa_output.kitchen", "node-1")
		headers := http.Header{}
		if accept != "" {
			headers.Set("Accept", accept)
		}
		answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio", headers)
		_, _ = io.Copy(io.Discard, answer.Body)
		called := harness.container.called()
		if len(called) != 1 {
			t.Fatalf("Accept: %q called the container %d times", accept, len(called))
		}
		if !strings.HasSuffix(called[0].Path, want) {
			t.Errorf("Accept: %q forwarded %q, want a path ending %s", accept, called[0].Path, want)
		}
	}
}

func TestTheContainersProblemIsRelayedWithThisRequestsInstance(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-1")
	harness.container.answers(problem{
		Type:     problemWrongTarget,
		Title:    "Wrong target",
		Status:   http.StatusInternalServerError,
		Detail:   "pw-record linked to a node other than the 46 this request named",
		Instance: "/v1/audio/sinks/alsa_output.kitchen/audio.wav#deadbeef",
	})
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusInternalServerError {
		t.Fatalf("a wrong target answered %s", answer.Status)
	}
	document := readProblemBody(t, answer)
	if document.Type != problemWrongTarget {
		t.Errorf("the problem type is %q", document.Type)
	}
	if !strings.Contains(document.Detail, "pw-record linked to a node other than") {
		t.Errorf("the detail is %q, and it must carry the container's own words", document.Detail)
	}
	// The instance names the route the caller called, not the private
	// one.
	if !strings.HasPrefix(document.Instance, "/v1/audio/sinks/kitchen/audio.wav#") {
		t.Errorf("the instance is %q", document.Instance)
	}
}

func TestAnAnswerThatIsNotAProblemDocumentIsABadGateway(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-1")
	harness.container.mu.Lock()
	harness.container.status = http.StatusInternalServerError
	harness.container.body = []byte("<html>a proxy wrote this</html>")
	harness.container.mu.Unlock()

	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusBadGateway {
		t.Fatalf("an answer that is not a problem document answered %s", answer.Status)
	}
	document := readProblemBody(t, answer)
	if document.Type != problemUpstreamFailed {
		t.Errorf("the problem type is %q, want %q", document.Type, problemUpstreamFailed)
	}
}

func TestTheInfoDocumentJoinsTheObjectAndTheGraph(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-1")
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen", nil)
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the info route answered %s: %s", answer.Status, body)
	}
	var document endpointDocument
	if err := json.NewDecoder(answer.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.Node != "alsa_output.kitchen" || document.Rate != 48000 || document.Channels != 2 {
		t.Errorf("the info document is %+v", document)
	}
	if document.ConnectionType != "usb" {
		t.Errorf("the connection type is %q", document.ConnectionType)
	}
	if len(document.MediaTypes) != 3 {
		t.Errorf("the info document publishes %v", document.MediaTypes)
	}
	// An info document links to the routes that tap it, and carries no
	// capture-only header.
	if got := answer.Header.Get("Link"); !strings.Contains(got, `rel="related"`) {
		t.Errorf("the info document says Link: %q", got)
	}
	for _, absent := range []string{"Content-Disposition", "Accept-Ranges", "Content-Location"} {
		if got := answer.Header.Get(absent); got != "" {
			t.Errorf("the info document carries %s: %q", absent, got)
		}
	}
}

func TestOptionsAndTheOtherMethods(t *testing.T) {
	harness := newAPIHarness(t)
	answer := harness.call(t, http.MethodOptions, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusNoContent {
		t.Errorf("OPTIONS answered %s", answer.Status)
	}
	if got := answer.Header.Get("Allow"); got != "GET, HEAD, OPTIONS" {
		t.Errorf("OPTIONS says Allow: %q", got)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		answer := harness.call(t, method, "/v1/audio/sinks/kitchen/audio.wav", nil)
		if answer.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s answered %s", method, answer.Status)
		}
		if got := answer.Header.Get("Allow"); got != "GET, HEAD, OPTIONS" {
			t.Errorf("%s got Allow: %q", method, got)
		}
		_, _ = io.Copy(io.Discard, answer.Body)
	}
}

func TestEveryRouteInTheTableAnswersThroughTheRouter(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-1")
	harness.cluster.mu.Lock()
	harness.cluster.sources["desk"] = Source{
		Metadata: EndpointMeta{Name: "desk"},
		Status:   EndpointStatus{Node: "alsa_input.desk", NodeName: "node-1"},
	}
	harness.cluster.mu.Unlock()

	for _, route := range apiRoutes {
		name := "kitchen"
		if route.Resource == "sources" {
			name = "desk"
		}
		target := route.path(name)
		answer := harness.call(t, http.MethodGet, target, nil)
		if answer.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(answer.Body)
			t.Errorf("%s answered %s: %s", target, answer.Status, body)
			continue
		}
		if got := answer.Header.Get("Vary"); got != "Accept" {
			t.Errorf("%s says Vary: %q", target, got)
		}
		if got := answer.Header.Get("Link"); !strings.Contains(got, `rel="service-desc"`) {
			t.Errorf("%s says Link: %q", target, got)
		}
		_, _ = io.Copy(io.Discard, answer.Body)
	}
}

func TestAPathOutsideTheTableIsANotFound(t *testing.T) {
	harness := newAPIHarness(t)
	for _, target := range []string{"/", "/v1", "/v1/audio/sinks/kitchen/levels"} {
		answer := harness.call(t, http.MethodGet, target, nil)
		if answer.StatusCode != http.StatusNotFound {
			t.Errorf("%s answered %s", target, answer.Status)
		}
		_, _ = io.Copy(io.Discard, answer.Body)
	}
}

func TestAHeadOnTheInfoRouteMakesNoPrivateCall(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-1")
	answer := harness.call(t, http.MethodHead, "/v1/audio/sinks/kitchen", nil)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the HEAD answered %s", answer.Status)
	}
	if got := answer.Header.Get("Content-Type"); got != documentType {
		t.Errorf("the HEAD says Content-Type: %q", got)
	}
	if answer.Header.Get("ETag") == "" {
		t.Error("the HEAD carries no ETag")
	}
	body, _ := io.ReadAll(answer.Body)
	if len(body) != 0 {
		t.Errorf("the HEAD carried %d bytes", len(body))
	}
	if called := harness.container.called(); len(called) != 0 {
		t.Errorf("the HEAD called the container %d times", len(called))
	}
}

func TestAContainerThatAnsweredNoHTTPIsABadGateway(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-1")
	// A listener that answers bytes no transport can read as HTTP. The
	// rulings put that at 502, apart from a refused connection at 503.
	broken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broken.Close() })
	go func() {
		for {
			connection, err := broken.Accept()
			if err != nil {
				return
			}
			_, _ = connection.Write([]byte("this is not HTTP at all\r\n\r\n"))
			_ = connection.Close()
		}
	}()
	harness.server.relay.address = broken.Addr().String()

	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("an answer that is not HTTP answered %s: %s", answer.Status, body)
	}
	document := readProblemBody(t, answer)
	if document.Type != problemUpstreamFailed {
		t.Errorf("the problem type is %q", document.Type)
	}
}

func TestAContainerThatRefusedTheConnectionIsUnavailable(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "alsa_output.kitchen", "node-1")
	// A port nothing listens on: the connection is refused, which the
	// rulings put at 503 with Retry-After.
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := closed.Addr().String()
	_ = closed.Close()
	harness.server.relay.address = address

	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a refused connection answered %s", answer.Status)
	}
	if got := answer.Header.Get("Retry-After"); got != "5" {
		t.Errorf("the refusal says Retry-After: %q", got)
	}
	_, _ = io.Copy(io.Discard, answer.Body)
}
