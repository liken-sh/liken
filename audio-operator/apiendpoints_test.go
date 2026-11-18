package main

// These tests send requests to the info and tap routes through the
// harness in api_test.go, so each one covers the path from the public
// request to the capture container and back.

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestANameTheClusterDoesNotHoldIsANotFound(t *testing.T) {
	harness := newAPIHarness(t)
	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("the request for an unknown Sink returned %s, want 404 Not Found", response.Status)
	}
	document := readProblemBody(t, response)
	if document.Type != problemNoNode {
		t.Errorf("the problem type is %q, want %q", document.Type, problemNoNode)
	}
}

func TestAnEndpointWithNoNodeIsAway(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "", "")
	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("the request for an endpoint with no node returned %s, want 409 Conflict", response.Status)
	}
	document := readProblemBody(t, response)
	if document.Type != problemAway {
		t.Errorf("the problem type is %q, want %q", document.Type, problemAway)
	}
	if !strings.Contains(document.Detail, "power the device on") {
		t.Errorf("the detail is %q, want a detail that contains \"power the device on\"", document.Detail)
	}
}

func TestANodeWithNoReadyContainerIsUnavailable(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-9", drillPipeWireNode)
	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("the request to a node with no Ready container returned %s, want 503 Service Unavailable", response.Status)
	}
	if got := response.Header.Get("Retry-After"); got != "5" {
		t.Errorf("the refusal says Retry-After: %q, want 5", got)
	}
}

func TestATapForwardsTheNodeNameAndTheQuery(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/kitchen/audio.opus?t=5,7&bitrate=128", nil)
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("the tap returned %s, want 200 OK: %s", response.Status, body)
	}
	_, _ = io.Copy(io.Discard, response.Body)

	called := harness.container.called()
	if len(called) != 1 {
		t.Fatalf("the container was called %d times, want 1", len(called))
	}
	// The path has status.nodeName, not the Sink's name or the machine
	// in status.node, because the container looks up a PipeWire node.
	if called[0].Path != "/v1/audio/sinks/"+drillPipeWireNode+"/audio.opus" {
		t.Errorf("the private path is %q, want /v1/audio/sinks/%s/audio.opus", called[0].Path, drillPipeWireNode)
	}
	if called[0].RawQuery != "t=5,7&bitrate=128" {
		t.Errorf("the private query is %q, want t=5,7&bitrate=128", called[0].RawQuery)
	}
	if harness.container.token != "Bearer private.leg.token" {
		t.Errorf("the private leg carried the token %q, want Bearer private.leg.token", harness.container.token)
	}
}

func TestATapCarriesTheHeadersTheAPISets(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio", nil)
	_, _ = io.Copy(io.Discard, response.Body)

	if got := response.Header.Get("Vary"); got != "Accept" {
		t.Errorf("the tap says Vary: %q, want Accept", got)
	}
	if got := response.Header.Get("Content-Location"); got != "/v1/audio/sinks/kitchen/audio.wav" {
		t.Errorf("the tap says Content-Location: %q, want /v1/audio/sinks/kitchen/audio.wav", got)
	}
	if got := response.Header.Get("Content-Disposition"); !strings.Contains(got, `filename="kitchen-`) {
		t.Errorf("the tap says Content-Disposition: %q, want a filename that starts with kitchen-", got)
	}
	if got := response.Header.Get("Link"); !strings.Contains(got, `rel="service-desc"`) {
		t.Errorf("the tap says Link: %q, want a link with rel=\"service-desc\"", got)
	}
	// On a 200 the API sets these itself and copies none of the
	// container's headers.
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("the tap says Cache-Control: %q, want no-store", got)
	}
	if got := response.Header.Get("Accept-Ranges"); got != "none" {
		t.Errorf("the tap says Accept-Ranges: %q, want none", got)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("the tap says X-Content-Type-Options: %q, want nosniff", got)
	}
}

func TestATapThatProducedBytesWritesACapturedEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		harness := newAPIHarness(t)
		harness.addSink("kitchen", "node-1", drillPipeWireNode)
		response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.flac", nil)
		_, _ = io.Copy(io.Discard, response.Body)

		written := harness.cluster.recorded()
		if len(written) != 1 {
			t.Fatalf("the tap wrote %d Events, want 1", len(written))
		}
		if written[0].Reason != reasonCaptured || written[0].Type != "Normal" {
			t.Errorf("the Event is %s/%s, want %s/Normal", written[0].Reason, written[0].Type, reasonCaptured)
		}
		if written[0].InvolvedObject.Kind != SinkKind || written[0].InvolvedObject.Name != "kitchen" {
			t.Errorf("the Event is about %+v, want the Sink kitchen", written[0].InvolvedObject)
		}
		if !strings.Contains(written[0].Message, "listener") ||
			!strings.Contains(written[0].Message, "audio") {
			t.Errorf("the message is %q, want one that names the subject listener and the aspect audio",
				written[0].Message)
		}
	})
}

func TestAHeadTakesNoSampleAndMakesNoPrivateCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		harness := newAPIHarness(t)
		harness.addSink("kitchen", "node-1", drillPipeWireNode)
		response := harness.call(t, http.MethodHead, "/v1/audio/sinks/kitchen/audio.wav", nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("the HEAD returned %s, want 200 OK", response.Status)
		}
		if got := response.Header.Get("Content-Type"); got != "audio/wav" {
			t.Errorf("the HEAD says Content-Type: %q, want audio/wav", got)
		}
		if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("the HEAD says X-Content-Type-Options: %q, want nosniff", got)
		}
		if got := response.Header.Get("Content-Disposition"); got == "" {
			t.Error("the HEAD carries no Content-Disposition")
		}
		body, _ := io.ReadAll(response.Body)
		if len(body) != 0 {
			t.Errorf("the HEAD carried %d bytes, want none", len(body))
		}
		if called := harness.container.called(); len(called) != 0 {
			t.Errorf("the HEAD called the container %d times, want 0", len(called))
		}
		if written := harness.cluster.recorded(); len(written) != 0 {
			t.Errorf("the HEAD wrote %d Events, want none", len(written))
		}
	})
}

func TestAHeadOnAnErrorCarriesNoBody(t *testing.T) {
	harness := newAPIHarness(t)
	response := harness.call(t, http.MethodHead, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("the HEAD returned %s, want 404 Not Found", response.Status)
	}
	body, _ := io.ReadAll(response.Body)
	if len(body) != 0 {
		t.Errorf("a HEAD on an error carried %d bytes, want none", len(body))
	}
}

func TestAnAcceptTheRouteCannotServeIsA406WithItsSiblings(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio",
		http.Header{"Accept": {"audio/mpeg"}})
	if response.StatusCode != http.StatusNotAcceptable {
		t.Fatalf("a request with Accept: audio/mpeg returned %s, want 406 Not Acceptable", response.Status)
	}
	if got := response.Header.Get("Vary"); got != "Accept" {
		t.Errorf("the 406 says Vary: %q, want Accept", got)
	}
	document := readProblemBody(t, response)
	if document.Type != problemNotAcceptable {
		t.Errorf("the problem type is %q, want %q", document.Type, problemNotAcceptable)
	}
	want := []acceptableType{
		{Type: "audio/wav", Href: "/v1/audio/sinks/kitchen/audio.wav"},
		{Type: "audio/flac", Href: "/v1/audio/sinks/kitchen/audio.flac"},
		{Type: "audio/ogg", Href: "/v1/audio/sinks/kitchen/audio.opus"},
	}
	if len(document.Acceptable) != 3 {
		t.Fatalf("acceptable is %v, want 3 entries", document.Acceptable)
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
		harness.addSink("kitchen", "node-1", drillPipeWireNode)
		headers := http.Header{}
		if accept != "" {
			headers.Set("Accept", accept)
		}
		response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio", headers)
		_, _ = io.Copy(io.Discard, response.Body)
		called := harness.container.called()
		if len(called) != 1 {
			t.Fatalf("Accept: %q called the container %d times, want 1", accept, len(called))
		}
		if !strings.HasSuffix(called[0].Path, want) {
			t.Errorf("Accept: %q forwarded %q, want a path ending %s", accept, called[0].Path, want)
		}
	}
}

func TestTheContainersProblemIsRelayedWithThisRequestsInstance(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	harness.container.returnsProblem(problem{
		Type:     problemWrongTarget,
		Title:    "Wrong target",
		Status:   http.StatusInternalServerError,
		Detail:   "pw-record linked to a node other than the 46 this request named",
		Instance: "/v1/audio/sinks/" + drillPipeWireNode + "/audio.wav#deadbeef",
	})
	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("a wrong target returned %s, want 500 Internal Server Error", response.Status)
	}
	document := readProblemBody(t, response)
	if document.Type != problemWrongTarget {
		t.Errorf("the problem type is %q, want %q", document.Type, problemWrongTarget)
	}
	if !strings.Contains(document.Detail, "pw-record linked to a node other than") {
		t.Errorf("the detail is %q, want the container's own words", document.Detail)
	}
	// The instance names the route the caller called, not the private
	// one.
	if !strings.HasPrefix(document.Instance, "/v1/audio/sinks/kitchen/audio.wav#") {
		t.Errorf("the instance is %q, want the public path /v1/audio/sinks/kitchen/audio.wav#<id>", document.Instance)
	}
}

func TestAResponseThatIsNotAProblemDocumentIsABadGateway(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	harness.container.mu.Lock()
	harness.container.status = http.StatusInternalServerError
	harness.container.body = []byte("<html>a proxy wrote this</html>")
	harness.container.mu.Unlock()

	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("a response that is not a problem document returned %s, want 502 Bad Gateway", response.Status)
	}
	document := readProblemBody(t, response)
	if document.Type != problemUpstreamFailed {
		t.Errorf("the problem type is %q, want %q", document.Type, problemUpstreamFailed)
	}
}

func TestTheInfoDocumentJoinsTheObjectAndTheGraph(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen", nil)
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("the info route returned %s, want 200 OK: %s", response.Status, body)
	}
	var document endpointDocument
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatalf("decoding the info document: %v", err)
	}
	// node is the machine, and nodeName is the PipeWire node that a tap
	// targets, spelled as in the CRD's status.
	if document.Node != "node-1" || document.NodeName != drillPipeWireNode {
		t.Errorf("the info document names %q and %q, want node-1 and %s", document.Node, document.NodeName, drillPipeWireNode)
	}
	if document.Rate != 48000 || document.Channels != 2 {
		t.Errorf("the info document is %+v, want a rate of 48000 and 2 channels", document)
	}
	if document.ConnectionType != "usb" {
		t.Errorf("the connection type is %q, want usb", document.ConnectionType)
	}
	if len(document.MediaTypes) != 3 {
		t.Errorf("the info document publishes the media types %v, want 3", document.MediaTypes)
	}
	// The info document links to the tap routes and has none of the
	// headers that only a capture has.
	if got := response.Header.Get("Link"); !strings.Contains(got, `rel="related"`) {
		t.Errorf("the info document says Link: %q, want a link with rel=\"related\"", got)
	}
	for _, absent := range []string{"Content-Disposition", "Accept-Ranges", "Content-Location"} {
		if got := response.Header.Get(absent); got != "" {
			t.Errorf("the info document carries %s: %q, want no such header", absent, got)
		}
	}
}

func TestOptionsAndTheOtherMethods(t *testing.T) {
	harness := newAPIHarness(t)
	response := harness.call(t, http.MethodOptions, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("OPTIONS returned %s, want 204 No Content", response.Status)
	}
	if got := response.Header.Get("Allow"); got != "GET, HEAD, OPTIONS" {
		t.Errorf("OPTIONS says Allow: %q, want GET, HEAD, OPTIONS", got)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		response := harness.call(t, method, "/v1/audio/sinks/kitchen/audio.wav", nil)
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s returned %s, want 405 Method Not Allowed", method, response.Status)
		}
		if got := response.Header.Get("Allow"); got != "GET, HEAD, OPTIONS" {
			t.Errorf("%s got Allow: %q, want GET, HEAD, OPTIONS", method, got)
		}
		_, _ = io.Copy(io.Discard, response.Body)
	}
}

func TestEveryRouteInTheTableRespondsThroughTheRouter(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	harness.cluster.mu.Lock()
	harness.cluster.sources["desk"] = Source{
		Metadata: EndpointMeta{Name: "desk"},
		Status:   EndpointStatus{Node: "node-1", NodeName: "liken.audio.card1-pcm0c"},
	}
	harness.cluster.mu.Unlock()

	for _, route := range apiRoutes {
		name := "kitchen"
		if route.Resource == "sources" {
			name = "desk"
		}
		target := route.path(name)
		response := harness.call(t, http.MethodGet, target, nil)
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			t.Errorf("%s returned %s, want 200 OK: %s", target, response.Status, body)
			continue
		}
		if got := response.Header.Get("Vary"); got != "Accept" {
			t.Errorf("%s says Vary: %q, want Accept", target, got)
		}
		if got := response.Header.Get("Link"); !strings.Contains(got, `rel="service-desc"`) {
			t.Errorf("%s says Link: %q, want a link with rel=\"service-desc\"", target, got)
		}
		_, _ = io.Copy(io.Discard, response.Body)
	}
}

func TestAPathOutsideTheTableIsANotFound(t *testing.T) {
	harness := newAPIHarness(t)
	for _, target := range []string{"/", "/v1", "/v1/audio/sinks/kitchen/levels"} {
		response := harness.call(t, http.MethodGet, target, nil)
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s returned %s, want 404 Not Found", target, response.Status)
		}
		_, _ = io.Copy(io.Discard, response.Body)
	}
}

func TestAHeadOnTheInfoRouteMakesNoPrivateCall(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	response := harness.call(t, http.MethodHead, "/v1/audio/sinks/kitchen", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the HEAD returned %s, want 200 OK", response.Status)
	}
	if got := response.Header.Get("Content-Type"); got != documentType {
		t.Errorf("the HEAD says Content-Type: %q, want %s", got, documentType)
	}
	if response.Header.Get("ETag") == "" {
		t.Error("the HEAD carries no ETag")
	}
	body, _ := io.ReadAll(response.Body)
	if len(body) != 0 {
		t.Errorf("the HEAD carried %d bytes, want none", len(body))
	}
	if called := harness.container.called(); len(called) != 0 {
		t.Errorf("the HEAD called the container %d times, want 0", len(called))
	}
}

// hijacked returns a container handler that takes over the connection
// and passes it to write, so a test can send bytes that are not HTTP.
func hijacked(t *testing.T, write func(net.Conn)) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		connection, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijacking the container's connection: %v", err)
			return
		}
		write(connection)
		_ = connection.Close()
	}
}

func TestAContainerThatSentNoHTTPIsABadGateway(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	// The container writes a line that is not HTTP and closes the
	// connection. Go's error text for that varies with when the close
	// arrives, so the test checks the status and the problem type, not
	// the text.
	harness.container.streams(hijacked(t, func(connection net.Conn) {
		_, _ = connection.Write([]byte("this is not HTTP at all\n"))
	}))

	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("a response that is not HTTP returned %s, want 502 Bad Gateway: %s", response.Status, body)
	}
	document := readProblemBody(t, response)
	if document.Type != problemUpstreamFailed {
		t.Errorf("the problem type is %q, want %q", document.Type, problemUpstreamFailed)
	}
}

// A container that accepts the connection and closes it without a
// response is a 502, like one that sends garbage, because the
// connection was made and no response could be read.
func TestAContainerThatClosedBeforeRespondingIsABadGateway(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.put(samplePod(drillMachine))
	harness.container.streams(hijacked(t, func(net.Conn) {}))

	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("the request to a container that closed at once returned %s, want 502 Bad Gateway: %s", response.Status, body)
	}
	if document := readProblemBody(t, response); document.Type != problemUpstreamFailed {
		t.Errorf("the problem type is %q, want %q", document.Type, problemUpstreamFailed)
	}
}

func TestAContainerThatRefusedTheConnectionIsUnavailable(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	// A container that is down refuses the connection with
	// ECONNREFUSED, which the API reports as a 503 with Retry-After.
	harness.container.server.SetDown(true)

	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a refused connection returned %s, want 503 Service Unavailable", response.Status)
	}
	if got := response.Header.Get("Retry-After"); got != "5" {
		t.Errorf("the refusal says Retry-After: %q, want 5", got)
	}
	_, _ = io.Copy(io.Discard, response.Body)
}

// status.node is the machine, and status.nodeName is the PipeWire node.
// If the API used one where it needs the other, it would look up the
// pod by a name that no node has and send a machine name to the
// container, and every route would return 503. The two names in this
// test are unlike, so a swap fails it.
func TestTheMachineAndThePipeWireNodeAreNotInterchangeable(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.put(samplePod(drillMachine))

	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("the tap returned %s, want 200 OK: %s", response.Status, body)
	}
	_, _ = io.Copy(io.Discard, response.Body)

	called := harness.container.called()
	if len(called) != 1 {
		t.Fatalf("the container was called %d times, want 1", len(called))
	}
	// The pod lookup used the machine, and the path uses the PipeWire
	// node.
	if called[0].Path != "/v1/audio/sinks/"+drillPipeWireNode+"/audio.wav" {
		t.Errorf("the private path is %q, want the PipeWire node", called[0].Path)
	}
	if strings.Contains(called[0].Path, drillMachine) {
		t.Error("the API requested a machine from the capture container")
	}
}

func TestAnEndpointWithAPipeWireNodeAndNoMachineIsAway(t *testing.T) {
	// status.nodeName is still set but the machine is gone, which is
	// what an unplugged USB card leaves.
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "", drillPipeWireNode)
	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("the request for an endpoint with no machine returned %s, want 409 Conflict", response.Status)
	}
	if document := readProblemBody(t, response); document.Type != problemAway {
		t.Errorf("the problem type is %q, want %q", document.Type, problemAway)
	}
}

func TestTheInfoRouteTakesTheSameTwoNames(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.put(samplePod(drillMachine))

	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen", nil)
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("the info route returned %s, want 200 OK: %s", response.Status, body)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	called := harness.container.called()
	if len(called) != 1 || called[0].Path != "/v1/audio/sinks/"+drillPipeWireNode {
		t.Errorf("the info route called the container with %v, want one call to /v1/audio/sinks/%s", called, drillPipeWireNode)
	}
}

// The header timeout covers only the wait for the container's headers,
// so a tap can run for many times that timeout.
func TestATapOutlivesTheHeaderBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		harness := newAPIHarness(t)
		harness.addSink("kitchen", drillMachine, drillPipeWireNode)
		harness.server.pods.put(samplePod(drillMachine))

		// The container sends its headers at once, then one block per
		// second for three times headerTimeout.
		blocks := 3 * int(headerTimeout/time.Second)
		harness.container.streams(func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "audio/wav")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			for range blocks {
				time.Sleep(time.Second)
				_, _ = w.Write([]byte("samples!"))
				w.(http.Flusher).Flush()
			}
		})

		started := time.Now()
		response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("the tap returned %s, want 200 OK: %s", response.Status, body)
		}
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("the body ended early: %v", err)
		}
		if len(body) != blocks*len("samples!") {
			t.Errorf("the tap delivered %d bytes, want %d",
				len(body), blocks*len("samples!"))
		}
		if ran := time.Since(started); ran != time.Duration(blocks)*time.Second {
			t.Errorf("the tap ran %s, want %s", ran, time.Duration(blocks)*time.Second)
		}
	})
}

func TestHeadersThatArriveLateAreAGatewayTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		harness := newAPIHarness(t)
		harness.addSink("kitchen", drillMachine, drillPipeWireNode)
		harness.server.pods.put(samplePod(drillMachine))

		harness.container.streams(func(w http.ResponseWriter) {
			time.Sleep(headerTimeout + time.Second)
			w.WriteHeader(http.StatusOK)
		})

		started := time.Now()
		response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
		if waited := time.Since(started); waited != headerTimeout {
			t.Errorf("the API gave up after %s, want %s", waited, headerTimeout)
		}
		if response.StatusCode != http.StatusGatewayTimeout {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("late headers returned %s, want 504 Gateway Timeout: %s", response.Status, body)
		}
		document := readProblemBody(t, response)
		if document.Type != problemUpstreamFailed {
			t.Errorf("the problem type is %q, want %q", document.Type, problemUpstreamFailed)
		}
		if !strings.Contains(document.Detail, "sent no headers in time") {
			t.Errorf("the detail is %q, want a detail that contains \"sent no headers in time\"", document.Detail)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		// The container's handler must finish before the synctest bubble
		// ends.
		time.Sleep(time.Second)
	})
}

// The header timeout includes the span's begin offset, so a tap that
// discards audio first is not cut off before the container responds.
func TestTheBoundAllowsForTheSpansOwnBegin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		harness := newAPIHarness(t)
		harness.addSink("kitchen", drillMachine, drillPipeWireNode)
		harness.server.pods.put(samplePod(drillMachine))

		harness.container.streams(func(w http.ResponseWriter) {
			time.Sleep(headerTimeout + 100*time.Millisecond)
			w.Header().Set("Content-Type", "audio/wav")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("samples!"))
		})

		// The container sends its headers 100 ms after headerTimeout, so
		// without the begin offset the API would give up first. The span
		// t=0.2,0.3 adds 200 ms to the timeout.
		response := harness.call(t, http.MethodGet,
			"/v1/audio/sinks/kitchen/audio.wav?t=0.2,0.3", nil)
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("a tap with a begin returned %s, want 200 OK: %s", response.Status, body)
		}
		_, _ = io.Copy(io.Discard, response.Body)
	})
}

// When the container ends its body without the final chunk, the API
// aborts its own response, so the caller sees an error and not a clean
// end of the body.
func TestAContainerThatCutTheBodyShortCutsThePublicOneToo(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.put(samplePod(drillMachine))

	// The container sends a 200 and some bytes, then aborts the handler,
	// as it does when the capture pipeline dies.
	harness.container.streams(func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("RIFFsamples"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})

	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the tap returned %s, want 200 OK", response.Status)
	}
	if _, err := io.ReadAll(response.Body); err == nil {
		t.Fatal("a body the container cut short arrived as though it were complete")
	}

	// The API writes the log line after the abort too, with the failure.
	line := harness.lines.last()
	if !strings.Contains(line, "the capture container ended the body part way") {
		t.Errorf("the API logged %q, want a line that contains \"the capture container ended the body part way\"", line)
	}
	if !strings.Contains(line, "status=200") {
		t.Errorf("the API logged %q, want a line that contains status=200", line)
	}
}

func TestABodyTheContainerFinishedArrivesComplete(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.put(samplePod(drillMachine))

	harness.container.streams(func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("RIFFsamples"))
	})

	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading a finished body: %v", err)
	}
	if string(body) != "RIFFsamples" {
		t.Errorf("the body is %q, want RIFFsamples", body)
	}
	if line := harness.lines.last(); !strings.Contains(line, "ended=ok") {
		t.Errorf("the API logged %q, want a line that contains ended=ok", line)
	}
}

func TestThePublicLegLogsEveryRequestWhateverBecameOfIt(t *testing.T) {
	// The log line is written from a defer, so every request is
	// logged, whether it ends normally, aborts, or never streams.
	harness := newAPIHarness(t)
	response := harness.call(t, http.MethodGet, "/v1/audio", nil)
	_, _ = io.Copy(io.Discard, response.Body)
	if line := harness.lines.last(); !strings.Contains(line, "ended=ok") ||
		!strings.Contains(line, "route=/v1/audio") {
		t.Errorf("the discovery document logged %q, want a line with ended=ok and route=/v1/audio", line)
	}
}

// The caller receives the headers as soon as the container sends them,
// not with the first block of audio. A tap with a begin offset sends no
// audio before begin, so otherwise the caller would wait that
// long without a status or headers.
func TestTheRelayedHeadersReachTheCallerBeforeTheFirstBodyByte(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		harness := newAPIHarness(t)
		harness.addSink("kitchen", drillMachine, drillPipeWireNode)
		harness.server.pods.put(samplePod(drillMachine))

		const silent = time.Minute
		harness.container.streams(func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "audio/flac")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			_ = http.NewResponseController(w).Flush()
			time.Sleep(silent)
			_, _ = w.Write([]byte("fLaC"))
			_ = http.NewResponseController(w).Flush()
		})

		// call returns when the headers arrive, so this measures the wait
		// before the client knows the status and the type.
		started := time.Now()
		response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.flac?t=60,61", nil)
		if waited := time.Since(started); waited != 0 {
			t.Errorf("the headers took %s, want 0 because the container sent them at once", waited)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("the tap returned %s, want 200 OK", response.Status)
		}
		if got := response.Header.Get("Content-Type"); got != "audio/flac" {
			t.Errorf("the type is %q, want audio/flac", got)
		}
		if response.Header.Get("Content-Disposition") == "" {
			t.Error("the save name is not in the headers")
		}
		if got := response.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("the cache directive is %q, want no-store", got)
		}

		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("reading the body: %v", err)
		}
		if string(body) != "fLaC" {
			t.Errorf("the body is %q, want fLaC", body)
		}
		if total := time.Since(started); total != silent {
			t.Errorf("the whole tap took %s, want the container's silence of %s", total, silent)
		}
	})
}
