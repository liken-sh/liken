package main

// This file routes the media capture API. Every path has the form
// /v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}], as
// the display and audio capture APIs do. The domain segment ("media"
// here) lets one ingress serve every domain's API on one host name.
//
// The routes are a table of templates, not a pattern mux, so each
// metric and log line can use the matched template as its route label.
// routeTemplates in apiopenapi.go builds the OpenAPI paths from the
// same captureAspects, and TestTheOpenAPIDocumentNamesEveryRoute fails
// when the two lists differ.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

const (
	apiBasePath       = "/v1/media"
	apiPlayerTemplate = apiBasePath + "/namespaces/{namespace}/players/{name}"
	healthTemplate    = "/healthz"
	readyTemplate     = "/readyz"
)

const apiAllowedMethods = "GET, HEAD, OPTIONS"

const apiRealm = "media-api"

// aboutBlank is the problem type for an error that has no documentation
// page. RFC 9457 section 4.2.1 makes its title the HTTP status phrase.
const aboutBlank = "about:blank"

type apiRoute struct {
	template string
	handle   func(*apiExchange)
}

// apiExchange is the state of one request. Handlers set the log fields
// as they go, and finish writes one log line when the request ends, so
// every route logs the same fields.
type apiExchange struct {
	server  *apiServer
	writer  http.ResponseWriter
	request *http.Request

	route     string
	namespace string
	name      string
	id        string
	head      bool
	started   time.Time

	subject   string
	status    int
	bytes     int64
	headerAt  time.Time
	upstreams []string
	offset    float64
	ffmpeg    string
}

// newRequestID returns a random id, because a counter would repeat
// after a restart and across replicas, and the id must pick out one
// request in the log and in a problem document's instance.
func newRequestID() string {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(raw)
}

// routes returns the route table. Each file extension has its own row,
// not a wildcard, so an extension that no form defines is a 404.
func (s *apiServer) routes() []apiRoute {
	routes := []apiRoute{
		{template: apiBasePath, handle: s.serveDiscovery},
		{template: apiBasePath + "/openapi.json", handle: s.serveOpenAPI},
		{template: apiPlayerTemplate, handle: s.serveInfo},
	}
	for _, aspect := range captureAspects() {
		routes = append(routes, apiRoute{
			template: apiPlayerTemplate + "/" + aspect.name,
			handle:   s.negotiatedHandler(aspect),
		})
		for _, form := range aspect.forms {
			routes = append(routes, apiRoute{
				template: apiPlayerTemplate + "/" + aspect.name + "." + form.Extension,
				handle:   s.fixedHandler(aspect, form),
			})
		}
	}
	return routes
}

func (s *apiServer) negotiatedHandler(aspect captureAspect) func(*apiExchange) {
	return func(e *apiExchange) { s.serveCapture(e, aspect, mediaForm{}, true) }
}

func (s *apiServer) fixedHandler(aspect captureAspect, form mediaForm) func(*apiExchange) {
	return func(e *apiExchange) { s.serveCapture(e, aspect, form, false) }
}

// matchTemplate returns the {namespace} and {name} segments of a path
// that matches template. An empty segment never matches, so a doubled
// slash or a missing name is a 404.
func matchTemplate(template, path string) (namespace, name string, ok bool) {
	wanted := strings.Split(strings.TrimPrefix(template, "/"), "/")
	got := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(wanted) != len(got) {
		return "", "", false
	}
	for index, segment := range wanted {
		switch segment {
		case "{namespace}":
			namespace = got[index]
		case "{name}":
			name = got[index]
		default:
			if segment != got[index] {
				return "", "", false
			}
		}
		if got[index] == "" {
			return "", "", false
		}
	}
	return namespace, name, true
}

// handler serves the probe paths first, with no authentication and no
// links. Every other response has Vary: Accept, on an extension route
// too, because RFC 9110 section 12.5.5 uses Vary to list the request
// headers that might have influenced the response. A path that matches
// no template gets the route label "unmatched", so the metric label
// cannot grow with arbitrary request paths.
func (s *apiServer) handler() http.Handler {
	routes := s.routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e := &apiExchange{
			server:  s,
			writer:  w,
			request: r,
			id:      newRequestID(),
			head:    r.Method == http.MethodHead,
			started: s.clock(),
		}
		defer e.finish()

		if r.URL.Path == healthTemplate || r.URL.Path == readyTemplate {
			e.route = r.URL.Path
			if e.methodRefused() {
				return
			}
			s.serveHealth(e)
			return
		}
		w.Header().Set("Vary", "Accept")
		addServiceLinks(w.Header())
		for _, route := range routes {
			namespace, name, ok := matchTemplate(route.template, r.URL.Path)
			if !ok {
				continue
			}
			e.route, e.namespace, e.name = route.template, namespace, name
			e.addDescribedByLink()
			if e.methodRefused() {
				return
			}
			route.handle(e)
			return
		}
		e.route = "unmatched"
		e.fail(aboutBlank, http.StatusNotFound, "", "no route for "+r.URL.Path)
	})
}

// methodRefused writes the response for any method other than GET and
// HEAD, and returns true when it did.
func (e *apiExchange) methodRefused() bool {
	switch e.request.Method {
	case http.MethodGet, http.MethodHead:
		return false
	case http.MethodOptions:
		e.writer.Header().Set("Allow", apiAllowedMethods)
		e.writeStatus(http.StatusNoContent)
		return true
	default:
		e.writer.Header().Set("Allow", apiAllowedMethods)
		e.fail(aboutBlank, http.StatusMethodNotAllowed, "",
			e.request.Method+" is not one of "+apiAllowedMethods)
		return true
	}
}

// addDescribedByLink links a Player route to the Player's object in the
// Kubernetes API.
func (e *apiExchange) addDescribedByLink() {
	if e.name == "" {
		return
	}
	addLink(e.writer.Header(), describedByTarget(e.namespace, e.name), relDescribedBy, "")
}

// instance is the problem document's instance field. The log line has
// the same request id, so a client's error report can be matched to
// the operator's log.
func (e *apiExchange) instance() string {
	return e.request.URL.Path + "#" + e.id
}

func (e *apiExchange) aspectPath(aspect, extension string) string {
	path := apiBasePath + "/namespaces/" + e.namespace + "/players/" + e.name + "/" + aspect
	if extension == "" {
		return path
	}
	return path + "." + extension
}

// writeStatus writes the status line and records the status and its time
// for the log line and the request histogram.
func (e *apiExchange) writeStatus(status int) {
	e.status = status
	e.headerAt = e.server.clock()
	e.writer.WriteHeader(status)
}

// writeStreamingStatus writes the status line and flushes it at once.
// net/http otherwise holds the headers until the body fills its buffer,
// and a composed capture writes no body until the muxer's first
// fragment, so the client could not tell that the capture had started.
// When the ResponseWriter cannot flush, the status reaches the client
// with the first body bytes.
func (e *apiExchange) writeStreamingStatus(status int) {
	e.writeStatus(status)
	_ = http.NewResponseController(e.writer).Flush()
}

func (e *apiExchange) fail(kind string, status int, title, detail string) {
	e.writeProblemDocument(newProblem(kind, status, title, detail, e.instance()))
}

func (e *apiExchange) writeProblemDocument(document problemDocument) {
	e.status = document.Status
	e.headerAt = e.server.clock()
	writeProblem(e.writer, e.head, document)
}

// authenticate identifies the caller the way the Kubernetes API server
// does: by a client certificate that this listener verified, or else by
// a bearer token that a TokenReview accepts. The 401 answers follow
// RFC 6750 section 3, and a rejected token's error_description is the
// TokenReview's message.
func (e *apiExchange) authenticate() (captureSubject, bool) {
	if subject, named := certificateCaller(e.request.TLS); named {
		e.subject = subject.User
		return subject, true
	}
	token := bearerToken(e.request.Header.Get("Authorization"))
	if token == "" {
		e.writer.Header().Set("WWW-Authenticate", `Bearer realm="`+apiRealm+`"`)
		e.fail(aboutBlank, http.StatusUnauthorized, "", "no bearer token")
		return captureSubject{}, false
	}
	subject, err := e.server.auth.authenticate(token)
	var refused unauthenticatedError
	if errors.As(err, &refused) {
		e.writer.Header().Set("WWW-Authenticate",
			`Bearer realm="`+apiRealm+`", error="invalid_token", error_description="`+
				escapeQuotedString(refused.Message)+`"`)
		e.fail(aboutBlank, http.StatusUnauthorized, "", refused.Message)
		return captureSubject{}, false
	}
	if err != nil {
		e.unavailable(err.Error())
		return captureSubject{}, false
	}
	e.subject = subject.User
	return subject, true
}

// authorize authenticates the caller and checks with a
// SubjectAccessReview that the caller may get the subresource. It
// returns false when it has written an error response. Handlers call it
// before they read the Player, so a 403 does not reveal whether the
// Player exists.
func (e *apiExchange) authorize(subresource string) bool {
	subject, ok := e.authenticate()
	if !ok {
		return false
	}
	allowed, err := e.server.auth.authorize(subject, e.namespace, e.name, subresource)
	if err != nil {
		e.unavailable(err.Error())
		return false
	}
	if !allowed {
		scope := "players"
		if subresource != "" {
			scope += "/" + subresource
		}
		e.writer.Header().Set("WWW-Authenticate",
			`Bearer realm="`+apiRealm+`", error="insufficient_scope", scope="`+scope+`"`)
		// A SubjectAccessReview returns no message to pass on, so the
		// detail gives the missing permission and the object, which the
		// caller needs to ask for access.
		e.fail(aboutBlank, http.StatusForbidden, "",
			"not allowed to get "+scope+" on "+e.namespace+"/"+e.name)
		return false
	}
	return true
}

// unavailable responds when a review or a read against the Kubernetes
// API server failed. It is a 503 with Retry-After, not a 500, because
// the fault is not in this API and a retry can succeed once the API
// server is back.
func (e *apiExchange) unavailable(detail string) {
	e.writer.Header().Set("Retry-After", retryAfterSeconds)
	e.fail(aboutBlank, http.StatusServiceUnavailable, "", detail)
}

// bearerToken returns the token of an Authorization header of the form
// "Bearer <token>", or "". RFC 9110 section 11.1 makes the scheme name
// case-insensitive.
func bearerToken(authorization string) string {
	const scheme = "bearer "
	if len(authorization) < len(scheme) || !strings.EqualFold(authorization[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(authorization[len(scheme):])
}

// escapeQuotedString makes a message safe inside an HTTP quoted-string,
// so it cannot end the quoted value or inject a header.
func escapeQuotedString(words string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", " ", "\n", " ")
	return replacer.Replace(words)
}

// player reads the Player in the path. A missing Player is a 404, and
// any other error is a 503.
func (e *apiExchange) player() (*Player, bool) {
	player, err := GetPlayer(e.server.client, e.namespace, e.name)
	if errors.Is(err, apiclient.ErrNotFound) {
		e.fail(aboutBlank, http.StatusNotFound, "",
			"no Player "+e.namespace+"/"+e.name)
		return nil, false
	}
	if err != nil {
		e.unavailable(err.Error())
		return nil, false
	}
	return player, true
}
