package main

// This file is the router. The grammar is
// /v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}],
// shared by the three capture APIs. The domain segment, media here,
// lets one ingress later mount every domain's API under one host name
// with no path clash. The router is a table of templates rather than
// a pattern mux, because the same table is the discovery document,
// the OpenAPI document, and the route label of every metric and log
// line, and one list cannot drift from itself.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

// The base every route of this domain is under, the one template
// every Player route extends, and the two probe paths outside the
// grammar.
const (
	apiBasePath       = "/v1/media"
	apiPlayerTemplate = apiBasePath + "/namespaces/{namespace}/players/{name}"
	healthTemplate    = "/healthz"
	readyTemplate     = "/readyz"
)

// apiAllowedMethods is the three methods this API answers, as Allow
// states them.
const apiAllowedMethods = "GET, HEAD, OPTIONS"

// apiRealm is the realm every WWW-Authenticate field names.
const apiRealm = "media-api"

// aboutBlank is the problem type for an error that needs no page of
// its own. RFC 9457 section 4.2.1 makes the status phrase its title.
const aboutBlank = "about:blank"

// apiRoute is one row of the table: the template, which the metrics
// and the log line carry as the route, and the handler behind it.
type apiRoute struct {
	template string
	handle   func(*apiExchange)
}

// apiExchange holds one request for its length: the route it matched,
// the path variables, the request id, and the facts the log line
// carries. The handlers fill the facts as they learn them, and finish
// writes the one log line, so every route logs the same fields and
// no handler can forget one.
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

// newRequestID is four random bytes as eight hex characters. A
// counter would repeat after a restart and read the same in two pods,
// and the id must name one request in the log and in a problem
// document's instance.
func newRequestID() string {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(raw)
}

// routes is the table: the three document routes, then for each
// aspect the negotiated route and one row per form. Each extension is
// its own row rather than one row with a wildcard, so an extension no
// form names is a 404 and not a route that fails later, and the same
// rows are the OpenAPI path items.
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

// matchTemplate matches a path against one template segment by
// segment. A literal segment must be equal; {namespace} and {name}
// take the path's segment. An empty segment matches nothing, so a
// path with a doubled slash or a missing name is a 404.
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

// handler is the one entry point. It answers the two probe paths
// first, with no auth and no links. Every other path gets Vary and
// the service links, then the first template that matches, then the
// method rules every route shares. A path no row matches is a 404
// under the route unmatched, so the metric stays bounded.
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
			e.prepare()
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

// methodRefused answers a method other than GET and HEAD, the probe
// paths included: 204 with Allow for OPTIONS, and 405 with Allow and
// a problem document for the rest.
func (e *apiExchange) methodRefused() bool {
	switch e.request.Method {
	case http.MethodGet, http.MethodHead:
		return false
	case http.MethodOptions:
		e.writer.Header().Set("Allow", apiAllowedMethods)
		e.answer(http.StatusNoContent)
		return true
	default:
		e.writer.Header().Set("Allow", apiAllowedMethods)
		e.fail(aboutBlank, http.StatusMethodNotAllowed, "",
			e.request.Method+" is not one of "+apiAllowedMethods)
		return true
	}
}

// prepare adds the describedby link every Player route carries. Vary:
// Accept is set on every response, extension routes included, because
// RFC 9110 section 12.5.5 gives the field a second purpose: it tells
// a cache which request fields the server reads, whether or not they
// changed the answer.
func (e *apiExchange) prepare() {
	if e.name == "" {
		return
	}
	addLink(e.writer.Header(), describedByTarget(e.namespace, e.name), relDescribedBy, "")
}

// instance is the request path plus # plus the request id. The
// problem document carries it, and the log line carries the id, so a
// client and an operator name the same request.
func (e *apiExchange) instance() string {
	return e.request.URL.Path + "#" + e.id
}

// aspectPath builds a route of this Player, with or without an
// extension. Every Location, Link, and Content-Location goes through
// it, so the path is spelt one way.
func (e *apiExchange) aspectPath(aspect, extension string) string {
	path := apiBasePath + "/namespaces/" + e.namespace + "/players/" + e.name + "/" + aspect
	if extension == "" {
		return path
	}
	return path + "." + extension
}

// answer writes the status and records it with the instant the
// headers went out, the two facts the log line and the request
// histogram read.
func (e *apiExchange) answer(status int) {
	e.status = status
	e.headerAt = e.server.clock()
	e.writer.WriteHeader(status)
}

func (e *apiExchange) fail(kind string, status int, title, detail string) {
	e.answerProblem(newProblem(kind, status, title, detail, e.instance()))
}

func (e *apiExchange) answerProblem(document problemDocument) {
	e.status = document.Status
	e.headerAt = e.server.clock()
	writeProblem(e.writer, e.head, document)
}

// authenticate reviews the token before anything is read, so an
// unauthenticated request costs no read and is told nothing. The two
// refusals follow RFC 6750 section 3: no token gets the realm alone,
// and a token the review refused gets error="invalid_token" with the
// review's own words in error_description.
func (e *apiExchange) authenticate() (string, captureSubject, bool) {
	token := bearerToken(e.request.Header.Get("Authorization"))
	if token == "" {
		e.writer.Header().Set("WWW-Authenticate", `Bearer realm="`+apiRealm+`"`)
		e.fail(aboutBlank, http.StatusUnauthorized, "", "no bearer token")
		return "", captureSubject{}, false
	}
	subject, err := e.server.auth.authenticate(token)
	var refused unauthenticatedError
	if errors.As(err, &refused) {
		e.writer.Header().Set("WWW-Authenticate",
			`Bearer realm="`+apiRealm+`", error="invalid_token", error_description="`+
				quotedWords(refused.Words)+`"`)
		e.fail(aboutBlank, http.StatusUnauthorized, "", refused.Words)
		return "", captureSubject{}, false
	}
	if err != nil {
		e.unavailable(err.Error())
		return "", captureSubject{}, false
	}
	e.subject = subject.User
	return token, subject, true
}

// authorize runs the two reviews in order and answers whether the
// handler may go on. It runs before the Player is read, so a 403
// never says whether the name exists. A denial gets
// error="insufficient_scope" with the failed check in scope.
func (e *apiExchange) authorize(subresource string) bool {
	token, subject, ok := e.authenticate()
	if !ok {
		return false
	}
	allowed, err := e.server.auth.authorize(token, subject, e.namespace, e.name, subresource)
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
		// The detail names the grant the caller lacks and the object it
		// lacks it on, because a SubjectAccessReview carries no words
		// of its own to relay and a caller needs to know what to ask
		// an owner for.
		e.fail(aboutBlank, http.StatusForbidden, "",
			"not allowed to get "+scope+" on "+e.namespace+"/"+e.name)
		return false
	}
	return true
}

// unavailable answers an API server that will not answer a review or
// a read. It is a 503 with Retry-After and not a 500, because the
// fault is not in this API and a retry after the API server returns
// clears it.
func (e *apiExchange) unavailable(detail string) {
	e.writer.Header().Set("Retry-After", retryAfterSeconds)
	e.fail(aboutBlank, http.StatusServiceUnavailable, "", detail)
}

// bearerToken reads the token out of an Authorization field in the
// shape RFC 6750 fixes, the scheme Bearer then the token. The scheme
// is matched without regard to case, because RFC 9110 section 11.1
// makes authentication scheme names case-insensitive.
func bearerToken(authorization string) string {
	const scheme = "bearer "
	if len(authorization) < len(scheme) || !strings.EqualFold(authorization[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(authorization[len(scheme):])
}

// quotedWords escapes the review's words for a quoted-string: a
// backslash and a quotation mark are escaped, and a line break
// becomes a space, so the words cannot end the field or the header.
func quotedWords(words string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", " ", "\n", " ")
	return replacer.Replace(words)
}

// player reads the Player the path names. A Player that does not
// exist is a 404, and an API server that will not answer is a 503.
func (e *apiExchange) player() (*Player, bool) {
	player, err := GetPlayer(e.server.client, e.namespace, e.name)
	if errors.Is(err, ErrNotFound) {
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
