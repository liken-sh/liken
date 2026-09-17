package main

// The capture container: the fifth container of the operator's pod,
// which is this binary in its capture mode.
//
// Every fact about PipeWire and every encoder is here: node
// resolution, the span, the bitrate knob, and the 400, 404, 500, and
// 503 answers that carry pw-record's own words. The API relays this
// container's status, headers, and body unchanged and adds Vary,
// Content-Location, Link, and Content-Disposition.
//
// The listener is 9201, named capture in the DaemonSet, and it serves
// the tap routes, /healthz, /readyz, and /metrics on that one TLS
// port. The last three need no token. 9200 is the operator's own
// metrics port, and liken gives each container of a pod one port.
//
// The container's route mirrors the public one with the extension
// always present, so nothing here negotiates: the API chose the form
// before it forwarded.

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
)

// The settings this container reads from its environment, which is
// how every setting in this program arrives.
const (
	captureAddressVariable = "CAPTURE_ADDRESS"
	captureTapsVariable    = "CAPTURE_TAPS"
)

// defaultCaptureTaps caps this container's own load. Taps on
// different endpoints, and several taps on one endpoint, run at once,
// because PipeWire links each stream to the monitor ports itself.
// The cap bounds the processes on the node, not the fan-out, and the
// tap past it is a 503.
const defaultCaptureTaps = 4

// retryAfterSeconds is what a 503 tells a client to do. The three
// capture APIs send the same number.
const retryAfterSeconds = "5"

// captureServer is the whole of this mode's state.
type captureServer struct {
	version  string
	leaf     *leaf
	review   *reviewer
	readings *captureMetrics
	taps     chan struct{}

	// graph reads the PipeWire graph. It is a field so a test drives
	// the resolution and the confirmation from fixtures rather than
	// from a running daemon.
	graph func(ctx context.Context) ([]byte, error)

	// start runs one tap's processes. A field for the same reason.
	start func(ctx context.Context, plan tapPlan) (*runningTap, error)

	// now is the accept instant, which is the span's zero.
	now func() time.Time

	// linkDeadline bounds the confirmation. It is a field so a test
	// drives the no-link case without waiting the real three seconds.
	linkDeadline time.Duration

	// log is where the per-tap line goes. It is a field so a test
	// reads the line rather than the process's own output.
	log func(string)
}

// capture is the mode's entry point.
func capture() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := InClusterClient()
	if err != nil {
		fatal("in-cluster config: %v", err)
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

func newCaptureServer(readings *captureMetrics, held *leaf, review *reviewer, taps int) *captureServer {
	return &captureServer{
		version:  version,
		leaf:     held,
		review:   review,
		readings: readings,
		taps:     make(chan struct{}, taps),
		graph:    dumpGraph,
		start:    startTap,
		now:      time.Now,
		log:      func(line string) { fmt.Println(line) },
	}
}

// captureTapLimit reads the cap from the environment.
func captureTapLimit() int {
	if value := os.Getenv(captureTapsVariable); value != "" {
		if limit, err := strconv.Atoi(value); err == nil && limit > 0 {
			return limit
		}
	}
	return defaultCaptureTaps
}

// serve answers on the listener until the run ends.
//
// The certificate comes from the leaf on every handshake, so a Secret
// the API minted after this container started is served with no
// restart. Before the file arrives the leaf answers with a
// certificate this container signed itself, which the API refuses
// and reports as a 503 with the reason.
func (s *captureServer) serve(ctx context.Context, listener net.Listener) error {
	serving := &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: metricsDeadline,
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

// handler is the whole of what this container answers. The three
// plain routes come first so a scrape and a probe never reach the
// token check.
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
	served.HandleFunc("/", s.answer)
	return served
}

func writePlain(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintln(w, text)
}

// answer handles one request on the private leg. The order is the
// API's own: route, method, caller, query, then the graph, so a
// refusal that costs nothing comes before a read that costs a
// process.
func (s *captureServer) answer(w http.ResponseWriter, r *http.Request) {
	at := s.now()
	id := requestID()
	route, name, found := matchRoute(r.URL.Path)
	if !found || !s.serves(route) {
		// A path this container does not serve is not a node that is
		// absent, so it carries no type of its own.
		s.refuse(w, r, http.StatusNotFound, problemBlank, id,
			fmt.Sprintf("this container serves no route at %s", r.URL.Path))
		return
	}
	if !answersMethod(r.Method) {
		w.Header().Set("Allow", allowHeader())
		s.refuse(w, r, http.StatusMethodNotAllowed, problemBlank, id,
			fmt.Sprintf("%s is not one of the methods this route allows", r.Method))
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
			"the capture container accepts the audio-api ServiceAccount alone")
		return
	}

	knobs, err := parseKnobs(route, r.URL.RawQuery)
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

	// The info route answers with the graph read and nothing else. The
	// API joins it to the object the API server holds.
	if route.Kind == routeInfo {
		writeJSON(w, r.Method, http.StatusOK, captureFormatDocument{
			Node:     name,
			Rate:     format.Rate,
			Channels: format.Channels,
		})
		return
	}

	// A HEAD answers with the GET's headers, takes no sample, and
	// starts no process (RFC 9110 section 9.3.2).
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", form.ContentType)
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
			fmt.Sprintf("this node is running its %d taps", cap(s.taps)))
		return
	}

	s.stream(w, r, tapPlan{
		Route:     route,
		Node:      name,
		Direction: direction,
		Form:      form,
		Format:    format,
		Knobs:     knobs,
		RequestID: id,
		Stream:    streamName(id),
	}, at)
}

// serves says whether this container answers a route at all. It
// answers the tap routes with their extension, because the API always
// forwards a fixed form, and the info route, because the rate and the
// channel count are facts of the graph and the graph is here.
func (s *captureServer) serves(route apiRoute) bool {
	if route.Kind == routeInfo {
		return true
	}
	return route.Kind == routeTap && route.Extension != ""
}

// captureFormatDocument is what the info route answers on the private
// leg: what a tap on this endpoint would run at.
type captureFormatDocument struct {
	Node     string `json:"node"`
	Rate     int    `json:"rate"`
	Channels int    `json:"channels"`
}

// authenticate reviews the API's own projected token. The username
// match is the whole of the rule: this container answers the
// audio-api ServiceAccount and nothing else, and it never authorizes,
// because the API did that before it forwarded.
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

// refuse writes one problem document.
func (s *captureServer) refuse(w http.ResponseWriter, r *http.Request,
	status int, kind, id, detail string) {
	writeProblem(w, r.Method, newProblem(kind, status, detail, instanceOf(r.URL.Path, id)))
}

// answersMethod says whether this API answers a method at all.
func answersMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// allowHeader is the Allow field a 405 and an OPTIONS both carry.
func allowHeader() string {
	return "GET, HEAD, OPTIONS"
}
