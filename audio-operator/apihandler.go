package main

// The handler matches the route first, so the metric and the log line
// record the route template and not the path. It authenticates and
// authorizes the caller before it reads any object, so a 403 does not
// reveal whether a name exists. Each request writes one log line, which
// never contains the token.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ServeHTTP implements http.Handler for the API.
func (s *apiServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	at := s.now()
	id := requestID()
	route, name, found := matchRoute(r.URL.Path)
	if !found {
		writeVaryAndLink(w, apiRoute{}, "")
		writeProblem(w, r.Method, newProblem(problemBlank, http.StatusNotFound,
			fmt.Sprintf("this API serves no route at %s", r.URL.Path),
			instanceOf(r.URL.Path, id)))
		s.readings.observeRequest("", r.Method, http.StatusNotFound, s.now().Sub(at))
		return
	}

	// The log line is written in a defer because serveTap ends a
	// capture stream that the capture container cut short with
	// panic(http.ErrAbortHandler), and that request must still be
	// logged. serveTap fills in outcome before it panics.
	outcome := &requestOutcome{}
	defer func() {
		headers := s.now().Sub(at) - outcome.Streamed
		s.readings.observeRequest(route.Template, r.Method, outcome.Status, headers)
		s.record(fmt.Sprintf(
			"%s: %s route=%s id=%s user=%s resource=%s status=%d bytes=%d "+
				"headers=%.3f stream=%.3f ended=%s",
			DriverName, apiComponent, route.Template, id, callerName(outcome.Who),
			resourceOf(route, name), outcome.Status, outcome.Sent,
			headers.Seconds(), outcome.Streamed.Seconds(), outcome.Ended))
	}()
	*outcome = s.handle(w, r, route, name, id, at, outcome)
}

// requestOutcome is what one request returned, for the log line and the
// metrics.
type requestOutcome struct {
	Status   int
	Sent     int64
	Streamed time.Duration
	Who      caller

	// Ended is "ok" when the body was written in full, and otherwise
	// the error that ended a capture stream.
	Ended string
}

func outcomeOf(status int) requestOutcome { return requestOutcome{Status: status, Ended: "ok"} }

// handle serves the request and returns its outcome, so ServeHTTP logs
// and counts each request once on every path. Content negotiation and
// query parsing come after authorization, so a 406 or a 400 reveals no
// more than a 403 does.
func (s *apiServer) handle(w http.ResponseWriter, r *http.Request, route apiRoute,
	name, id string, at time.Time, outcome *requestOutcome) requestOutcome {
	if !allowsMethod(r.Method) {
		w.Header().Set("Allow", allowHeader())
		return s.refuse(w, r, route, name, http.StatusMethodNotAllowed, problemBlank, id,
			fmt.Sprintf("%s is not one of the methods that this route allows: %s", r.Method, allowHeader()))
	}
	if r.Method == http.MethodOptions {
		writeVaryAndLink(w, route, name)
		w.Header().Set("Allow", allowHeader())
		w.WriteHeader(http.StatusNoContent)
		return outcomeOf(http.StatusNoContent)
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
		return s.refuseAs(w, r, route, name, who, http.StatusServiceUnavailable,
			problemBlank, id, err.Error())
	}
	if !allowed {
		w.Header().Set("WWW-Authenticate", insufficientScopeChallenge(scopeOf(route)))
		return s.refuseAs(w, r, route, name, who, http.StatusForbidden, problemBlank, id, reason)
	}

	form, acceptable := negotiate(route, r.Header.Get("Accept"))
	if !acceptable {
		document := newProblem(problemNotAcceptable, http.StatusNotAcceptable,
			fmt.Sprintf("this route serves none of %q", r.Header.Get("Accept")),
			instanceOf(r.URL.Path, id))
		document.Acceptable = route.acceptable(name)
		writeVaryAndLink(w, route, name)
		writeProblem(w, r.Method, document)
		return requestOutcome{Status: http.StatusNotAcceptable, Who: who, Ended: "ok"}
	}

	query, err := parseCaptureQuery(route, r.URL.RawQuery)
	if err != nil {
		return s.refuseAs(w, r, route, name, who, http.StatusBadRequest,
			problemBlank, id, err.Error())
	}

	if route.Kind == routeDiscovery || route.Kind == routeOpenAPI {
		return s.serveDocument(w, r, route, name, form, who)
	}
	return s.serveEndpoint(w, r, route, name, form, query, who, id, at, outcome)
}

// authenticate identifies the caller by a verified client certificate,
// and otherwise by a Bearer token, in the order that the Kubernetes API
// server uses.
//
// On a refusal it returns the status code, the WWW-Authenticate
// challenge, and the detail text. When the TokenReview does not
// complete, the status is 503 and not 401, because the API server did
// not review the token.
func (s *apiServer) authenticate(r *http.Request) (caller, int, string, string) {
	if who, found := certificateCaller(r.TLS); found {
		return who, 0, "", ""
	}
	token, found := bearerToken(r.Header.Get("Authorization"))
	if !found {
		return caller{}, http.StatusUnauthorized, challenge(),
			"this API accepts a ServiceAccount token with the audience " + apiAudience
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

func (s *apiServer) refuse(w http.ResponseWriter, r *http.Request, route apiRoute,
	name string, status int, kind, id, detail string) requestOutcome {
	writeVaryAndLink(w, route, name)
	writeProblem(w, r.Method, newProblem(kind, status, detail, instanceOf(r.URL.Path, id)))
	return outcomeOf(status)
}

// serveDocument serves the discovery and OpenAPI documents. Each has an
// ETag, and a request whose If-None-Match matches it gets a 304.
func (s *apiServer) serveDocument(w http.ResponseWriter, r *http.Request,
	route apiRoute, name string, form representation, who caller) requestOutcome {
	writeDocumentHeaders(w, route, name, form, s.buildVersion())
	if matchesETag(r.Header.Get("If-None-Match"), documentETag(s.buildVersion())) {
		// A 304 has the ETag and Vary headers and no body (RFC 9110
		// section 15.4.5).
		w.WriteHeader(http.StatusNotModified)
		return requestOutcome{Status: http.StatusNotModified, Who: who, Ended: "ok"}
	}
	if route.Kind == routeDiscovery {
		writeJSON(w, r.Method, http.StatusOK, newDiscovery())
		return requestOutcome{Status: http.StatusOK, Who: who, Ended: "ok"}
	}
	writeJSON(w, r.Method, http.StatusOK, newOpenAPI(s.origin(r)))
	return requestOutcome{Status: http.StatusOK, Who: who, Ended: "ok"}
}

// origin returns the base URL for the servers field of the OpenAPI
// document: PUBLIC_BASE when it is set, and otherwise the scheme and
// host of the request.
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

// matchesETag reports whether the If-None-Match header lists tag or *.
// It ignores a W/ prefix, because If-None-Match uses weak comparison
// (RFC 9110 section 13.1.2).
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

// resourceOf returns "-" for a document route, which has no resource.
func resourceOf(route apiRoute, name string) string {
	if route.Resource == "" {
		return "-"
	}
	return route.Resource + "/" + name
}

func (s *apiServer) record(line string) {
	if s.log == nil {
		fmt.Println(line)
		return
	}
	s.log(line)
}

// callerName returns "-" for a request that was refused before it was
// authenticated.
func callerName(who caller) string {
	if who.Username == "" {
		return "-"
	}
	return who.Username
}
