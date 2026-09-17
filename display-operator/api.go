package main

// This file is display-api, the one door a caller reaches a screen
// through. It names the caller from a verified client certificate or
// from a TokenReview, authorizes the request with a
// SubjectAccessReview, reads the Display to find its node, and
// forwards the request to the capture sidecar on that node. It stores
// nothing, and it never decodes, encodes, or holds a frame: the bytes
// the sidecar sends are the bytes the caller reads.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// The role argument the display-api Deployment passes to the binary.
const apiMode = "api"

// The component label this process carries on liken_build_info, and
// the port it serves HTTPS on. The Service maps 443 to it.
const (
	apiComponent   = "display-api"
	defaultAPIAddr = ":8443"
)

// The two Secrets and the ConfigMap the API owns, and the Service
// whose names its own leaf carries. Its one CA signs that leaf and
// the sidecar's, which carries the single SAN certs.go names.
const (
	apiTLSSecret     = "display-api-tls"
	sidecarTLSSecret = "display-capture-server"
	trustAnchorMap   = "display-api-ca"
	apiServiceName   = "display-api"
)

// The record a request that the caller abandoned carries. See hungUp.
const statusClientClosed = 499

// The describedby link names the Display object on the API server.
// It is absolute because RFC 8288 section 3.1 resolves a relative
// reference against the API's own origin, which is not the API
// server.
const describedByBase = "https://kubernetes.default.svc/apis/" +
	DisplayGroup + "/" + DisplayVersion + "/" + displaysPlural + "/"

// The manual page the service-doc relation (RFC 8631) points at.
const serviceDocument = "https://display.liken.sh/docs/reference/api/"

// The time in the Content-Disposition file name (RFC 6266 section 4)
// is RFC 3339 UTC with the colons replaced by hyphens, because a
// colon is not legal in a file name everywhere a browser saves. This
// is the one place the API states a time outside the standard form.
const dispositionTimeLayout = "2006-01-02T15-04-05Z"

// headerDeadline bounds how long the API waits for the sidecar's
// response headers, plus the t= begin the caller asked for.
// idleDeadline bounds the quiet a body may hold before the API ends
// the capture. Both are variables so a test can shorten them, the
// way this repository's other waits are.
var (
	headerDeadline = 10 * time.Second
	idleDeadline   = 30 * time.Second
)

// The server holds the cluster client it reads Displays with, the
// sidecar index it answers "which node" from memory with, the token
// cache that holds review verdicts, and the private-leg client it
// forwards captures on. record writes the Captured Event, and now is
// a field so a test can hold the clock still.
type apiServer struct {
	client     *Client
	sidecars   *sidecarIndex
	tokens     *tokenCache
	readings   *apiMetrics
	sidecar    *sidecarClient
	record     func(screen *Display, subject, aspect, form string)
	publicBase string
	now        func() time.Time
}

// The api role. The certificate comes first, because the API serves
// HTTPS only and has nothing to listen with before it holds one.
// Then the pod informer starts, and then the two listeners: the
// process listener for metrics and health, and the API listener.
func serveAPI() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	namespace := envOr("POD_NAMESPACE", sidecarNamespace)
	client, err := InClusterClient()
	if err != nil {
		fatal("in-cluster config: %v", err)
	}
	readings := newAPIMetrics(apiComponent, version)

	holder := &certificateHolder{}
	anchor, err := startAuthority(ctx, client, namespace, holder, readings)
	if err != nil {
		fatal("the serving certificate: %v", err)
	}

	// The cluster's client authority is read before the listener
	// starts, and again every minute. A read that fails is reported
	// and never fatal: an API that cannot read the ConfigMap still
	// answers every caller that sends a Bearer token, and the next
	// pass loads the authority once the grant or the API server is
	// back.
	anchors := &clientAnchors{}
	if err := anchors.load(client); err != nil {
		fmt.Fprintf(os.Stderr, "reading the cluster's client authority: %v\n", err)
	}
	go keepClientAnchors(ctx, client, anchors)

	index := newSidecarIndex()
	go index.run(ctx, client, namespace)

	server := &apiServer{
		client:   client,
		sidecars: index,
		tokens:   newTokenCache(func(token string) (*caller, *fault) { return reviewToken(client, token, apiAudience) }),
		readings: readings,
		sidecar:  newSidecarClient(anchor, captureTokenPath),
		record: func(screen *Display, subject, aspect, form string) {
			recordCapture(client, screen, subject, aspect, form)
		},
		publicBase: os.Getenv("PUBLIC_BASE"),
		now:        time.Now,
	}

	if address := envOr("METRICS_ADDR", defaultMetricsAddr); address != "" {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			fatal("metrics listener: %v", err)
		}
		go serveProcess(ctx, listener, processHandler(readings.registry, holder.held))
	}

	address := envOr("API_ADDR", defaultAPIAddr)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fatal("the API listener on %s: %v", address, err)
	}
	fmt.Printf("%s: serving %s on %s\n", apiComponent, apiRoot, address)
	serving := &http.Server{
		Handler:           server,
		ReadHeaderTimeout: headerDeadline,
		TLSConfig:         apiTLSConfig(holder, anchors),
	}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.ServeTLS(listener, "", ""); err != nil && ctx.Err() == nil {
		fatal("the API listener stopped: %v", err)
	}
}

// The metrics, health, and readiness listener is the same in every
// role of this binary; only the handler behind it differs.
func serveProcess(ctx context.Context, listener net.Listener, handler http.Handler) {
	serving := &http.Server{Handler: handler, ReadHeaderTimeout: metricsDeadline}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.Serve(listener); err != nil && ctx.Err() == nil {
		slog.Warn("the process listener stopped", "error", err)
	}
}

// One request, in this order: the route, the method, the token, the
// authorization verdict, the Display, the media type, the query, and
// only then the node. Authorization comes before the read, so a 403
// never says whether a name exists, and every knob is checked before
// anything on a node is asked to capture.
func (s *apiServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := s.now()
	id := newRequestID()
	head := r.Method == http.MethodHead

	route, name, matched := matchRoute(r.URL.Path)
	if !matched {
		s.refuse(w, r, apiRoute{template: "unmatched"}, id, start, head,
			notFound(fmt.Sprintf("this API serves no route at %s", r.URL.Path)))
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	case http.MethodOptions:
		s.allow(w, r, route, name, id, start)
		return
	default:
		s.refuse(w, r, route, id, start, head,
			methodNotAllowed(fmt.Sprintf("%s is not a method this API answers", r.Method)))
		return
	}

	who, f := s.authenticate(r)
	if f != nil {
		s.refuse(w, r, route, id, start, head, f)
		return
	}
	// The subject travels with the request from here on, so every
	// line this request logs names who asked.
	r = r.WithContext(context.WithValue(r.Context(), subjectKey{}, who.Username))
	if f := s.authorize(route, name, who); f != nil {
		s.refuse(w, r, route, id, start, head, f)
		return
	}

	if route.kind == documentRoute {
		s.serveDocument(w, r, route, id, start, head)
		return
	}

	screen, notServing := s.readDisplay(name)
	// A screen that is down still has a name, a node, and a mode, and
	// the info route answers those. Every other route needs the
	// screen itself, so the refusal stands for them.
	if notServing != nil && (route.kind != infoRoute || screen == nil) {
		s.refuse(w, r, route, id, start, head, notServing)
		return
	}

	mediaType, f := s.selectType(route, name, r.Header.Get("Accept"))
	if f != nil {
		s.refuse(w, r, route, id, start, head, f)
		return
	}
	if route.kind == infoRoute {
		s.serveInfo(w, r, route, name, screen, notServing, id, start, head)
		return
	}
	if notServing != nil {
		s.refuse(w, r, route, id, start, head, notServing)
		return
	}

	chosen, f := parseSelection(r.URL.RawQuery, mediaType)
	if f != nil {
		s.refuse(w, r, route, id, start, head, f)
		return
	}
	width, height, refresh := screenMode(screen)
	if f := chosen.validate(width, height, refresh); f != nil {
		s.refuse(w, r, route, id, start, head, f)
		return
	}
	s.serveCapture(w, r, route, name, screen, mediaType, chosen, who, id, start, head)
}

// A caller names itself two ways, and this API reads them in the
// order kube-apiserver reads them. A connection that carries a client
// certificate the cluster's authority signed is that certificate's
// subject. Otherwise the caller carries a token in the Authorization
// field, RFC 6750 section 2.1: a request with no token gets the bare
// challenge, and a token the TokenReview refuses gets the review's
// own words.
func (s *apiServer) authenticate(r *http.Request) (*caller, *fault) {
	if who, held := certificateCaller(r.TLS); held {
		return who, nil
	}
	field := r.Header.Get("Authorization")
	scheme, token, split := strings.Cut(field, " ")
	if !split || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return nil, unauthenticated("")
	}
	return s.tokens.verdict(strings.TrimSpace(token))
}

// Every route authorizes before it reads, so a 403 never says whether
// a name exists. The documents need no authorization; the info route
// needs get on displays; a capture needs get on displays/screen. The
// subresource exists in no CRD. It is a string RBAC matches, the way
// pods/log is matched.
func (s *apiServer) authorize(route apiRoute, name string, who *caller) *fault {
	if route.kind == documentRoute {
		return nil
	}
	subresource := ""
	if route.kind == captureRoute {
		subresource = screenAspect
	}
	allowed, reason, f := authorizeSubject(s.client, who, "get", DisplayGroup, displaysPlural, subresource, "", name)
	if f != nil {
		return f
	}
	if !allowed {
		// The API server states a reason for most refusals and states
		// none for a subject that simply matches no rule, so the rule
		// that was asked for is the answer when it says nothing.
		if reason == "" {
			reason = fmt.Sprintf("not allowed to get %s on %s", captureScope, name)
			if subresource == "" {
				reason = fmt.Sprintf("not allowed to get %s on %s", displaysPlural, name)
			}
		}
		return unauthorized(reason)
	}
	return nil
}

// A Display is absent, present with no node, or present with a
// node whose compositor may or may not be serving. Each of the last
// two is a 503 with Retry-After, not a 409: 409 is for a state the
// caller can act on, and a Display waiting for its node is not one.
// The two carry different types, because they are different waits.
// A screen with no node is waiting for the scheduler, which is a
// state every capture API has and which they share the type for. A
// compositor that is not serving is display's own: the operator
// restarts it, and the words the Display's own condition carries say
// what it is doing.
func (s *apiServer) readDisplay(name string) (*Display, *fault) {
	screen, err := get[Display](s.client, DisplaysPath+"/"+name)
	if err == ErrNotFound {
		return nil, notFound(fmt.Sprintf("no Display is named %s", name))
	}
	if err != nil {
		return nil, unavailable(problemUpstreamFailed, err.Error())
	}
	if screen.Status.Node == "" {
		return nil, unavailable(problemNoNode, fmt.Sprintf("the Display %s names no node yet", name))
	}
	for _, condition := range screen.Status.Conditions {
		if condition.Type == CompositorServingCondition && condition.Status == conditionFalse {
			// The object comes back with the refusal, because the
			// info route answers a screen that is down from the
			// object alone.
			return screen, unavailable(problemCompositorDown, condition.Message)
		}
	}
	return screen, nil
}

// An extension names one fixed representation, and an Accept that
// excludes it is a 406: RFC 9110 section 12.5.1 lets a server send
// 406 or ignore Accept, and a client that said two contradictory
// things is told. Without an extension, Accept negotiates among the
// screen's four forms.
func (s *apiServer) selectType(route apiRoute, name, accept string) (string, *fault) {
	if route.mediaType != "" {
		if acceptsType(accept, route.mediaType) {
			return route.mediaType, nil
		}
		if route.kind == captureRoute {
			return "", notAcceptable(accept, screenAcceptable(name))
		}
		return "", notAcceptable(accept, []acceptableForm{{Type: route.mediaType, Href: routePath(route, name)}})
	}
	chosen, ok := chooseType(accept, screenMediaTypes())
	if !ok {
		return "", notAcceptable(accept, screenAcceptable(name))
	}
	return chosen, nil
}

// The mode the Display reports is what the knobs are checked
// against: width and height bound the scale, and the refresh bounds
// the framerate. A Display that reports no mode refuses no knob.
func screenMode(screen *Display) (width, height, refresh int) {
	if screen.Status.Mode == nil {
		return 0, 0, 0
	}
	mode, err := parseMode(screen.Status.Mode.Kernel)
	if err != nil {
		return 0, 0, 0
	}
	if _, err := fmt.Sscanf(mode.Name, "%dx%d", &width, &height); err != nil {
		return 0, 0, 0
	}
	return width, height, mode.Refresh
}

// OPTIONS answers the methods in Allow and nothing else, RFC 9110
// section 9.3.7, with a 204 and no body.
func (s *apiServer) allow(w http.ResponseWriter, r *http.Request, route apiRoute, name, id string, start time.Time) {
	w.Header().Set("Allow", allowedMethods)
	w.Header().Set("Vary", "Accept")
	s.links(w, route, name, "")
	w.WriteHeader(http.StatusNoContent)
	s.logged(r, route, id, "", http.StatusNoContent, 0, start)
}

// The discovery and OpenAPI documents change only with the build,
// so the build version is their ETag, and a client that holds it
// gets a 304.
func (s *apiServer) serveDocument(w http.ResponseWriter, r *http.Request, route apiRoute, id string, start time.Time, head bool) {
	mediaType, f := s.selectType(route, "", r.Header.Get("Accept"))
	if f != nil {
		s.refuse(w, r, route, id, start, head, f)
		return
	}
	var body []byte
	var err error
	if route.mediaType == openAPIMediaType {
		body, err = renderJSON(openAPIDocument(requestOrigin(r, s.publicBase)))
	} else {
		body, err = renderJSON(discovery())
	}
	if err != nil {
		s.refuse(w, r, route, id, start, head, upstreamFailed(err.Error()))
		return
	}
	s.document(w, r, route, "", mediaType, id, start, head, body)
}

// The info document carries the screen's own numbers, read from the
// sidecar on the node, which has them from the compositor's wl_output
// events. Its Links point at the routes that capture the screen with
// the related relation. A HEAD asks the node for nothing.
//
// A screen whose compositor is down, and a screen whose sidecar this
// API cannot reach, answer 200 from the Display object alone: the
// name, the node, and the mode the Display's status reports, with
// what is wrong beside them. A caller asking what a screen is must
// not need the screen to be up, and the members that come from the
// node are left out rather than guessed.
func (s *apiServer) serveInfo(w http.ResponseWriter, r *http.Request, route apiRoute,
	name string, screen *Display, down *fault, id string, start time.Time, head bool) {
	if head {
		s.document(w, r, route, name, jsonMediaType, id, start, head, nil)
		return
	}
	info, f := s.screenInfo(r, screen, down)
	if f != nil {
		if s.hungUp(r, route, id, start) {
			return
		}
		s.refuse(w, r, route, id, start, head, f)
		return
	}
	info.Name, info.Node = name, screen.Status.Node
	body, err := renderJSON(info)
	if err != nil {
		s.refuse(w, r, route, id, start, head, upstreamFailed(err.Error()))
		return
	}
	s.document(w, r, route, name, jsonMediaType, id, start, head, body)
}

// What the info route answers with: the node's own reading of the
// screen, or the Display's when the node cannot be asked.
func (s *apiServer) screenInfo(r *http.Request, screen *Display, down *fault) (screenInfo, *fault) {
	if down != nil {
		return staticInfo(screen, "compositor", "down", down.detail), nil
	}
	pod, f := s.reach(screen)
	if f != nil {
		return staticInfo(screen, "sidecar", "unreachable", f.detail), nil
	}
	info, f := s.sidecar.info(r.Context(), pod, screen.Status.Connector)
	if f != nil {
		if r.Context().Err() != nil {
			return screenInfo{}, f
		}
		return staticInfo(screen, "sidecar", "unreachable", f.detail), nil
	}
	return info, nil
}

// The screen as the Display states it, for a node that cannot be
// asked. The mode status reports gives the size and the refresh;
// scale, formats, and conversion come from the compositor and the
// encoder, so they are absent rather than invented.
func staticInfo(screen *Display, part, state, detail string) screenInfo {
	width, height, refresh := screenMode(screen)
	info := screenInfo{Width: width, Height: height, Refresh: refresh, Detail: detail}
	if part == "compositor" {
		info.Compositor = state
		return info
	}
	info.Sidecar = state
	return info
}

// Every document answer carries an ETag, Vary: Accept, and
// Cache-Control: no-cache. no-cache lets a cache hold the document
// and asks it to revalidate with If-None-Match on every use, so a
// client after an upgrade reads the new build's document.
func (s *apiServer) document(w http.ResponseWriter, r *http.Request, route apiRoute,
	name, mediaType, id string, start time.Time, head bool, body []byte) {
	tag := `"` + version + `"`
	w.Header().Set("ETag", tag)
	w.Header().Set("Vary", "Accept")
	w.Header().Set("Cache-Control", "no-cache")
	s.links(w, route, name, "")
	if matchesTag(r.Header.Get("If-None-Match"), tag) {
		w.WriteHeader(http.StatusNotModified)
		s.readings.answered(route.template, r.Method, http.StatusNotModified, s.now().Sub(start))
		s.logged(r, route, id, "", http.StatusNotModified, 0, start)
		return
	}
	w.Header().Set("Content-Type", mediaType)
	if head {
		s.readings.answered(route.template, r.Method, http.StatusOK, s.now().Sub(start))
		s.logged(r, route, id, "", http.StatusOK, 0, start)
		return
	}
	w.WriteHeader(http.StatusOK)
	written, _ := w.Write(body)
	s.readings.answered(route.template, r.Method, http.StatusOK, s.now().Sub(start))
	s.logged(r, route, id, "", http.StatusOK, written, start)
}

// If-None-Match is a star, which matches any representation, or a
// list of entity tags. RFC 9110 section 13.1.2 asks for the weak
// comparison, so a W/ prefix is dropped before the tags are compared.
func matchesTag(field, tag string) bool {
	field = strings.TrimSpace(field)
	if field == "" {
		return false
	}
	if field == "*" {
		return true
	}
	for candidate := range strings.SplitSeq(field, ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == tag {
			return true
		}
	}
	return false
}

// A route's own path is its template with the one name filled in.
// An href in a 406 document names a route this way.
func routePath(route apiRoute, name string) string {
	return strings.ReplaceAll(route.template, "{name}", name)
}

// A capture answer carries Cache-Control: no-store (RFC 9111 section
// 5.2.2.5), Accept-Ranges: none (RFC 9110 section 14.3), Vary: Accept
// (section 12.5.5), and Content-Disposition with a file name (RFC
// 6266 section 4). Content-Location goes on the negotiated route
// alone, as section 8.7's "more specific identifier for the selected
// representation". Section 8.7's identity guarantee, that a GET on it
// returns the same representation, does not hold for a live capture,
// which is also why Accept-Ranges is none. Vary is on the extension
// routes too, where Accept only decides between 200 and 406, for
// section 12.5.5's second purpose: to say the response was subject to
// negotiation.
func (s *apiServer) captureHeaders(w http.ResponseWriter, route apiRoute,
	name, mediaType, served string, at time.Time) {
	ext := route.ext
	if ext == "" {
		ext = formExtension(mediaType)
		w.Header().Set("Content-Location", capturePath(name, ext))
	}
	w.Header().Set("Content-Type", served)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("Vary", "Accept")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s-%s.%s"`,
		name, at.UTC().Format(dispositionTimeLayout), ext))
	s.links(w, route, name, mediaType)
}

// Every response carries the two RFC 8631 relations, and every
// response about one Display carries describedby. The info route
// adds related links to the capture routes (RFC 4287, registered).
// The negotiated capture route alone adds alternate links, one per
// extension form, with a type that carries no media type parameters.
func (s *apiServer) links(w http.ResponseWriter, route apiRoute, name, mediaType string) {
	serviceLinks(w)
	if name == "" {
		return
	}
	w.Header().Add("Link", fmt.Sprintf(`<%s%s>; rel="describedby"`, describedByBase, name))
	switch {
	case route.kind == infoRoute:
		for _, form := range screenForms {
			w.Header().Add("Link", fmt.Sprintf(`<%s>; rel="related"; type=%q`,
				capturePath(name, form.ext), form.mediaType))
		}
	case route.kind == captureRoute && route.ext == "":
		for _, form := range screenForms {
			w.Header().Add("Link", fmt.Sprintf(`<%s>; rel="alternate"; type=%q`,
				capturePath(name, form.ext), form.mediaType))
		}
	}
}

// The caller hung up while the API was reaching the node. There is
// nobody to answer, so the request writes no problem document and
// ends on its log line. The status the record carries is 499, which
// no RFC defines and nothing sends on the wire: it is the number
// every proxy writes down for a client that closed, and it keeps the
// request in the count instead of hiding it under a 503 nobody read.
func (s *apiServer) hungUp(r *http.Request, route apiRoute, id string, start time.Time) bool {
	if r.Context().Err() == nil {
		return false
	}
	s.readings.answered(route.template, r.Method, statusClientClosed, s.now().Sub(start))
	s.logged(r, route, id, "the client closed the connection", statusClientClosed, 0, start)
	return true
}

// A refusal is a problem document. A HEAD carries none, and the
// request id in the document's instance is the id in the log line.
func (s *apiServer) refuse(w http.ResponseWriter, r *http.Request, route apiRoute,
	id string, start time.Time, head bool, f *fault) {
	writeFault(w, f, r.URL.Path, id, head)
	s.readings.answered(route.template, r.Method, f.status, s.now().Sub(start))
	s.logged(r, route, id, f.logged(), f.status, 0, start)
}

// Every request writes one log line: the route as its template, the
// method, the status, the bytes, the seconds, the request id, and
// the subject. The token is never in it.
func (s *apiServer) logged(r *http.Request, route apiRoute, id, detail string, status, bytes int, start time.Time) {
	slog.Info("request",
		"route", route.template,
		"method", r.Method,
		"status", status,
		"bytes", bytes,
		"seconds", s.now().Sub(start).Seconds(),
		"request", id,
		"subject", subjectOf(r),
		"detail", detail)
}

// The subject a log line names comes from the client certificate or
// the TokenReview, and is empty for a request refused before either
// one named a caller.
func subjectOf(r *http.Request) string {
	if who, held := r.Context().Value(subjectKey{}).(string); held {
		return who
	}
	return ""
}

type subjectKey struct{}

// A capture goes to the sidecar pod on the Display's node. No sidecar
// on that node, a pod with no address yet, and a pod the kubelet does
// not call Ready are all 503, because each one clears on its own.
func (s *apiServer) reach(screen *Display) (sidecarPod, *fault) {
	pod, held := s.sidecars.on(screen.Status.Node)
	if !held {
		return pod, unavailable(problemUpstreamFailed,
			fmt.Sprintf("no capture sidecar runs on %s", screen.Status.Node))
	}
	if pod.IP == "" || !pod.Ready {
		notReady := unavailable(problemUpstreamFailed,
			fmt.Sprintf("the capture sidecar on %s is not ready", screen.Status.Node))
		notReady.log = fmt.Sprintf("%s/%s at %q, ready=%t", pod.Namespace, pod.Name, pod.IP, pod.Ready)
		return pod, notReady
	}
	return pod, nil
}
