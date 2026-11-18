package main

// Every route reports an error as an RFC 9457 problem document. A
// route returns a fault, which holds the status, the problem type, the
// detail, and the response fields that the status requires. Only
// writeFault writes a response from a fault, so no route sets an error
// status itself.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
)

// The display, audio, and media capture APIs share some problem types,
// which are under liken.sh/problems, so a client handles a type the
// same way whichever API returned it. The types that only this API
// returns are under the display manual's section of liken.sh.
const (
	sharedProblemBase  = "https://liken.sh/problems/"
	displayProblemBase = "https://liken.sh/display/problems/"
)

// capture-denied, compositor-down, and encoder-failed are under the
// display base because only this API has a compositor and an encoder.
const (
	problemBlank          = "about:blank"
	problemNoNode         = sharedProblemBase + "no-node"
	problemNotAcceptable  = sharedProblemBase + "not-acceptable"
	problemCaptureBusy    = sharedProblemBase + "capture-busy"
	problemUpstreamFailed = sharedProblemBase + "upstream-failed"
	problemAway           = sharedProblemBase + "away"
	problemCaptureDenied  = displayProblemBase + "capture-denied"
	problemCompositorDown = displayProblemBase + "compositor-down"
	problemEncoderFailed  = displayProblemBase + "encoder-failed"
)

// problemTypes lists every type this API returns, for the enumeration
// in the OpenAPI document.
func problemTypes() []string {
	return []string{
		problemBlank,
		problemNoNode,
		problemNotAcceptable,
		problemCaptureBusy,
		problemUpstreamFailed,
		problemAway,
		problemCaptureDenied,
		problemCompositorDown,
		problemEncoderFailed,
	}
}

// The media type that RFC 9457 registers. Every error response uses
// it, so a client tells a problem document from a capture by the
// Content-Type alone.
const problemMediaType = "application/problem+json"

// The scope that a 403 names beside error="insufficient_scope" in its
// WWW-Authenticate field (RFC 6750 section 3).
const captureScope = "displays/screen"

// The API process uses this realm, and the capture sidecar replaces it
// with display-capture at startup, so a 401 shows which process refused
// the request.
var authenticateRealm = `Bearer realm="display-api"`

// One entry of the acceptable member of a 406 document. RFC 9110
// section 15.5.7 says a 406 response should list the available
// representations and where to get each one, and RFC 9457 section 3.2
// allows a problem document to carry that list as an extension member.
type acceptableForm struct {
	Type string `json:"type"`
	Href string `json:"href"`
}

// The members that RFC 9457 defines, plus acceptable on a 406.
type problemDocument struct {
	Type       string           `json:"type"`
	Title      string           `json:"title"`
	Status     int              `json:"status"`
	Detail     string           `json:"detail,omitempty"`
	Instance   string           `json:"instance"`
	Acceptable []acceptableForm `json:"acceptable,omitempty"`
}

type fault struct {
	status     int
	kind       string
	title      string
	detail     string
	acceptable []acceptableForm
	// The text for the log line, when it differs from detail. An error
	// that names an address, a port, or a path inside the cluster would
	// show the cluster's layout to a caller who is only allowed to read
	// screens, so the response gets a shorter detail and the log gets
	// the full text.
	log string
	// The header fields that RFC 9110 requires with some statuses:
	// Allow on a 405, Retry-After on a 503, and WWW-Authenticate on a
	// 401 and a 403.
	headers [][2]string
}

func (f *fault) Error() string {
	return fmt.Sprintf("%d %s: %s", f.status, f.title, f.detail)
}

func (f *fault) logged() string {
	if f.log != "" {
		return f.log
	}
	return f.detail
}

// newFault builds a fault. A kind with no title of its own, which
// includes about:blank, gets the HTTP status phrase as its title (RFC
// 9457 section 4.2.1).
func newFault(status int, kind, detail string) *fault {
	return &fault{status: status, kind: kind, title: faultTitle(kind, status), detail: detail}
}

// The title of each typed problem. A title describes the condition in
// general. The detail member describes the request that hit it.
var problemTitles = map[string]string{
	problemNoNode:         "The screen cannot be reached",
	problemNotAcceptable:  "No representation of this screen is acceptable",
	problemCaptureBusy:    "The output is already being captured",
	problemUpstreamFailed: "The capture sidecar returned an error",
	problemAway:           "The screen is away",
	problemCaptureDenied:  "The compositor denied the capture",
	problemCompositorDown: "The compositor is not serving this screen",
	problemEncoderFailed:  "The encoder produced no frame",
}

func faultTitle(kind string, status int) string {
	if title, named := problemTitles[kind]; named {
		return title
	}
	return http.StatusText(status)
}

// Each constructor below takes the text of the failure as its detail,
// such as the query that the parser rejected, the TokenReview's error,
// or the sidecar's problem document, so a caller can read the cause
// from the response.
func badRequest(detail string) *fault {
	return newFault(http.StatusBadRequest, problemBlank, detail)
}

const noTokenDetail = "the request has no Authorization header with a Bearer token"

func unauthenticated(detail string) *fault {
	// RFC 6750 section 3 defines two WWW-Authenticate forms for a 401.
	// A request with no token gets the challenge alone. A rejected
	// token also gets error="invalid_token" and the review's error
	// text.
	if detail == "" {
		f := newFault(http.StatusUnauthorized, problemBlank, noTokenDetail)
		f.headers = [][2]string{{"WWW-Authenticate", authenticateRealm}}
		return f
	}
	f := newFault(http.StatusUnauthorized, problemBlank, detail)
	f.headers = [][2]string{{"WWW-Authenticate", fmt.Sprintf(
		`%s, error="invalid_token", error_description=%q`, authenticateRealm, detail)}}
	return f
}

func unauthorized(detail string) *fault {
	f := newFault(http.StatusForbidden, problemBlank, detail)
	f.headers = [][2]string{{"WWW-Authenticate", fmt.Sprintf(
		`%s, error="insufficient_scope", scope=%q`, authenticateRealm, captureScope)}}
	return f
}

func notFound(detail string) *fault {
	return newFault(http.StatusNotFound, problemBlank, detail)
}

func methodNotAllowed(detail string) *fault {
	f := newFault(http.StatusMethodNotAllowed, problemBlank, detail)
	f.headers = [][2]string{{"Allow", allowedMethods}}
	return f
}

func notAcceptable(detail string, forms []acceptableForm) *fault {
	f := newFault(http.StatusNotAcceptable, problemNotAcceptable, detail)
	f.acceptable = forms
	return f
}

func captureDenied(detail string) *fault {
	return newFault(http.StatusInternalServerError, problemCaptureDenied, detail)
}

func upstreamFailed(detail string) *fault {
	return newFault(http.StatusBadGateway, problemUpstreamFailed, detail)
}

func unavailable(kind, detail string) *fault {
	f := newFault(http.StatusServiceUnavailable, kind, detail)
	f.headers = [][2]string{{"Retry-After", retryAfterSeconds}}
	return f
}

// away is the fault for a Display whose monitor is not connected. It
// is a 409 (RFC 9110 section 15.5.10). It is not a 404, because the
// Display exists, and not a 503, because a retry fails until someone
// connects the monitor. The audio API returns the same type for a Sink
// or a Source with no node, and the media API passes it through on a
// composed stream.
func away(detail string) *fault {
	return newFault(http.StatusConflict, problemAway, detail)
}

func gatewayTimeout(detail string) *fault {
	return newFault(http.StatusGatewayTimeout, problemUpstreamFailed, detail)
}

// The Retry-After value, in seconds, of every 503. The audio and media
// APIs send the same value, so a client needs one wait for all of them.
const retryAfterSeconds = "5"

// The methods that every route accepts. No route reads a request
// body, so a POST gets a 405 with this Allow value, and the API never
// returns a 415.
const allowedMethods = "GET, HEAD, OPTIONS"

// newRequestID returns eight random hex characters. The id is in the
// instance member of the problem document and in the request's log
// line, so a caller can report the id and a person can find the line.
func newRequestID() string {
	var raw [4]byte
	// When the read fails, the id is all zeros, which still identifies
	// the request in the log.
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

func problemInstance(path, id string) string {
	return path + "#" + id
}

// writeFault writes the response for a fault. A HEAD request gets the
// status and the header fields but no body (RFC 9110 section 9.3.2).
func writeFault(w http.ResponseWriter, f *fault, path, id string, head bool) {
	w.Header().Set("Vary", "Accept")
	serviceLinks(w)
	for _, field := range f.headers {
		w.Header().Set(field[0], field[1])
	}
	w.Header().Set("Content-Type", problemMediaType)
	w.WriteHeader(f.status)
	if head {
		return
	}
	body, err := json.Marshal(problemDocument{
		Type:       f.kind,
		Title:      f.title,
		Status:     f.status,
		Detail:     f.detail,
		Instance:   problemInstance(path, id),
		Acceptable: f.acceptable,
	})
	if err != nil {
		return
	}
	_, _ = w.Write(append(body, '\n'))
}

// serviceLinks adds the RFC 8631 service-desc and service-doc links,
// which point to the OpenAPI document and the manual, to a response.
// Every response of this API has them, errors included.
func serviceLinks(w http.ResponseWriter) {
	w.Header().Add("Link", fmt.Sprintf(`<%s/openapi.json>; rel="service-desc"`, apiRoot))
	w.Header().Add("Link", fmt.Sprintf(`<%s>; rel="service-doc"`, serviceDocument))
}
