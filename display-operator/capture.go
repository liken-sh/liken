package main

// This file is the capture sidecar: the one program on a node that
// may take a frame from the compositor. It answers the info route
// and the capture routes on the private leg, and it answers
// display-api alone, whose token carries the audience
// display-capture. It holds one capture per output, because the
// compositor's framebuffer source disables hardware planes and
// forces a repaint per frame, and two captures on one output would
// double that cost while each got half the frames.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The role argument the pod's fourth container passes, and the
// component label this process carries on liken_build_info.
const (
	captureMode      = "capture"
	captureComponent = "display-capture"
)

// The one listener this process opens. It serves captures and
// metrics together, because 9200, the port every liken process
// serves metrics on, belongs to the operator container in the same
// pod.
const defaultCaptureAddr = ":9201"

// The socket the layout module opens for this process alone. It is
// in the weston-config emptyDir, not under XDG_RUNTIME_DIR, because
// CDI delivers that directory to consumer pods and the capture socket
// must never be delivered.
const captureSocketPath = "/etc/weston/wayland-capture"

// The knob that says how many captures one output may carry. The
// default is one: every capture holds the output's planes off and
// forces a repaint per frame, and a second one would take frames
// from the first.
const capturePerOutput = "CAPTURE_PER_OUTPUT"

// The knob that picks the conversion graph, and the two values it
// takes. It is a setting rather than a per-request retry, because a
// graph that cannot be built fails the same way on every request,
// and the probe below reads that answer once. An owner who sets it
// overrides the probe in either direction.
const (
	captureConversion = "CAPTURE_CONVERSION"
	softwareGraph     = "software"
	vaapiGraph        = "vaapi"
)

// How long the startup probe may take. It encodes one 64 by 64 frame
// to nothing, which is milliseconds of work, so a probe still
// running after this is a driver that is not answering.
const conversionProbeTimeout = 20 * time.Second

// How long the client waits on the compositor for each step of the
// exchange: the connection, the registry, the output's events, and
// each frame.
const captureOpenTimeout = 5 * time.Second

// The sidecar holds the socket it captures through, the render node
// it encodes on, and the captures running now, keyed by connector.
type captureServer struct {
	client     *Client
	tokens     *tokenCache
	readings   *captureMetrics
	process    http.Handler
	socketPath string
	device     string
	perOutput  int
	now        func() time.Time

	mu       sync.Mutex
	running  map[string][]runningCapture
	software bool
}

// A running capture records when it ends, so the 503 a second caller
// gets says how long to wait.
type runningCapture struct {
	ends time.Time
}

// The capture role. The certificate comes first, so the listener
// always answers the liveness probe, then the render node, then the
// one listener.
func serveCaptureSidecar() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := InClusterClient()
	if err != nil {
		fatal("in-cluster config: %v", err)
	}
	readings := newCaptureMetrics(captureComponent, version)

	holder := &certificateHolder{}
	directory := envOr("CAPTURE_TLS_DIR", captureTLSDir)
	if err := loadCaptureLeaf(directory, holder, time.Now()); err != nil {
		fatal("the capture certificate: %v", err)
	}
	readings.ready(holder.held())
	go watchCaptureLeaf(ctx, directory, holder, readings)

	// A node with no render device still serves the metrics and
	// answers a capture with the encoder's own words, rather than
	// refusing to start and holding the pod in a restart loop.
	device, err := renderNode(driRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the render node: %v\n", err)
	}

	server := &captureServer{
		client:     client,
		tokens:     newTokenCache(func(token string) (*reviewedToken, *fault) { return reviewToken(client, token, captureAudience) }),
		readings:   readings,
		process:    processHandler(readings.registry, holder.held),
		socketPath: envOr("CAPTURE_SOCKET", captureSocketPath),
		device:     device,
		perOutput:  perOutputLimit(),
		now:        time.Now,
		running:    map[string][]runningCapture{},
		software:   chooseConversion(ctx, device),
	}
	readings.converting(server.usesSoftwareConversion())

	address := envOr("CAPTURE_ADDR", defaultCaptureAddr)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fatal("the capture listener on %s: %v", address, err)
	}
	// The realm a challenge names is this process, not the caller it
	// expects, so a 401 from the capture port reads as the capture
	// port's own.
	authenticateRealm = `Bearer realm="display-capture"`
	fmt.Printf("%s: serving captures on %s through %s\n", captureComponent, address, server.socketPath)
	serving := &http.Server{
		Handler:           server,
		ReadHeaderTimeout: headerDeadline,
		TLSConfig:         &tls.Config{GetCertificate: holder.get, MinVersion: tls.VersionTLS12},
	}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.ServeTLS(listener, "", ""); err != nil && ctx.Err() == nil {
		fatal("the capture listener stopped: %v", err)
	}
}

// Which conversion graph this process runs, decided once at startup.
// A node that was told which graph to run is believed, because an
// owner who set the knob knows something the probe cannot read. Every
// other node is asked: the GPU graph runs one synthetic frame, and a
// failure selects the software conversion for the life of the
// process. Either way the choice and the reason go in the log, so a
// node's own line answers which graph it takes.
func chooseConversion(ctx context.Context, device string) bool {
	switch envOr(captureConversion, "") {
	case softwareGraph:
		fmt.Printf("%s: %s names the software conversion, so this node converts on the CPU\n",
			captureComponent, captureConversion)
		return true
	case vaapiGraph:
		fmt.Printf("%s: %s names the GPU graph, so this node converts in scale_vaapi\n",
			captureComponent, captureConversion)
		return false
	}
	if device == "" {
		fmt.Printf("%s: no render node, so this node converts on the CPU\n", captureComponent)
		return true
	}
	probe, stop := context.WithTimeout(ctx, conversionProbeTimeout)
	defer stop()
	if err := probeConversion(probe, device); err != nil {
		fmt.Printf("%s: %s has no VA-API post-processing, so this node converts on the CPU: %v\n",
			captureComponent, device, err)
		return true
	}
	fmt.Printf("%s: %s converts in scale_vaapi\n", captureComponent, device)
	return false
}

func perOutputLimit() int {
	value, err := strconv.Atoi(envOr(capturePerOutput, "1"))
	if err != nil || value < 1 {
		return 1
	}
	return value
}

// /metrics, /healthz, and /readyz take no token, so the kubelet and
// the scrape need none. Every other path takes display-api's own.
func (s *captureServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/metrics", "/healthz", "/readyz":
		s.process.ServeHTTP(w, r)
		return
	}

	id := newRequestID()
	head := r.Method == http.MethodHead
	route, connector, matched := matchRoute(r.URL.Path)
	if !matched || route.kind == documentRoute {
		writeFault(w, notFound(fmt.Sprintf("this sidecar serves no route at %s", r.URL.Path)), r.URL.Path, id, head)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeFault(w, methodNotAllowed(fmt.Sprintf("%s is not a method this sidecar answers", r.Method)), r.URL.Path, id, head)
		return
	}
	if f := s.authenticate(r); f != nil {
		writeFault(w, f, r.URL.Path, id, head)
		return
	}
	if route.kind == infoRoute {
		s.answerInfo(w, r, connector, id, head)
		return
	}
	s.answerCapture(w, r, route, connector, id, head)
}

// The only caller this sidecar answers is display-api. A
// TokenReview for the audience display-capture tells its projected
// token from any other token in the cluster, and the username has to
// be the display-api ServiceAccount's own.
func (s *captureServer) authenticate(r *http.Request) *fault {
	scheme, token, split := strings.Cut(r.Header.Get("Authorization"), " ")
	if !split || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return unauthenticated("")
	}
	who, f := s.tokens.verdict(strings.TrimSpace(token))
	if f != nil {
		return f
	}
	if !servesAccount(who.Username, sidecarNamespace, apiServiceName) {
		return unauthorized(fmt.Sprintf("%s is not the display API", who.Username))
	}
	return nil
}

// The info answer is the compositor's own numbers, read on a
// connection that is opened and closed for this one question, so
// the sidecar holds no Wayland connection while unused.
func (s *captureServer) answerInfo(w http.ResponseWriter, r *http.Request, connector, id string, head bool) {
	session, err := openCapture(s.socketPath, connector, captureOpenTimeout)
	if err != nil {
		writeFault(w, s.compositorFault(connector, err), r.URL.Path, id, head)
		return
	}
	defer func() { _ = session.close() }()

	screen := session.screen()
	body, err := renderJSON(screenInfo{
		Width:      screen.Width,
		Height:     screen.Height,
		Scale:      screen.Scale,
		Refresh:    screen.Refresh,
		Formats:    screenMediaTypes(),
		Conversion: s.conversionName(),
	})
	if err != nil {
		writeFault(w, upstreamFailed(err.Error()), r.URL.Path, id, head)
		return
	}
	w.Header().Set("Content-Type", jsonMediaType)
	if head {
		return
	}
	_, _ = w.Write(body)
}

// A capture the compositor refuses becomes one of two faults. A
// failed event is a 500 capture-denied that carries weston's own
// word, unauthorized, and no retry clears it. Every other failure to
// reach the compositor, a socket that is absent, a connection it
// closed, or a step that timed out, is a 503 a retry may clear.
func (s *captureServer) compositorFault(connector string, err error) *fault {
	var refused *captureFailure
	if errors.As(err, &refused) {
		s.readings.failed(deniedReason)
		return captureDenied(refused.Message)
	}
	s.readings.failed(compositorReason)
	return unavailable(problemNoNode, fmt.Sprintf("the compositor on %s: %v", connector, err))
}

// One capture per output, outputs on one card independent. A second
// request on a busy output gets a 503 whose detail names the running
// capture's end, or says that its client's hang-up ends it.
func (s *captureServer) take(connector string, ends time.Time) (func(), *fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := s.running[connector]
	if len(held) >= s.perOutput {
		s.readings.failed(busyReason)
		return nil, unavailable(problemCaptureBusy, fmt.Sprintf(
			"%s is being captured %s", connector, until(held[0].ends)))
	}
	s.running[connector] = append(held, runningCapture{ends: ends})
	return func() { s.release(connector) }, nil
}

func (s *captureServer) release(connector string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := s.running[connector]
	if len(held) <= 1 {
		delete(s.running, connector)
		return
	}
	s.running[connector] = held[1:]
}

// A capture with a t= end names its end. One with none names what
// ends it: its client's hang-up.
func until(ends time.Time) string {
	if ends.IsZero() {
		return "until its client closes"
	}
	return "until " + ends.UTC().Format(time.RFC3339)
}

// The sidecar parses and refuses the query itself, although
// display-api refused the same query before it forwarded anything.
// The sidecar is a door of its own, and nothing it serves may depend
// on what a caller upstream checked.
func (s *captureServer) answerCapture(w http.ResponseWriter, r *http.Request, route apiRoute, connector, id string, head bool) {
	mediaType := route.mediaType
	if mediaType == "" {
		chosen, ok := chooseType(r.Header.Get("Accept"), screenMediaTypes())
		if !ok {
			writeFault(w, notAcceptable(r.Header.Get("Accept"), screenAcceptable(connector)), r.URL.Path, id, head)
			return
		}
		mediaType = chosen
	}
	chosen, f := parseSelection(r.URL.RawQuery, mediaType)
	if f != nil {
		writeFault(w, f, r.URL.Path, id, head)
		return
	}
	if head {
		w.Header().Set("Content-Type", contentTypeOf(mediaType))
		return
	}

	// A capture with a t= end records when it ends, so the 503 a
	// second caller gets names that instant rather than a hang-up.
	ends := time.Time{}
	if chosen.Time.HasEnd {
		ends = s.now().Add(seconds(chosen.Time.End))
	}
	done, f := s.take(connector, ends)
	if f != nil {
		writeFault(w, f, r.URL.Path, id, head)
		return
	}
	defer done()

	if f := s.stream(w, r, connector, mediaType, chosen); f != nil {
		writeFault(w, f, r.URL.Path, id, head)
	}
}

// One line at the first frame of every capture. It carries what the
// compositor sent and what this process made of it: the DRM fourcc,
// the -pixel_format derived from it, the size and scale the output
// reported, and the graph the frames run through. A drill reads the
// node's own answer here rather than inferring it from ffmpeg's echo
// of its input.
func (s *captureServer) report(connector string, screen captureScreen, mediaType string) {
	fmt.Printf("the capture of %s: %s at %dx%d scale %d, fourcc %s (%#08x) as %s, %s, %s conversion\n",
		connector, mediaType, screen.Width, screen.Height, screen.Scale,
		fourccName(screen.Format), screen.Format, screen.PixelFormat,
		s.socketPath, s.conversionName())
}

// An answer that is already streaming cannot become a problem
// document, because its status line is on the wire. This reports the
// cause to the log, and the response ends.
func (s *captureServer) reportMidStream(connector string, err error) {
	fmt.Fprintf(os.Stderr, "the capture of %s ended: %v\n", connector, err)
}

// The same report for a capture that ended while its caller was
// still reading, which is where a mode change lands: the compositor
// destroys the output, the connection dies with it, and the feed
// reads an end of file. The line carries both sizes, the one the
// capture was encoding at and the one the screen serves now, because
// a reader of the log cannot otherwise tell a mode change from a
// compositor that stopped.
func (s *captureServer) reportEnded(connector string, was captureScreen, err error) {
	fmt.Fprintf(os.Stderr, "the capture of %s ended at %dx%d: %v%s\n",
		connector, was.Width, was.Height, err, s.sizeNow(connector))
}

// The size the screen serves now, for the log line of a capture that
// ended. A compositor that answers nothing says so instead: a mode
// change ends the connection, and the compositor is often still
// restarting when this line is written.
func (s *captureServer) sizeNow(connector string) string {
	session, err := openCapture(s.socketPath, connector, captureOpenTimeout)
	if err != nil {
		return fmt.Sprintf("; %s serves nothing now: %v", connector, err)
	}
	defer func() { _ = session.close() }()
	screen := session.screen()
	return fmt.Sprintf("; %s serves %dx%d now", connector, screen.Width, screen.Height)
}
