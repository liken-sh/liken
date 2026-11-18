package main

// The capture container runs this binary in capture mode, in the
// operator's pod. For each tap, it finds the PipeWire node, records the
// requested span with pw-record, encodes it, and streams it to the
// audio API. The API relays the status, headers, and body unchanged,
// and adds only the Vary, Content-Location, Link, and
// Content-Disposition headers. So each 400, 404, 500, and 503 is
// written here, and its detail carries the text of the error behind
// it: the query parser's, pw-dump's, or pw-record's.
//
// The container listens on port 9201, which the DaemonSet names
// capture, because the operator's own metrics use port 9200 and two
// containers of a pod cannot share a port. The tap routes, /healthz,
// /readyz, and /metrics share that TLS port, and only the tap routes
// need a token.
//
// A tap route here always ends in a format extension, because the API
// negotiates the format before it forwards a request.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

const (
	captureAddressVariable = "CAPTURE_ADDRESS"
	captureTapsVariable    = "CAPTURE_TAPS"
)

// defaultCaptureTaps is the limit on taps that run at once when
// CAPTURE_TAPS is unset. Each tap runs a pw-record process and an
// encoder on the node, and a request over the limit gets a 503. Several
// taps can record one endpoint, because PipeWire links each stream to
// the monitor ports on its own.
const defaultCaptureTaps = 4

// retryAfterSeconds is the Retry-After value of every 503, in this
// container and in the audio API.
const retryAfterSeconds = "5"

type captureServer struct {
	version  string
	leaf     *leaf
	review   *reviewer
	readings *captureMetrics
	taps     chan struct{}

	// A test replaces graph and start, so that it needs no PipeWire
	// daemon, and log, to read the line that each tap writes.
	graph func(ctx context.Context) ([]byte, error)
	start func(ctx context.Context, plan tapPlan) (*runningTap, error)
	log   func(string)

	// now gives the time that a request arrives. The begin and end
	// offsets of the span count from that time.
	now func() time.Time
}

func capture() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		fatal("reading the in-cluster config: %v", err)
	}

	directory := os.Getenv(captureTLSDirVariable)
	if directory == "" {
		directory = captureTLSDir
	}
	server := newCaptureServer(newCaptureMetrics(version), newLeaf(directory),
		newReviewer(client, captureAudience), captureTapLimit())

	go server.leaf.watch(ctx.Done(), server.readings, func(err error) {
		fmt.Fprintf(os.Stderr, "%s\n", err)
	})

	address := os.Getenv(captureAddressVariable)
	if address == "" {
		address = ":9201"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fatal("listening for captures on %s: %v", address, err)
	}
	fmt.Printf("%s: serving captures on %s\n", DriverName, listener.Addr())
	if err := server.serve(ctx, listener); err != nil {
		fatal("the capture listener stopped: %v", err)
	}
}

func newCaptureServer(readings *captureMetrics, tlsLeaf *leaf, review *reviewer, taps int) *captureServer {
	return &captureServer{
		version:  version,
		leaf:     tlsLeaf,
		review:   review,
		readings: readings,
		taps:     make(chan struct{}, taps),
		graph:    dumpGraph,
		start:    startTap,
		now:      time.Now,
		log:      func(line string) { fmt.Println(line) },
	}
}

// captureTapLimit returns the tap limit from the environment. A value
// that is not a positive integer gives the default, so a typo does not
// leave the node unable to run any tap.
func captureTapLimit() int {
	if value := os.Getenv(captureTapsVariable); value != "" {
		if limit, err := strconv.Atoi(value); err == nil && limit > 0 {
			return limit
		}
	}
	return defaultCaptureTaps
}

// The leaf supplies the certificate on every TLS handshake, so a
// certificate that the API mints after this container starts is served
// without a restart. Until the certificate files exist, the leaf
// serves a self-signed certificate. The API does not trust it, and
// returns a 503 that gives the reason to its caller.
func (s *captureServer) serve(ctx context.Context, listener net.Listener) error {
	serving := &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		TLSConfig:         s.leaf.tlsConfig(),
	}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.ServeTLS(listener, "", ""); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// ServeMux sends a request to its most specific pattern, so /metrics,
// /healthz, and /readyz go to their own handlers, and a scrape or a
// probe never reaches the token check in handleRequest.
func (s *captureServer) handler() http.Handler {
	served := http.NewServeMux()
	served.Handle("/metrics", captureRegistryHandler(s.readings))
	served.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writePlain(w, http.StatusOK, "ok")
	})
	served.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !s.leaf.loaded() {
			writePlain(w, http.StatusServiceUnavailable, ErrNoCertificate.Error())
			return
		}
		writePlain(w, http.StatusOK, "ok")
	})
	served.HandleFunc("/", s.handleRequest)
	return served
}

func writePlain(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintln(w, text)
}

// handleRequest checks the route, the method, the token, and the query
// in the order that the API checks them. It reads the PipeWire graph
// only after those checks, because the read runs pw-dump.
func (s *captureServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	at := s.now()
	id := requestID()
	route, name, found := matchRoute(r.URL.Path)
	if !found || !s.serves(route) {
		// The no-node problem type means a missing endpoint, so a path
		// that is no route gets the blank type.
		s.refuse(w, r, http.StatusNotFound, problemBlank, id,
			fmt.Sprintf("this container serves no route at %s", r.URL.Path))
		return
	}
	if !allowsMethod(r.Method) {
		w.Header().Set("Allow", allowHeader())
		s.refuse(w, r, http.StatusMethodNotAllowed, problemBlank, id,
			fmt.Sprintf("%s is not one of the methods that this route allows: %s", r.Method, allowHeader()))
		return
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", allowHeader())
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if reason, ok := s.authenticate(r); !ok {
		w.Header().Set("WWW-Authenticate", reason)
		s.refuse(w, r, http.StatusUnauthorized, problemBlank, id,
			"the capture container accepts only the audio-api ServiceAccount")
		return
	}

	query, err := parseCaptureQuery(route, r.URL.RawQuery)
	if err != nil {
		s.refuse(w, r, http.StatusBadRequest, problemBlank, id, err.Error())
		return
	}
	form, _ := representationFor(route.Extension)
	direction := directionSink
	if route.Resource == "sources" {
		direction = directionSource
	}

	document, err := s.graph(r.Context())
	if err != nil {
		s.readings.failed(failureConnect)
		w.Header().Set("Retry-After", retryAfterSeconds)
		s.refuse(w, r, http.StatusServiceUnavailable, problemBlank, id, err.Error())
		return
	}
	format, err := resolveNode(document, name, direction)
	if err != nil {
		s.readings.failed(failureTarget)
		s.refuse(w, r, http.StatusNotFound, problemNoNode, id, err.Error())
		return
	}

	// The info route returns only the rate and channel count from the
	// graph. The API merges them with the Sink or Source.
	if route.Kind == routeInfo {
		writeJSON(w, r.Method, http.StatusOK, captureFormatDocument{
			Node:     name,
			Rate:     format.Rate,
			Channels: format.Channels,
		})
		return
	}

	// A HEAD response has the headers of the GET response and no body
	// (RFC 9110 section 9.3.2), so no pw-record or encoder starts.
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", form.ContentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Accept-Ranges", "none")
		w.WriteHeader(http.StatusOK)
		return
	}

	select {
	case s.taps <- struct{}{}:
		defer func() { <-s.taps }()
	default:
		s.readings.failed(failureLimit)
		w.Header().Set("Retry-After", retryAfterSeconds)
		s.refuse(w, r, http.StatusServiceUnavailable, problemCaptureBusy, id,
			fmt.Sprintf("this node already runs its limit of %d taps", cap(s.taps)))
		return
	}

	s.stream(w, r, tapPlan{
		Route:     route,
		Node:      name,
		Direction: direction,
		Form:      form,
		Format:    format,
		Query:     query,
		RequestID: id,
		Stream:    streamName(id),
	}, at)
}

// serves reports whether this container handles the route. A tap route
// must have a format extension, because the API always forwards one.
// The info route is served here because only this container reads the
// PipeWire graph, which gives the rate and the channel count.
func (s *captureServer) serves(route apiRoute) bool {
	if route.Kind == routeInfo {
		return true
	}
	return route.Kind == routeTap && route.Extension != ""
}

// captureFormatDocument is the body of the info route. A tap on the
// endpoint records at this rate and channel count.
type captureFormatDocument struct {
	Node     string `json:"node"`
	Rate     int    `json:"rate"`
	Channels int    `json:"channels"`
}

// authenticate sends the bearer token to a TokenReview. It returns true
// when the token belongs to the audio-api ServiceAccount, and otherwise
// false with the WWW-Authenticate challenge to send. It does no
// authorization, because the API authorized the caller before it
// forwarded the request.
func (s *captureServer) authenticate(r *http.Request) (string, bool) {
	token, found := bearerToken(r.Header.Get("Authorization"))
	if !found {
		return `Bearer realm="` + captureAudience + `"`, false
	}
	who, err := s.review.review(token)
	if err != nil {
		return `Bearer realm="` + captureAudience + `", error="invalid_token", ` +
			`error_description="` + quoted(err.Error()) + `"`, false
	}
	if who.Username != captureCaller {
		return `Bearer realm="` + captureAudience + `", error="invalid_token", ` +
			`error_description="` + quoted(who.Username+" is not "+captureCaller) + `"`, false
	}
	return "", true
}

// refuse writes an RFC 9457 problem document with the status.
func (s *captureServer) refuse(w http.ResponseWriter, r *http.Request,
	status int, kind, id, detail string) {
	writeProblem(w, r.Method, newProblem(kind, status, detail, instanceOf(r.URL.Path, id)))
}

// allowsMethod reports whether the routes of this container and of the
// audio API accept the method.
func allowsMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func allowHeader() string {
	return "GET, HEAD, OPTIONS"
}
