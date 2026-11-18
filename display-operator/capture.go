package main

// The capture sidecar is the only process on a node that reads frames
// from the compositor. It serves the info and capture routes over
// TLS, and only to display-api, which authenticates with a token for
// the audience display-capture.
//
// It allows one capture per output by default. The compositor's
// framebuffer source turns off hardware planes and forces a repaint
// for every frame, so a second capture on the same output would
// double that cost and each capture would get half the frames.

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

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// captureMode is the first argument that starts the binary as the
// capture sidecar. captureComponent is the component label on
// liken_build_info.
const (
	captureMode      = "capture"
	captureComponent = "display-capture"
)

// defaultCaptureAddr serves captures and metrics on one port. The
// operator container in the same pod already listens on the usual
// metrics port, 9200.
const defaultCaptureAddr = ":9201"

// captureSocketPath is the Wayland socket that the layout module
// opens for the sidecar alone. It is in the weston-config emptyDir and
// not in XDG_RUNTIME_DIR, because the CDI spec mounts that directory
// into every consumer container, and a consumer must not be able to
// capture the screen.
const captureSocketPath = "/etc/weston/wayland-capture"

// capturePerOutput is the environment variable that sets how many
// captures can run on one output at once. The default is 1, for the
// reason at the top of this file.
const capturePerOutput = "CAPTURE_PER_OUTPUT"

// captureConversion is the environment variable that selects the
// ffmpeg conversion graph, softwareGraph or vaapiGraph, in place of the
// probe in chooseConversion. The sidecar chooses once at startup,
// because a graph that cannot be built fails the same way on every
// request.
const (
	captureConversion = "CAPTURE_CONVERSION"
	softwareGraph     = "software"
	vaapiGraph        = "vaapi"
)

// conversionProbeTimeout limits the startup probe, which converts one
// 64 by 64 frame. A probe that runs this long means that the GPU
// driver does not respond.
const conversionProbeTimeout = 20 * time.Second

// captureOpenTimeout is how long the client waits for the compositor
// at each step: the connection, the registry, the output's events,
// and each frame.
const captureOpenTimeout = 5 * time.Second

// captureServer serves the sidecar's routes. running lists the
// captures in progress on each connector.
type captureServer struct {
	client     *apiclient.Client
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

// runningCapture records when a capture ends, so the 503 to the next
// caller can say how long to wait. ends is the zero time when the
// capture lasts until its client disconnects.
type runningCapture struct {
	ends time.Time
}

// serveCaptureSidecar runs the capture sidecar until SIGINT or
// SIGTERM. It exits when the serving certificate does not load,
// before it opens the listener.
func serveCaptureSidecar() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		fatal("in-cluster config: %v", err)
	}
	readings := newCaptureMetrics(captureComponent, version)

	holder := &certificateHolder{}
	directory := envOr("CAPTURE_TLS_DIR", captureTLSDir)
	if err := loadCaptureLeaf(directory, holder, time.Now()); err != nil {
		fatal("the capture certificate: %v", err)
	}
	readings.ready(holder.verifiable())
	go watchCaptureLeaf(ctx, directory, holder, readings, reloadFallback)

	// A node with no render node still starts the sidecar, so the pod
	// does not restart in a loop. The sidecar serves its metrics, and
	// chooseConversion selects the CPU graph.
	device, err := renderNode(driRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "finding the render node: %v\n", err)
	}

	server := &captureServer{
		client:     client,
		tokens:     newTokenCache(func(token string) (*caller, *fault) { return reviewToken(client, token, captureAudience) }),
		readings:   readings,
		process:    processHandler(readings.registry, holder.verifiable),
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
	// A 401 from this port names the realm display-capture, so a person
	// who reads the header can tell it from a 401 of display-api.
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

// chooseConversion returns true when the sidecar converts frames on
// the CPU, and false when it converts them in scale_vaapi on the GPU.
// A value of CAPTURE_CONVERSION is used as given, because a person can
// know about a driver fault that the probe does not find. With no
// value, a node with no render node uses the CPU, and any other node
// uses the GPU when the probe succeeds.
func chooseConversion(ctx context.Context, device string) bool {
	switch envOr(captureConversion, "") {
	case softwareGraph:
		fmt.Printf("%s: %s is set to the software conversion, so this node converts on the CPU\n",
			captureComponent, captureConversion)
		return true
	case vaapiGraph:
		fmt.Printf("%s: %s is set to the GPU graph, so this node converts in scale_vaapi\n",
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

// perOutputLimit returns 1 when CAPTURE_PER_OUTPUT is not a positive
// integer.
func perOutputLimit() int {
	value, err := strconv.Atoi(envOr(capturePerOutput, "1"))
	if err != nil || value < 1 {
		return 1
	}
	return value
}

// ServeHTTP serves /metrics, /healthz, and /readyz with no token,
// because the kubelet and the metrics scraper send none. Every other
// path needs display-api's token.
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
		writeFault(w, notFound(fmt.Sprintf("the capture sidecar has no route at %s", r.URL.Path)), r.URL.Path, id, head)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeFault(w, methodNotAllowed(fmt.Sprintf("the capture sidecar does not accept the method %s", r.Method)), r.URL.Path, id, head)
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

// authenticate accepts only display-api. The TokenReview is for the
// audience display-capture, so a token for another audience fails
// with a 401. A valid token of another ServiceAccount fails with a 403.
func (s *captureServer) authenticate(r *http.Request) *fault {
	scheme, token, split := strings.Cut(r.Header.Get("Authorization"), " ")
	if !split || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return unauthenticated("")
	}
	who, f := s.tokens.lookup(strings.TrimSpace(token))
	if f != nil {
		return f
	}
	if !isServiceAccount(who.Username, sidecarNamespace, apiServiceName) {
		return unauthorized(fmt.Sprintf("%s is not the display API", who.Username))
	}
	return nil
}

// answerInfo opens a Wayland connection for each request and closes
// it after, so the sidecar keeps no connection to the compositor while
// no one captures.
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
		Codecs:     h264Codecs(clipWidth(screen.Width), clipHeight(screen), defaultFramerate("video/mp4")),
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

// compositorFault returns a 500 when weston refused the capture with
// a failed event, because a retry gets the same refusal. Any other
// error, such as a missing socket, a closed connection, or a timeout,
// is a 503, because a retry can succeed.
func (s *captureServer) compositorFault(connector string, err error) *fault {
	var refused *captureFailure
	if errors.As(err, &refused) {
		s.readings.failed(deniedReason)
		return captureDenied(refused.Message)
	}
	s.readings.failed(compositorReason)
	return unavailable(problemNoNode, fmt.Sprintf("the compositor on %s: %v", connector, err))
}

// take reserves one capture on a connector and returns the function
// that releases it. When the connector already has perOutput captures
// running, it returns a 503 whose detail gives the end of the oldest.
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

// until returns the end of a capture for the 503 detail.
func until(ends time.Time) string {
	if ends.IsZero() {
		return "until its client closes"
	}
	return "until " + ends.UTC().Format(time.RFC3339)
}

// answerCapture parses the query again, after display-api has
// validated it, so the sidecar does not depend on its caller's checks.
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
		w.Header().Set("X-Content-Type-Options", "nosniff")
		return
	}

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

// report logs the first frame of a capture: the output's size and
// scale, the DRM fourcc that the compositor sent, the ffmpeg
// -pixel_format for it, the socket, and the conversion graph, so a
// person does not have to infer them from ffmpeg's log of its input.
func (s *captureServer) report(connector string, screen captureScreen, mediaType string) {
	fmt.Printf("the capture of %s: %s at %dx%d scale %d, fourcc %s (%#08x) as %s, %s, %s conversion\n",
		connector, mediaType, screen.Width, screen.Height, screen.Scale,
		fourccName(screen.Format), screen.Format, screen.PixelFormat,
		s.socketPath, s.conversionName())
}

// reportMidStream logs an error after the response began. The status
// line is already sent, so the error cannot become a problem document.
func (s *captureServer) reportMidStream(connector string, err error) {
	fmt.Fprintf(os.Stderr, "the capture of %s ended after the response began: %v\n", connector, err)
}

// reportEnded logs a capture that ended while its caller still read.
// A mode change ends a capture this way, because the compositor
// destroys the output and the connection closes. The line gives the
// size that the capture encoded and the output's size now, so a person
// can tell a mode change from a compositor that stopped.
func (s *captureServer) reportEnded(connector string, was captureScreen, err error) {
	fmt.Fprintf(os.Stderr, "the capture of %s ended at %dx%d: %v%s\n",
		connector, was.Width, was.Height, err, s.sizeNow(connector))
}

// sizeNow returns the output's size now, or the error when the
// compositor does not respond, which is common while it restarts after
// a mode change.
func (s *captureServer) sizeNow(connector string) string {
	session, err := openCapture(s.socketPath, connector, captureOpenTimeout)
	if err != nil {
		return fmt.Sprintf("; %s has no screen now: %v", connector, err)
	}
	defer func() { _ = session.close() }()
	screen := session.screen()
	return fmt.Sprintf("; %s serves %dx%d now", connector, screen.Width, screen.Height)
}
