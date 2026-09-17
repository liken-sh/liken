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
	"time"
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
	harness.holds("kitchen", "node-9", drillPipeWireNode)
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
	harness.holds("kitchen", "node-1", drillPipeWireNode)
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
	// The API forwards the PipeWire node in status.nodeName, not the
	// Sink's own name and not the machine in status.node, because what
	// the container resolves is a node in the graph.
	if called[0].Path != "/v1/audio/sinks/"+drillPipeWireNode+"/audio.opus" {
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
	harness.holds("kitchen", "node-1", drillPipeWireNode)
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
	harness.holds("kitchen", "node-1", drillPipeWireNode)
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
	harness.holds("kitchen", "node-1", drillPipeWireNode)
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
	harness.holds("kitchen", "node-1", drillPipeWireNode)
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
		harness.holds("kitchen", "node-1", drillPipeWireNode)
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
	harness.holds("kitchen", "node-1", drillPipeWireNode)
	harness.container.answers(problem{
		Type:     problemWrongTarget,
		Title:    "Wrong target",
		Status:   http.StatusInternalServerError,
		Detail:   "pw-record linked to a node other than the 46 this request named",
		Instance: "/v1/audio/sinks/" + drillPipeWireNode + "/audio.wav#deadbeef",
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
	harness.holds("kitchen", "node-1", drillPipeWireNode)
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
	harness.holds("kitchen", "node-1", drillPipeWireNode)
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen", nil)
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the info route answered %s: %s", answer.Status, body)
	}
	var document endpointDocument
	if err := json.NewDecoder(answer.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	// The document carries both names the status carries, under the
	// CRD's own spellings: node is the machine and nodeName is the
	// PipeWire node a tap targets.
	if document.Node != "node-1" || document.NodeName != drillPipeWireNode {
		t.Errorf("the info document names %q and %q", document.Node, document.NodeName)
	}
	if document.Rate != 48000 || document.Channels != 2 {
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
	harness.holds("kitchen", "node-1", drillPipeWireNode)
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
	harness.holds("kitchen", "node-1", drillPipeWireNode)
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
	harness.holds("kitchen", "node-1", drillPipeWireNode)
	// A listener that writes one line no transport can read as HTTP and
	// closes. Go reports that as a malformed response, a peek failure,
	// or an unexpected EOF depending on which side of the read the
	// close lands, so the assertion is on the status and the problem
	// type and never on the words.
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
			_, _ = connection.Write([]byte("this is not HTTP at all\n"))
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

// A connection that stood and then answered nothing at all is the same
// failure as one that answered garbage: the container is there and its
// answer did not read.
func TestAContainerThatClosedBeforeAnsweringIsABadGateway(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.replace([]pod{samplePod(drillMachine)})

	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = silent.Close() })
	go func() {
		for {
			connection, err := silent.Accept()
			if err != nil {
				return
			}
			_ = connection.Close()
		}
	}()
	harness.server.relay.address = silent.Addr().String()

	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("a container that closed at once answered %s: %s", answer.Status, body)
	}
	if document := readProblemBody(t, answer); document.Type != problemUpstreamFailed {
		t.Errorf("the problem type is %q", document.Type)
	}
}

func TestAContainerThatRefusedTheConnectionIsUnavailable(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "node-1", drillPipeWireNode)
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

// status.node is the machine and status.nodeName is the PipeWire node.
// A build that reads one where it needs the other looks the pod up by
// a name no node has and asks the container for a machine, and every
// route answers 503. The two names here look nothing alike, so the
// swap cannot pass.
func TestTheMachineAndThePipeWireNodeAreNotInterchangeable(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.replace([]pod{samplePod(drillMachine)})

	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the tap answered %s: %s", answer.Status, body)
	}
	_, _ = io.Copy(io.Discard, answer.Body)

	called := harness.container.called()
	if len(called) != 1 {
		t.Fatalf("the container was called %d times", len(called))
	}
	// The pod came from the machine, and the path carries the graph's
	// node.
	if called[0].Path != "/v1/audio/sinks/"+drillPipeWireNode+"/audio.wav" {
		t.Errorf("the private path is %q, want the PipeWire node", called[0].Path)
	}
	if strings.Contains(called[0].Path, drillMachine) {
		t.Error("the API asked the capture container for a machine")
	}
}

func TestAnEndpointWithAPipeWireNodeAndNoMachineIsAway(t *testing.T) {
	// PipeWire held a node for it once and the machine is gone, which
	// is the state a USB card that was unplugged leaves behind.
	harness := newAPIHarness(t)
	harness.holds("kitchen", "", drillPipeWireNode)
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusConflict {
		t.Fatalf("an endpoint with no machine answered %s", answer.Status)
	}
	if document := readProblemBody(t, answer); document.Type != problemAway {
		t.Errorf("the problem type is %q", document.Type)
	}
}

func TestTheInfoRouteTakesTheSameTwoNames(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.replace([]pod{samplePod(drillMachine)})

	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen", nil)
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the info route answered %s: %s", answer.Status, body)
	}
	_, _ = io.Copy(io.Discard, answer.Body)
	called := harness.container.called()
	if len(called) != 1 || called[0].Path != "/v1/audio/sinks/"+drillPipeWireNode {
		t.Errorf("the info route asked for %v", called)
	}
}

// A tap runs until the client hangs up or the span ends. The header
// bound covers the wait for the container's status line and nothing
// after it, so a stream that outlives the bound many times over is
// what the route promises. The bound here stands in for the real ten
// seconds, shortened so the test runs in a moment.
func TestATapOutlivesTheHeaderBound(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.replace([]pod{samplePod(drillMachine)})
	harness.server.relay.headers = 40 * time.Millisecond

	// The container answers its headers at once and then delivers a
	// block every 20 ms for well past the bound.
	blocks := 30
	harness.container.streams(func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for range blocks {
			time.Sleep(20 * time.Millisecond)
			_, _ = w.Write([]byte("samples!"))
			w.(http.Flusher).Flush()
		}
	})

	started := time.Now()
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the tap answered %s: %s", answer.Status, body)
	}
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("the body ended early: %v", err)
	}
	ran := time.Since(started)
	if len(body) != blocks*len("samples!") {
		t.Errorf("the tap delivered %d bytes of %d, and it was cut short",
			len(body), blocks*len("samples!"))
	}
	if ran < 4*harness.server.relay.headers {
		t.Errorf("the tap ran %s, which is too short to have outlived the bound", ran)
	}
}

func TestHeadersThatArriveLateAreAGatewayTimeout(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.replace([]pod{samplePod(drillMachine)})
	harness.server.relay.headers = 40 * time.Millisecond

	// The container takes longer than the bound to answer at all.
	harness.container.streams(func(w http.ResponseWriter) {
		time.Sleep(400 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusGatewayTimeout {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("late headers answered %s: %s", answer.Status, body)
	}
	document := readProblemBody(t, answer)
	if document.Type != problemUpstreamFailed {
		t.Errorf("the problem type is %q", document.Type)
	}
	if !strings.Contains(document.Detail, "sent no headers in time") {
		t.Errorf("the detail is %q", document.Detail)
	}
	_, _ = io.Copy(io.Discard, answer.Body)
}

// The span's begin is added to the bound, so a tap that discards a
// minute is not cut off before its first byte.
func TestTheBoundAllowsForTheSpansOwnBegin(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.replace([]pod{samplePod(drillMachine)})
	harness.server.relay.headers = 10 * time.Millisecond

	harness.container.streams(func(w http.ResponseWriter) {
		time.Sleep(120 * time.Millisecond)
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("samples!"))
	})

	// Without the begin the 10 ms bound would cut this off; t=0.2,0.3
	// adds 200 ms to it.
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/kitchen/audio.wav?t=0.2,0.3", nil)
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("a tap with a begin answered %s: %s", answer.Status, body)
	}
	_, _ = io.Copy(io.Discard, answer.Body)
}

// The capture container ends a cut-short body without its terminating
// chunk, which reaches this side as a read that ended with anything
// but EOF. The API tells its own caller the same way, so a truncation
// crosses both legs as a truncation rather than turning into a clean
// end at the door.
func TestAContainerThatCutTheBodyShortCutsThePublicOneToo(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.replace([]pod{samplePod(drillMachine)})

	// The container writes a 200 and some bytes, then ends its handler
	// the way a tap whose pipeline died does.
	harness.container.streams(func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("RIFFsamples"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})

	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the tap answered %s", answer.Status)
	}
	if _, err := io.ReadAll(answer.Body); err == nil {
		t.Fatal("a body the container cut short arrived as though it were complete")
	}

	// The record of the request survives the abort, and it says what
	// happened.
	line := harness.lines.last()
	if !strings.Contains(line, "the capture container ended the body part way") {
		t.Errorf("the API logged %q", line)
	}
	if !strings.Contains(line, "status=200") {
		t.Errorf("the line does not name the status it sent: %q", line)
	}
}

func TestABodyTheContainerFinishedArrivesComplete(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.replace([]pod{samplePod(drillMachine)})

	harness.container.streams(func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("RIFFsamples"))
	})

	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav", nil)
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("a finished body ended with %v", err)
	}
	if string(body) != "RIFFsamples" {
		t.Errorf("the body is %q", body)
	}
	if line := harness.lines.last(); !strings.Contains(line, "ended=ok") {
		t.Errorf("the API logged %q", line)
	}
}

func TestThePublicLegLogsEveryRequestWhateverBecameOfIt(t *testing.T) {
	// The line is written from a defer, so the two endings and the
	// answers that never stream all reach the log.
	harness := newAPIHarness(t)
	answer := harness.call(t, http.MethodGet, "/v1/audio", nil)
	_, _ = io.Copy(io.Discard, answer.Body)
	if line := harness.lines.last(); !strings.Contains(line, "ended=ok") ||
		!strings.Contains(line, "route=/v1/audio") {
		t.Errorf("the discovery document logged %q", line)
	}
}

// The headers reach the caller when the container's do, not when the
// first block of audio does. A tap with a begin sends no audio for as
// long as the discard runs, so a caller waiting on the headers would
// otherwise have nothing to show for a minute.
func TestTheRelayedHeadersReachTheCallerBeforeTheFirstBodyByte(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", drillMachine, drillPipeWireNode)
	harness.server.pods.replace([]pod{samplePod(drillMachine)})

	const silent = 600 * time.Millisecond
	harness.container.streams(func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "audio/flac")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_ = http.NewResponseController(w).Flush()
		time.Sleep(silent)
		_, _ = w.Write([]byte("fLaC"))
		_ = http.NewResponseController(w).Flush()
	})

	// Do returns when the headers arrive, so this is the wait a client
	// sees before it knows what it is getting.
	request, err := http.NewRequest(http.MethodGet,
		harness.serving.URL+"/v1/audio/sinks/kitchen/audio.flac?t=0.6,0.7", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer a.b.c")
	started := time.Now()
	answer, err := harness.serving.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = answer.Body.Close() }()
	waited := time.Since(started)

	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the tap answered %s", answer.Status)
	}
	if waited >= silent {
		t.Errorf("the headers took %s, which is the whole of the container's silence", waited)
	}
	if waited > silent/3 {
		t.Errorf("the headers took %s, and the container sent them at once", waited)
	}
	// Everything the caller needs to act on is already there.
	if got := answer.Header.Get("Content-Type"); got != "audio/flac" {
		t.Errorf("the type is %q", got)
	}
	if answer.Header.Get("Content-Disposition") == "" {
		t.Error("the save name is not in the headers")
	}
	if got := answer.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("the cache directive is %q", got)
	}

	// And the body still arrives.
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	if string(body) != "fLaC" {
		t.Errorf("the body is %q", body)
	}
	if total := time.Since(started); total < silent {
		t.Errorf("the whole tap took %s, so the container's silence never happened", total)
	}
}
