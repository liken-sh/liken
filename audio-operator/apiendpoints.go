package main

// The routes for one Sink or Source: the info document and the tap.
//
// serveEndpoint reads the Sink or Source from the API server, then the
// capture pod of its node from the pod informer's cache, then the format
// or the audio from the container in that pod. A missing object is a
// 404. An empty status.node is a 409 whose detail says to power the
// device on, because the API returns a 409 only when the caller can fix
// the cause. A node with no Ready capture container is a 503, which the
// cache settles without a request to the API server.
//
// The API passes on the container's status and body. It sets Vary and
// Link on every response. On a 200 it also sets the content type, the
// cache headers, Content-Disposition, and Content-Location, because they
// depend on the representation that the API negotiated with the caller.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

type capturedEndpoint struct {
	Kind   string
	Name   string
	UID    string
	Status EndpointStatus
}

// readEndpoint reads the Sink or Source that route.Resource names into
// one type, because the routes use only fields that both kinds have.
func (s *apiServer) readEndpoint(route apiRoute, name string) (capturedEndpoint, error) {
	if route.Resource == "sources" {
		source, err := getSource(s.client, name)
		if err != nil {
			return capturedEndpoint{}, err
		}
		return capturedEndpoint{
			Kind:   SourceKind,
			Name:   source.Metadata.Name,
			UID:    source.Metadata.UID,
			Status: source.Status,
		}, nil
	}
	sink, err := getSink(s.client, name)
	if err != nil {
		return capturedEndpoint{}, err
	}
	return capturedEndpoint{
		Kind:   SinkKind,
		Name:   sink.Metadata.Name,
		UID:    sink.Metadata.UID,
		Status: sink.Status.EndpointStatus,
	}, nil
}

func (s *apiServer) serveEndpoint(w http.ResponseWriter, r *http.Request, route apiRoute,
	name string, form representation, query captureQuery, who caller,
	id string, at time.Time, outcome *requestOutcome) requestOutcome {
	endpoint, err := s.readEndpoint(route, name)
	if errors.Is(err, apiclient.ErrNotFound) {
		return s.refuseAs(w, r, route, name, who, http.StatusNotFound, problemNoNode, id,
			fmt.Sprintf("this cluster has no %s named %s", singular(route.Resource), name))
	}
	if err != nil {
		w.Header().Set("Retry-After", retryAfterSeconds)
		return s.refuseAs(w, r, route, name, who, http.StatusServiceUnavailable,
			problemBlank, id, err.Error())
	}
	// status.node is the machine, and status.nodeName is the PipeWire
	// node that a stream targets. With no machine the endpoint is away,
	// even when status.nodeName is still set.
	if endpoint.Status.Node == "" {
		return s.refuseAs(w, r, route, name, who, http.StatusConflict, problemAway, id,
			fmt.Sprintf("%s %s is away: power the device on, or connect it, and the operator "+
				"publishes its node again", endpoint.Kind, endpoint.Name))
	}

	pod, running := s.pods.on(endpoint.Status.Node)
	if !running || !pod.Ready {
		w.Header().Set("Retry-After", retryAfterSeconds)
		return s.refuseAs(w, r, route, name, who, http.StatusServiceUnavailable, problemBlank, id,
			fmt.Sprintf("no Ready capture container is running on the node %s", endpoint.Status.Node))
	}

	if route.Kind == routeInfo {
		return s.serveInfo(w, r, route, endpoint, form, pod, who, id)
	}
	return s.serveTap(w, r, route, endpoint, form, query, pod, who, id, at, outcome)
}

// refuseAs is refuse with the caller added to the result, so the log
// line of a refused request names the caller.
func (s *apiServer) refuseAs(w http.ResponseWriter, r *http.Request, route apiRoute,
	name string, who caller, status int, kind, id, detail string) requestOutcome {
	result := s.refuse(w, r, route, name, status, kind, id, detail)
	result.Who = who
	return result
}

// serveInfo writes the endpoint's info document: the Sink or Source
// from the API server, with the sample rate and channel count that the
// container reads from the PipeWire graph.
func (s *apiServer) serveInfo(w http.ResponseWriter, r *http.Request, route apiRoute,
	endpoint capturedEndpoint, form representation, pod capturePod, who caller, id string) requestOutcome {
	// A HEAD makes no call to the container, because only the body
	// depends on the PipeWire graph.
	if r.Method == http.MethodHead {
		writeDocumentHeaders(w, route, endpoint.Name, form, s.buildVersion())
		w.WriteHeader(http.StatusOK)
		return requestOutcome{Status: http.StatusOK, Who: who}
	}
	format, err := s.readCaptureFormat(r, route, endpoint, pod)
	if err != nil {
		return s.relayFailure(w, r, route, endpoint.Name, who, id, err)
	}
	writeDocumentHeaders(w, route, endpoint.Name, form, s.buildVersion())
	if matchesETag(r.Header.Get("If-None-Match"), documentETag(s.buildVersion())) {
		w.WriteHeader(http.StatusNotModified)
		return requestOutcome{Status: http.StatusNotModified, Who: who}
	}
	writeJSON(w, r.Method, http.StatusOK,
		newEndpointDocument(endpoint.Kind, endpoint.Name, endpoint.Status, format))
	return requestOutcome{Status: http.StatusOK, Who: who}
}

// readCaptureFormat reads from the container the sample rate and
// channel count that a tap of this endpoint would use. Only the
// container can read them, from the PipeWire graph on its node.
func (s *apiServer) readCaptureFormat(r *http.Request, route apiRoute,
	endpoint capturedEndpoint, pod capturePod) (captureFormat, error) {
	response, err := s.relay.forward(r.Context(), pod,
		apiPrefix+"/"+route.Resource+"/"+endpoint.Status.NodeName, "", 0)
	if err != nil {
		return captureFormat{}, err
	}
	defer drain(response.Body)
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxDrain))
		if document, isProblem := readProblem(body); isProblem {
			return captureFormat{}, errors.New(document.Detail)
		}
		return captureFormat{}, fmt.Errorf("the capture container returned %s", response.Status)
	}
	var document captureFormatDocument
	if err := readJSON(response.Body, &document); err != nil {
		return captureFormat{}, err
	}
	return captureFormat{Rate: document.Rate, Channels: document.Channels}, nil
}

func (s *apiServer) serveTap(w http.ResponseWriter, r *http.Request, route apiRoute,
	endpoint capturedEndpoint, form representation, query captureQuery, pod capturePod,
	who caller, id string, at time.Time, outcome *requestOutcome) requestOutcome {
	// A HEAD returns the GET's headers, with no sample and no call to
	// the container (RFC 9110 section 9.3.2).
	if r.Method == http.MethodHead {
		writeTapHeaders(w, route, endpoint.Name, form, at)
		w.Header().Set("Content-Type", form.ContentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		return requestOutcome{Status: http.StatusOK, Who: who, Ended: "ok"}
	}

	// The container's path always has the extension, and it names the
	// PipeWire node in status.nodeName, not the machine in status.node.
	private := apiPrefix + "/" + route.Resource + "/" + endpoint.Status.NodeName +
		"/" + route.Aspect + "." + form.Extension
	// The idleReader calls stop to end a silent body.
	streaming, stop := context.WithCancel(r.Context())
	defer stop()
	response, err := s.relay.forward(streaming, pod, private, r.URL.RawQuery, query.Span.Begin)
	if err != nil {
		return s.relayFailure(w, r, route, endpoint.Name, who, id, err)
	}
	defer drain(response.Body)

	if response.StatusCode != http.StatusOK {
		return s.relayProblem(w, r, route, endpoint.Name, response, who, id)
	}

	// On a 200, writeTapHeaders and copyFlushing set the content type
	// and the cache headers, so none of the container's headers are
	// copied.
	writeTapHeaders(w, route, endpoint.Name, form, at)

	s.readings.streaming(route.Aspect, 1)
	defer s.readings.streaming(route.Aspect, -1)

	started := s.now()
	body := newIdleReader(response.Body, idleTimeout, started.Add(query.Span.Begin))
	body.watch(stop)
	defer body.stopWatching()
	// The callback posts the captured Event with the first block, not
	// when the tap ends. An open-ended tap can run for hours, and
	// kubectl describe has to show the listener while it runs.
	sent, copyErr := copyFlushing(w, form.ContentType, body, func() {
		s.recordCapture(endpoint.Kind, endpoint.Name, endpoint.UID, route.Aspect, form.Extension, who.Username)
	})
	streamed := s.now().Sub(started)

	// When a capture is cut short, the container aborts its response
	// without the final chunk, so the read fails with an error other
	// than EOF. The API then aborts its own response, so the caller sees
	// the truncated body as an error and not as a complete one.
	cut := copyErr != nil && !errors.Is(copyErr, errClientGone)
	result := requestOutcome{
		Status: http.StatusOK, Sent: sent, Streamed: streamed, Who: who, Ended: "ok",
	}
	if copyErr != nil {
		result.Ended = copyErr.Error()
	}
	if cut {
		result.Ended = "the capture container ended the body part way: " + copyErr.Error()
		*outcome = result
		panic(http.ErrAbortHandler)
	}
	return result
}

// relayFailure writes the problem for an error of forward, which
// returned no response.
//
// A container that sent no headers in time is a 504, and a response
// that could not be read on an established connection is a 502. Any
// other error is a 503 with Retry-After: a refused connection, a
// certificate that the API's CA did not sign, a CA that the API has not
// minted yet, an unreadable token file, or a CA whose PEM holds no
// certificate.
func (s *apiServer) relayFailure(w http.ResponseWriter, r *http.Request,
	route apiRoute, name string, who caller, id string, err error) requestOutcome {
	switch {
	case errors.Is(err, ErrCaptureTimeout):
		return s.refuseAs(w, r, route, name, who, http.StatusGatewayTimeout,
			problemUpstreamFailed, id, err.Error())
	case errors.Is(err, ErrCaptureMalformed):
		return s.refuseAs(w, r, route, name, who, http.StatusBadGateway,
			problemUpstreamFailed, id, err.Error())
	default:
		w.Header().Set("Retry-After", retryAfterSeconds)
		return s.refuseAs(w, r, route, name, who, http.StatusServiceUnavailable,
			problemBlank, id, err.Error())
	}
}

// relayProblem returns the container's problem document to the caller.
// A response that is not a problem document is a 502, because the API
// cannot tell what failed on the node.
func (s *apiServer) relayProblem(w http.ResponseWriter, r *http.Request, route apiRoute,
	name string, response *http.Response, who caller, id string) requestOutcome {
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxDrain))
	document, isProblem := readProblem(body)
	if !isProblem {
		return s.refuseAs(w, r, route, name, who, http.StatusBadGateway, problemUpstreamFailed, id,
			fmt.Sprintf("the capture container returned %s with %d bytes that are not a problem document",
				response.Status, len(body)))
	}
	relayHeaders(response.Header, w.Header())
	writeVaryAndLink(w, route, name)
	// The instance is the caller's path and this request's id, not the
	// container's private path.
	document.Instance = instanceOf(r.URL.Path, id)
	writeProblem(w, r.Method, document)
	return requestOutcome{Status: document.Status, Who: who}
}

func readJSON(body io.Reader, into any) error {
	return json.NewDecoder(io.LimitReader(body, maxDrain)).Decode(into)
}

// maxDrain limits each read of a container response that the API
// buffers or discards. The API requests only small documents this way,
// so a larger body is unexpected.
const maxDrain = 4 << 20

// drain discards the rest of a response body, up to maxDrain, and
// closes it. Go returns a connection to its pool only when the body
// reaches EOF, so without the drain the next request to the same
// container opens a new connection and makes a new TLS handshake.
func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	_ = body.Close()
}
