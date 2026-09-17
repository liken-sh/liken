package main

// The two routes that name an endpoint: the info document and the
// tap.
//
// Three reads happen in order. The object comes from the API server:
// none is a 404, and no status.node is a 409 away, with a detail that
// says to power the device on, because a 409 is used only where the
// caller can act. The node's pod comes from the informer's memory: no
// Ready capture container is an instant 503. The sound comes from
// that pod.
//
// The API adds Vary, Content-Location, Link, and Content-Disposition
// to the container's answer. The status and the body are the
// container's own. On a 200 the API also writes the type and the
// cache directives, because they follow from the form it chose.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// capturedEndpoint is one Sink or Source as this API reads it: the
// kind, the object's own identity, and the status the tap needs.
type capturedEndpoint struct {
	Kind   string
	Name   string
	UID    string
	Status EndpointStatus
}

// readEndpoint reads one object. The two kinds share a status shape, so
// one function reads either.
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
		Status: sink.Status,
	}, nil
}

// serveEndpoint answers the info route and the tap routes.
func (s *apiServer) serveEndpoint(w http.ResponseWriter, r *http.Request, route apiRoute,
	name string, form representation, knobs captureKnobs, who caller,
	id string, at time.Time) answered {
	held, err := s.readEndpoint(route, name)
	if errors.Is(err, ErrNotFound) {
		return s.refuseAs(w, r, route, name, who, http.StatusNotFound, problemNoNode, id,
			fmt.Sprintf("this cluster holds no %s named %s", singular(route.Resource), name))
	}
	if err != nil {
		w.Header().Set("Retry-After", retryAfterSeconds)
		return s.refuseAs(w, r, route, name, who, http.StatusServiceUnavailable,
			problemBlank, id, err.Error())
	}
	// status.node is the machine the endpoint is on and status.nodeName
	// is the PipeWire node a stream targets. An endpoint with no
	// machine is away, whatever PipeWire last held for it.
	if held.Status.Node == "" {
		return s.refuseAs(w, r, route, name, who, http.StatusConflict, problemAway, id,
			fmt.Sprintf("%s %s is away; power the device on, or connect it, and the operator "+
				"publishes its node again", held.Kind, held.Name))
	}

	pod, running := s.pods.on(held.Status.Node)
	if !running || !pod.Ready {
		w.Header().Set("Retry-After", retryAfterSeconds)
		return s.refuseAs(w, r, route, name, who, http.StatusServiceUnavailable, problemBlank, id,
			fmt.Sprintf("no Ready capture container is running on the node %s", held.Status.Node))
	}

	if route.Kind == routeInfo {
		return s.serveInfo(w, r, route, held, form, pod, who, id)
	}
	return s.serveTap(w, r, route, held, form, knobs, pod, who, id, at)
}

// refuseAs writes a problem and names the caller, so the one log line
// still says who asked.
func (s *apiServer) refuseAs(w http.ResponseWriter, r *http.Request, route apiRoute,
	name string, who caller, status int, kind, id, detail string) answered {
	result := s.refuse(w, r, route, name, status, kind, id, detail)
	result.Who = who
	return result
}

// serveInfo answers one endpoint's own document: the object the API
// server holds, joined to the format the container read from the graph.
func (s *apiServer) serveInfo(w http.ResponseWriter, r *http.Request, route apiRoute,
	held capturedEndpoint, form representation, pod capturePod, who caller, id string) answered {
	// A HEAD answers with the GET's headers and makes no call to the
	// capture container, and the graph read is only in the body.
	if r.Method == http.MethodHead {
		writeDocumentHeaders(w, route, held.Name, form, s.buildVersion())
		w.WriteHeader(http.StatusOK)
		return answered{Status: http.StatusOK, Who: who}
	}
	format, err := s.readCaptureFormat(r, route, held, pod)
	if err != nil {
		return s.relayFailure(w, r, route, held.Name, who, id, err)
	}
	writeDocumentHeaders(w, route, held.Name, form, s.buildVersion())
	if matchesETag(r.Header.Get("If-None-Match"), documentETag(s.buildVersion())) {
		w.WriteHeader(http.StatusNotModified)
		return answered{Status: http.StatusNotModified, Who: who}
	}
	writeJSON(w, r.Method, http.StatusOK,
		newEndpointDocument(held.Kind, held.Name, held.Status, format))
	return answered{Status: http.StatusOK, Who: who}
}

// readCaptureFormat asks the node's container what a tap would run at.
// The graph is on the node, so this is the one read that is not the
// API's own.
func (s *apiServer) readCaptureFormat(r *http.Request, route apiRoute,
	held capturedEndpoint, pod capturePod) (captureFormat, error) {
	answer, err := s.relay.forward(r.Context(), pod,
		apiPrefix+"/"+route.Resource+"/"+held.Status.NodeName, "", 0)
	if err != nil {
		return captureFormat{}, err
	}
	defer drain(answer.Body)
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(answer.Body, maxDrain))
		if document, held := readProblem(body); held {
			return captureFormat{}, errors.New(document.Detail)
		}
		return captureFormat{}, fmt.Errorf("the capture container answered %s", answer.Status)
	}
	var document captureFormatDocument
	if err := readJSON(answer.Body, &document); err != nil {
		return captureFormat{}, err
	}
	return captureFormat{Rate: document.Rate, Channels: document.Channels}, nil
}

// serveTap forwards one capture and relays what comes back.
func (s *apiServer) serveTap(w http.ResponseWriter, r *http.Request, route apiRoute,
	held capturedEndpoint, form representation, knobs captureKnobs, pod capturePod,
	who caller, id string, at time.Time) answered {
	// A HEAD answers with the GET's headers, takes no sample, and makes
	// no call to the capture container (RFC 9110 section 9.3.2).
	if r.Method == http.MethodHead {
		writeTapHeaders(w, route, held.Name, form, at)
		w.WriteHeader(http.StatusOK)
		return answered{Status: http.StatusOK, Who: who}
	}

	// The container's route mirrors this one with the extension always
	// present, and what it resolves is a PipeWire node, so the path
	// carries status.nodeName rather than the machine in status.node.
	private := apiPrefix + "/" + route.Resource + "/" + held.Status.NodeName +
		"/" + route.Aspect + "." + form.Extension
	// The stream runs under a context of its own, so the idle bound
	// below can end it: a read on a connection that went silent blocks
	// inside the transport, and only the request's own cancel returns
	// from it.
	streaming, stop := context.WithCancel(r.Context())
	defer stop()
	answer, err := s.relay.forward(streaming, pod, private, r.URL.RawQuery, knobs.Span.Begin)
	if err != nil {
		return s.relayFailure(w, r, route, held.Name, who, id, err)
	}
	defer drain(answer.Body)

	if answer.StatusCode != http.StatusOK {
		return s.relayProblem(w, r, route, held.Name, answer, who, id)
	}

	// writeTapHeaders owns the type and the cache directives on a 200,
	// so nothing of the container's is relayed onto it.
	writeTapHeaders(w, route, held.Name, form, at)
	w.WriteHeader(http.StatusOK)
	flush(w)

	s.readings.streaming(route.Aspect, 1)
	defer s.readings.streaming(route.Aspect, -1)

	started := s.now()
	body := newIdleReader(answer.Body, idleTimeout, started.Add(knobs.Span.Begin))
	body.watch(stop)
	defer body.stopWatching()
	// The event is written on the first block, not at the end: an
	// open-ended tap runs for hours, and kubectl describe has to
	// answer who is listening while they still are.
	sent, copyErr := copyFlushing(w, body, func() {
		if err := s.event(held.Kind, held.Name, held.UID, route.Aspect,
			form.Extension, who.Username, at); err != nil {
			fmt.Fprintf(os.Stderr, "writing the Captured event for %s: %v\n", held.Name, err)
		}
	})
	streamed := s.now().Sub(started)

	if copyErr != nil {
		fmt.Printf("%s: %s stream %s ended: %v\n", DriverName, apiComponent, id, copyErr)
	}
	return answered{Status: http.StatusOK, Sent: sent, Streamed: streamed, Who: who}
}

// relayFailure maps a private leg that did not answer at all. A
// container that sent no headers within the bound is a 504. An answer
// that is not HTTP is a 502. Everything else is a container this API
// could not reach: a refused connection, a certificate it does not
// trust, a token it could not read, or a CA it has not minted yet.
// All of those are a 503 with Retry-After, because each one clears on
// its own.
func (s *apiServer) relayFailure(w http.ResponseWriter, r *http.Request,
	route apiRoute, name string, who caller, id string, err error) answered {
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

// relayProblem relays the container's own problem document unchanged.
// An answer that is not a problem document is a 502, because the API
// cannot say what went wrong on the node.
func (s *apiServer) relayProblem(w http.ResponseWriter, r *http.Request, route apiRoute,
	name string, answer *http.Response, who caller, id string) answered {
	body, _ := io.ReadAll(io.LimitReader(answer.Body, maxDrain))
	document, held := readProblem(body)
	if !held {
		return s.refuseAs(w, r, route, name, who, http.StatusBadGateway, problemUpstreamFailed, id,
			fmt.Sprintf("the capture container answered %s with %d bytes that are not a problem document",
				answer.Status, len(body)))
	}
	relayHeaders(answer.Header, w.Header())
	writeAnswerHeaders(w, route, name)
	// The instance is the public path and this request's own id, so the
	// caller's document names the route the caller called.
	document.Instance = instanceOf(r.URL.Path, id)
	writeProblem(w, r.Method, document)
	return answered{Status: document.Status, Who: who}
}

// readJSON decodes one small document the private leg answered with.
// The bound is the one every other read of a body in this program
// takes: a document this API asked for, and nothing more.
func readJSON(body io.Reader, into any) error {
	return json.NewDecoder(io.LimitReader(body, maxDrain)).Decode(into)
}
