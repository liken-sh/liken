package main

// display-api is the HTTPS API that callers use to read and capture a
// screen. It identifies the caller from a verified client certificate
// or a TokenReview, authorizes the request with a SubjectAccessReview,
// reads the Display to find its node, and forwards the request to the
// capture sidecar on that node. It never decodes or encodes a frame:
// the caller receives the bytes that the sidecar sends.

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

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// apiMode is the role argument that the display-api Deployment passes
// to the binary.
const apiMode = "api"

// apiComponent is the component label on liken_build_info.
// defaultAPIAddr is the listen address, and the Service maps port 443
// to it.
const (
	apiComponent   = "display-api"
	defaultAPIAddr = ":8443"
)

// The API creates these Secrets and the ConfigMap. One CA signs the
// API's serving certificate, which has the DNS names of the Service,
// and the sidecar's certificate, which has the name sidecarName.
const (
	apiTLSSecret     = "display-api-tls"
	sidecarTLSSecret = "display-capture-server"
	trustAnchorMap   = "display-api-ca"
	apiServiceName   = "display-api"
)

// statusClientClosed is the status that the log and the metrics record
// for a request whose caller closed the connection. See hungUp.
const statusClientClosed = 499

// describedByBase is the prefix of the describedby link, which points
// at the Display object on the Kubernetes API server. It is absolute
// because RFC 8288 section 3.1 resolves a relative reference against
// this API's origin, and the Display is not served there.
const describedByBase = "https://kubernetes.default.svc/apis/" +
	DisplayGroup + "/" + DisplayVersion + "/" + displaysPlural + "/"

// serviceDocument is the manual page that the service-doc link
// relation (RFC 8631) points at.
const serviceDocument = "https://liken.sh/display/docs/reference/api/"

// dispositionTimeLayout formats the time in the Content-Disposition
// file name (RFC 6266 section 4). It is RFC 3339 in UTC with hyphens
// in place of colons, because Windows does not allow a colon in a file
// name.
const dispositionTimeLayout = "2006-01-02T15-04-05Z"

// headerDeadline is how long the API waits for a caller's request
// headers and for the sidecar's response headers. For a capture whose
// query sets a t= start time, the wait for the sidecar's headers is
// headerDeadline plus that start time, because the sidecar drops every
// frame before the start time. idleDeadline is how long a capture may
// send no bytes before the API ends it.
const (
	headerDeadline = 10 * time.Second
	idleDeadline   = 30 * time.Second
)

// apiServer is the http.Handler of display-api. sidecars is filled by
// an informer, so finding a node's sidecar makes no API request. record
// posts the Captured Event.
type apiServer struct {
	client     *apiclient.Client
	sidecars   *sidecarIndex
	tokens     *tokenCache
	readings   *apiMetrics
	sidecar    *sidecarClient
	record     func(screen *Display, subject, aspect, form string)
	publicBase string
	now        func() time.Time
}

// serveAPI runs the api role. It loads or creates the serving
// certificate before it opens a listener, because the API serves only
// HTTPS.
func serveAPI() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	namespace := envOr("POD_NAMESPACE", sidecarNamespace)
	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		fatal("in-cluster config: %v", err)
	}
	watcher, err := informer.InCluster()
	if err != nil {
		fatal("in-cluster config for the watches: %v", err)
	}
	readings := newAPIMetrics(apiComponent, version)
	// events.Options{} leaves the reportingInstance as the host name,
	// which is the pod name, so a reader can tell which replica posted
	// an Event.
	recorder := events.New(ctx, client, apiComponent, events.Options{})

	holder := &certificateHolder{}
	anchor, err := startAuthority(ctx, client, watcher, namespace, holder, readings)
	if err != nil {
		fatal("the serving certificate: %v", err)
	}

	// A failed read of the client CA bundle is logged and does not stop
	// the API, because a caller with a Bearer token does not need the
	// bundle. The watch on the ConfigMap loads it when the read starts
	// to work, and applies each later change.
	anchors := &clientAnchors{}
	if err := anchors.load(client); err != nil {
		fmt.Fprintf(os.Stderr, "the API starts without a client CA bundle: %v\n", err)
	}
	go keepClientAnchors(ctx, watcher, anchors)

	index := newSidecarIndex()
	go index.run(ctx, watcher, namespace)

	server := &apiServer{
		client:   client,
		sidecars: index,
		tokens:   newTokenCache(func(token string) (*caller, *fault) { return reviewToken(client, token, apiAudience) }),
		readings: readings,
		sidecar:  newSidecarClient(anchor, captureTokenPath),
		record: func(screen *Display, subject, aspect, form string) {
			recordCapture(recorder, screen, subject, aspect, form)
		},
		publicBase: os.Getenv("PUBLIC_BASE"),
		now:        time.Now,
	}

	if address := envOr("METRICS_ADDR", defaultMetricsAddr); address != "" {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			fatal("metrics listener: %v", err)
		}
		go serveProcess(ctx, listener, processHandler(readings.registry, holder.verifiable))
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

// serveProcess serves metrics, health, and readiness for every role
// of this binary.
func serveProcess(ctx context.Context, listener net.Listener, handler http.Handler) {
	serving := &http.Server{Handler: handler, ReadHeaderTimeout: metricsDeadline}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.Serve(listener); err != nil && ctx.Err() == nil {
		slog.Warn("The metrics and health listener stopped", "error", err)
	}
}

// ServeHTTP checks a request in this order: route, method, caller,
// authorization, the Display, the media type, and the query
// parameters. It contacts the sidecar only after every check passes.
func (s *apiServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := s.now()
	id := newRequestID()
	head := r.Method == http.MethodHead

	route, name, matched := matchRoute(r.URL.Path)
	if !matched {
		s.refuse(w, r, apiRoute{template: "unmatched"}, id, start, head,
			notFound(fmt.Sprintf("the API has no route at %s", r.URL.Path)))
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	case http.MethodOptions:
		s.allow(w, r, route, name, id, start)
		return
	default:
		s.refuse(w, r, route, id, start, head,
			methodNotAllowed(fmt.Sprintf("the API does not accept the method %s", r.Method)))
		return
	}

	who, f := s.authenticate(r)
	if f != nil {
		s.refuse(w, r, route, id, start, head, f)
		return
	}
	// The log line of the request reads the caller from the context.
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
	// The info route responds from the Display object when the screen
	// cannot be captured, as long as the Display was read. Every other
	// route returns the fault.
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

// authenticate tries a client certificate that the cluster's CA signed
// first, as kube-apiserver does, and then a Bearer token in the
// Authorization header (RFC 6750 section 2.1). A request with neither
// gets a 401 with the challenge alone. A rejected token gets the
// TokenReview's error text.
func (s *apiServer) authenticate(r *http.Request) (*caller, *fault) {
	if who, held := certificateCaller(r.TLS); held {
		return who, nil
	}
	field := r.Header.Get("Authorization")
	scheme, token, split := strings.Cut(field, " ")
	if !split || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return nil, unauthenticated("")
	}
	return s.tokens.lookup(strings.TrimSpace(token))
}

// authorize runs a SubjectAccessReview before the Display is read, so
// a 403 does not reveal whether a Display exists. The documents need
// no authorization. The info route needs get on displays, and a
// capture needs get on displays/screen. The CRD does not define the
// screen subresource, but RBAC matches a subresource by its name
// alone, as it does for pods/log.
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
		// The API server returns no reason when no rule matches the
		// subject. Then the 403 names the rule that was checked.
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

// readDisplay reads the Display and returns a fault when the screen
// cannot be captured. A Display with no node, or whose compositor is
// not serving, gets a 503 with Retry-After, because the caller can
// only wait. no-node is a problem type that the audio and media APIs
// share, and compositor-down belongs to display alone. Its detail is
// the message of the CompositorServing condition.
//
// A Display whose Connected condition is False gets a 409, because
// retrying does not help until someone connects a monitor. Connected
// is checked first, because a Display keeps its last CompositorServing
// condition after the monitor is unplugged.
func (s *apiServer) readDisplay(name string) (*Display, *fault) {
	screen, err := apiclient.Get[Display](s.client, DisplaysPath+"/"+name)
	if err == apiclient.ErrNotFound {
		return nil, notFound(fmt.Sprintf("no Display is named %s", name))
	}
	if err != nil {
		return nil, unavailable(problemUpstreamFailed, err.Error())
	}
	if screen.Status.Node == "" {
		return nil, unavailable(problemNoNode, fmt.Sprintf("the Display %s has no status.node yet", name))
	}
	for _, condition := range screen.Status.Conditions {
		if condition.Type == ConnectedCondition && condition.Status == conditionFalse {
			return screen, away(condition.Message)
		}
	}
	for _, condition := range screen.Status.Conditions {
		if condition.Type == CompositorServingCondition && condition.Status == conditionFalse {
			// The info route needs the Display even with this fault.
			return screen, unavailable(problemCompositorDown, condition.Message)
		}
	}
	return screen, nil
}

// selectType returns the media type of the response. A route with a
// file extension has one fixed type, and an Accept header that excludes
// it gets a 406. RFC 9110 section 12.5.1 lets a server either return
// 406 or ignore Accept, and this API returns 406 so that a client
// learns that its Accept and its extension disagree. Without an
// extension, Accept chooses among screenForms.
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

// screenMode returns the size and refresh rate from the Display's
// status, which limit the scale and framerate query parameters. It
// returns zeros when the status has no mode that parses, and then
// validate applies no limit.
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

// allow responds to OPTIONS with the Allow header, a 204, and no body
// (RFC 9110 section 9.3.7).
func (s *apiServer) allow(w http.ResponseWriter, r *http.Request, route apiRoute, name, id string, start time.Time) {
	w.Header().Set("Allow", allowedMethods)
	w.Header().Set("Vary", "Accept")
	s.links(w, route, name, "")
	w.WriteHeader(http.StatusNoContent)
	s.logged(r, route, id, "", http.StatusNoContent, 0, start)
}

// serveDocument serves the discovery or OpenAPI document. Both change
// only with a new build, so document uses the build version as the
// ETag.
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

// serveInfo serves the info document, which the sidecar builds from
// the compositor's wl_output events. A HEAD request does not contact
// the sidecar.
//
// When the monitor is unplugged, the compositor is not serving, or the
// sidecar cannot be reached, the response is still a 200, built by
// staticInfo from the Display. That way a caller can read a screen's
// node and mode, and what is wrong with it, while it cannot be
// captured.
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

// screenInfo returns the sidecar's info document, or staticInfo when
// the sidecar cannot be reached. It returns a fault only when the
// caller's request was canceled.
func (s *apiServer) screenInfo(r *http.Request, screen *Display, down *fault) (screenInfo, *fault) {
	if down != nil && down.kind == problemAway {
		return staticInfo(screen, "panel", "disconnected", down.detail), nil
	}
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

// staticInfo builds the info document from the Display's status, for a
// screen whose compositor or sidecar cannot answer. part is "panel",
// "compositor", or "sidecar", and staticInfo sets the field of that
// name to state. The scale, formats, and conversion come from the
// compositor and the encoder, so they are left empty.
func staticInfo(screen *Display, part, state, detail string) screenInfo {
	width, height, refresh := screenMode(screen)
	info := screenInfo{Width: width, Height: height, Refresh: refresh, Detail: detail}
	switch part {
	case "panel":
		info.Panel = state
	case "compositor":
		info.Compositor = state
	default:
		info.Sidecar = state
	}
	return info
}

// document writes a discovery, OpenAPI, or info response with the
// build version as its ETag, and a 304 when If-None-Match matches it.
// Cache-Control: no-cache lets a cache store the document but makes it
// revalidate on every use, so a client gets the new document after an
// upgrade.
func (s *apiServer) document(w http.ResponseWriter, r *http.Request, route apiRoute,
	name, mediaType, id string, start time.Time, head bool, body []byte) {
	tag := `"` + version + `"`
	w.Header().Set("ETag", tag)
	w.Header().Set("Vary", "Accept")
	w.Header().Set("Cache-Control", "no-cache")
	s.links(w, route, name, "")
	if matchesTag(r.Header.Get("If-None-Match"), tag) {
		w.WriteHeader(http.StatusNotModified)
		s.readings.recordRequest(route.template, r.Method, http.StatusNotModified, s.now().Sub(start))
		s.logged(r, route, id, "", http.StatusNotModified, 0, start)
		return
	}
	w.Header().Set("Content-Type", mediaType)
	if head {
		s.readings.recordRequest(route.template, r.Method, http.StatusOK, s.now().Sub(start))
		s.logged(r, route, id, "", http.StatusOK, 0, start)
		return
	}
	w.WriteHeader(http.StatusOK)
	written, _ := w.Write(body)
	s.readings.recordRequest(route.template, r.Method, http.StatusOK, s.now().Sub(start))
	s.logged(r, route, id, "", http.StatusOK, written, start)
}

// matchesTag reports whether an If-None-Match value matches tag. RFC
// 9110 section 13.1.2 requires the weak comparison for If-None-Match,
// so a W/ prefix is ignored.
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

func routePath(route apiRoute, name string) string {
	return strings.ReplaceAll(route.template, "{name}", name)
}

// captureHeaders sets the headers of a capture response. Every capture
// is a new frame or stream, so Cache-Control is no-store (RFC 9111
// section 5.2.2.5) and Accept-Ranges is none (RFC 9110 section 14.3).
// Content-Location is set only on the negotiated route, where it gives
// the extension route of the chosen type (RFC 9110 section 8.7).
//
// Vary: Accept is set on the extension routes too, because Accept
// chooses between 200 and 406 there, and RFC 9110 section 12.5.5
// requires Vary whenever Accept can change the response. The callers
// set Content-Type and X-Content-Type-Options.
func (s *apiServer) captureHeaders(w http.ResponseWriter, route apiRoute,
	name, mediaType string, at time.Time) {
	ext := route.ext
	if ext == "" {
		ext = formExtension(mediaType)
		w.Header().Set("Content-Location", capturePath(name, ext))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("Vary", "Accept")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s-%s.%s"`,
		name, at.UTC().Format(dispositionTimeLayout), ext))
	s.links(w, route, name, mediaType)
}

// links sets the Link headers. Every response has the service-desc and
// service-doc relations (RFC 8631), and every response about one
// Display has describedby. The info route adds a related link to each
// capture route (RFC 4287). The negotiated capture route adds an
// alternate link to each extension route, with a type that has no
// media type parameters.
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

// hungUp reports whether the caller closed the connection while the
// API waited for the sidecar. Then nobody can read a response, so it
// writes none, and records the request in the log and the metrics with
// statusClientClosed. No RFC defines 499. nginx logs it for a client
// that closed the connection, and it keeps these requests apart from
// a 503 that a caller received.
func (s *apiServer) hungUp(r *http.Request, route apiRoute, id string, start time.Time) bool {
	if r.Context().Err() == nil {
		return false
	}
	s.readings.recordRequest(route.template, r.Method, statusClientClosed, s.now().Sub(start))
	s.logged(r, route, id, "the client closed the connection", statusClientClosed, 0, start)
	return true
}

// refuse writes the fault as a problem document, without a body for a
// HEAD request. The document's instance field and the log line have
// the same request id.
func (s *apiServer) refuse(w http.ResponseWriter, r *http.Request, route apiRoute,
	id string, start time.Time, head bool, f *fault) {
	writeFault(w, f, r.URL.Path, id, head)
	s.readings.recordRequest(route.template, r.Method, f.status, s.now().Sub(start))
	s.logged(r, route, id, f.logged(), f.status, 0, start)
}

// logged writes the log line of a request. It never includes the
// token.
func (s *apiServer) logged(r *http.Request, route apiRoute, id, detail string, status, bytes int, start time.Time) {
	slog.Info("Served a request",
		"route", route.template,
		"method", r.Method,
		"status", status,
		"bytes", bytes,
		"seconds", s.now().Sub(start).Seconds(),
		"request", id,
		"subject", subjectOf(r),
		"detail", detail)
}

// subjectOf returns the caller's username from the request context.
// It is empty when the request was refused before authenticate.
func subjectOf(r *http.Request) string {
	if who, held := r.Context().Value(subjectKey{}).(string); held {
		return who
	}
	return ""
}

type subjectKey struct{}

// reach returns the sidecar pod on the Display's node. It returns a
// 503 when the node has no sidecar pod, or when the pod has no IP
// address or is not Ready, because the caller can only wait.
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
