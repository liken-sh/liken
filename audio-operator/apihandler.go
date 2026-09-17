package main

// The order every request goes through.
//
// The route is matched first, so the metric and the log line carry a
// template rather than a path. The method is checked before anything
// reads, because a 405 costs nothing. The caller is authenticated and
// then authorized, and only then is the object read, so a 403 never
// reveals that a name exists.
//
// Each request writes one log line: the route template, the request
// id, the caller's username, the resource, the status, the bytes, the
// header time, and the stream time. It never carries the token.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ServeHTTP answers one request.
func (s *apiServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	at := s.now()
	id := requestID()
	route, name, found := matchRoute(r.URL.Path)
	if !found {
		// A path outside the table names no resource, so the answer
		// carries the two service links and nothing else.
		writeAnswerHeaders(w, apiRoute{}, "")
		writeProblem(w, r.Method, newProblem(problemBlank, http.StatusNotFound,
			fmt.Sprintf("this API serves no route at %s", r.URL.Path),
			instanceOf(r.URL.Path, id)))
		s.readings.answered("", r.Method, http.StatusNotFound, s.now().Sub(at))
		return
	}

	result := s.answer(w, r, route, name, id, at)
	headers := s.now().Sub(at) - result.Streamed
	s.readings.answered(route.Template, r.Method, result.Status, headers)
	fmt.Printf("%s: %s route=%s id=%s user=%s resource=%s status=%d bytes=%d headers=%.3f stream=%.3f\n",
		DriverName, apiComponent, route.Template, id, callerName(result.Who),
		resourceOf(route, name), result.Status, result.Sent,
		headers.Seconds(), result.Streamed.Seconds())
}

// answered is what one request produced, which is what the log line
// and the two metrics report.
type answered struct {
	Status   int
	Sent     int64
	Streamed time.Duration
	Who      caller
}

func refused(status int) answered { return answered{Status: status} }

// answer does the work and reports what was sent, so ServeHTTP writes
// one log line and one metric for every path through this file. The
// negotiation and the query come after the authorization, so a 406
// and a 400 reveal no more than a 403 does.
func (s *apiServer) answer(w http.ResponseWriter, r *http.Request, route apiRoute,
	name, id string, at time.Time) answered {
	if !answersMethod(r.Method) {
		w.Header().Set("Allow", allowHeader())
		return s.refuse(w, r, route, name, http.StatusMethodNotAllowed, problemBlank, id,
			fmt.Sprintf("%s is not one of the methods this route answers", r.Method))
	}
	if r.Method == http.MethodOptions {
		writeAnswerHeaders(w, route, name)
		w.Header().Set("Allow", allowHeader())
		w.WriteHeader(http.StatusNoContent)
		return refused(http.StatusNoContent)
	}

	who, status, challenged, detail := s.authenticate(r)
	if status != 0 {
		if challenged != "" {
			w.Header().Set("WWW-Authenticate", challenged)
		}
		if status == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", retryAfterSeconds)
		}
		return s.refuse(w, r, route, name, status, problemBlank, id, detail)
	}

	allowed, reason, err := s.access.authorize(who, route, name)
	if err != nil {
		w.Header().Set("Retry-After", retryAfterSeconds)
		return s.refuse(w, r, route, name, http.StatusServiceUnavailable, problemBlank, id, err.Error())
	}
	if !allowed {
		w.Header().Set("WWW-Authenticate", insufficientScopeChallenge(scopeOf(route)))
		return s.refuse(w, r, route, name, http.StatusForbidden, problemBlank, id, reason)
	}

	form, acceptable := negotiate(route, r.Header.Get("Accept"))
	if !acceptable {
		document := newProblem(problemNotAcceptable, http.StatusNotAcceptable,
			fmt.Sprintf("this route serves none of %q", r.Header.Get("Accept")),
			instanceOf(r.URL.Path, id))
		document.Acceptable = route.acceptable(name)
		writeAnswerHeaders(w, route, name)
		writeProblem(w, r.Method, document)
		return answered{Status: http.StatusNotAcceptable, Who: who}
	}

	knobs, err := parseKnobs(route, r.URL.RawQuery)
	if err != nil {
		return s.refuse(w, r, route, name, http.StatusBadRequest, problemBlank, id, err.Error())
	}

	if route.Kind == routeDiscovery || route.Kind == routeOpenAPI {
		return s.serveDocument(w, r, route, name, form, who)
	}
	return s.serveEndpoint(w, r, route, name, form, knobs, who, id, at)
}

// authenticate reviews the caller's token. It answers the caller, or
// the status and the challenge a refusal carries. A review the API
// server did not answer is a 503, not a 401: the token was never
// judged.
func (s *apiServer) authenticate(r *http.Request) (caller, int, string, string) {
	token, found := bearerToken(r.Header.Get("Authorization"))
	if !found {
		return caller{}, http.StatusUnauthorized, challenge(),
			"this API takes a ServiceAccount token with the audience " + apiAudience
	}
	who, err := s.review.review(token)
	if errors.Is(err, ErrTokenDenied) {
		return caller{}, http.StatusUnauthorized, invalidTokenChallenge(err.Error()), err.Error()
	}
	if err != nil {
		return caller{}, http.StatusServiceUnavailable, "", err.Error()
	}
	return who, 0, "", ""
}

// refuse writes one problem document and reports what the log line
// says about it.
func (s *apiServer) refuse(w http.ResponseWriter, r *http.Request, route apiRoute,
	name string, status int, kind, id, detail string) answered {
	writeAnswerHeaders(w, route, name)
	writeProblem(w, r.Method, newProblem(kind, status, detail, instanceOf(r.URL.Path, id)))
	return refused(status)
}

// serveDocument answers the discovery and OpenAPI routes, which carry
// an ETag and answer If-None-Match with a 304.
func (s *apiServer) serveDocument(w http.ResponseWriter, r *http.Request,
	route apiRoute, name string, form representation, who caller) answered {
	writeDocumentHeaders(w, route, name, form, s.buildVersion())
	if matchesETag(r.Header.Get("If-None-Match"), documentETag(s.buildVersion())) {
		// A 304 carries the ETag and Vary and no body (RFC 9110
		// section 15.4.5).
		w.WriteHeader(http.StatusNotModified)
		return answered{Status: http.StatusNotModified, Who: who}
	}
	if route.Kind == routeDiscovery {
		writeJSON(w, r.Method, http.StatusOK, newDiscovery())
		return answered{Status: http.StatusOK, Who: who}
	}
	writeJSON(w, r.Method, http.StatusOK, newOpenAPI(s.origin(r)))
	return answered{Status: http.StatusOK, Who: who}
}

// origin is what the served OpenAPI document names in servers: the
// configured public base, else the request's own origin.
func (s *apiServer) origin(r *http.Request) string {
	if s.publicBase != "" {
		return s.publicBase
	}
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

func (s *apiServer) buildVersion() string { return version }

// matchesETag reads an If-None-Match field. A star matches anything
// the resource has (RFC 9110 section 13.1.2).
func matchesETag(header, tag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == "*" || candidate == tag {
			return true
		}
	}
	return false
}

// resourceOf is what the log line names as the resource: the kind and
// the name, or nothing on a document route.
func resourceOf(route apiRoute, name string) string {
	if route.Resource == "" {
		return "-"
	}
	return route.Resource + "/" + name
}

// callerName is the username the log line carries. A request that
// never got past the challenge has none.
func callerName(who caller) string {
	if who.Username == "" {
		return "-"
	}
	return who.Username
}
