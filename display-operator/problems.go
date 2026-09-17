package main

// This file holds RFC 9457 problem documents, the one error shape
// every route answers with. A fault carries the status, the problem
// type, the source's own words, and the response fields the status
// requires, so the one place that writes a refusal has everything it
// needs and no route writes a status of its own.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
)

// The problem types the three capture APIs share live under liken.sh,
// so a client handles one type the same way whichever API answered.
// A type only this domain answers lives under display.liken.sh.
const (
	sharedProblemBase  = "https://liken.sh/problems/"
	displayProblemBase = "https://display.liken.sh/problems/"
)

// The seven typed problems this API answers with. no-node,
// not-acceptable, capture-busy, and upstream-failed are shared with
// the audio and media APIs. capture-denied, compositor-down, and
// encoder-failed are display's own, because only this domain has a
// compositor and an encoder.
const (
	problemBlank          = "about:blank"
	problemNoNode         = sharedProblemBase + "no-node"
	problemNotAcceptable  = sharedProblemBase + "not-acceptable"
	problemCaptureBusy    = sharedProblemBase + "capture-busy"
	problemUpstreamFailed = sharedProblemBase + "upstream-failed"
	problemCaptureDenied  = displayProblemBase + "capture-denied"
	problemCompositorDown = displayProblemBase + "compositor-down"
	problemEncoderFailed  = displayProblemBase + "encoder-failed"
)

// Every type this API answers with, for the OpenAPI document's own
// enumeration of them. A reader of the description then meets the
// same list a caller meets.
func problemTypes() []string {
	return []string{
		problemBlank,
		problemNoNode,
		problemNotAcceptable,
		problemCaptureBusy,
		problemUpstreamFailed,
		problemCaptureDenied,
		problemCompositorDown,
		problemEncoderFailed,
	}
}

// The media type RFC 9457 registers. Every refusal is sent under it,
// so a client tells a problem document from a capture body by the
// Content-Type alone.
const problemMediaType = "application/problem+json"

// The realm every WWW-Authenticate field names, and the scope a 403
// names beside error="insufficient_scope", RFC 6750 section 3.
const captureScope = "displays/screen"

// The realm a challenge names is the process that answers it. The
// API keeps the default; the capture sidecar names itself at
// startup, so a 401 from either one says which door refused.
var authenticateRealm = `Bearer realm="display-api"`

// One entry of a 406 document's acceptable member: the
// "representation characteristics and corresponding resource
// identifiers" RFC 9110 section 15.5.7 asks for, as the extension
// member RFC 9457 section 3.2 allows.
type acceptableForm struct {
	Type string `json:"type"`
	Href string `json:"href"`
}

// The five members RFC 9457 defines, plus acceptable on a 406.
// instance is the request path plus the request id, and the log line
// for the request carries the same id.
type problemDocument struct {
	Type       string           `json:"type"`
	Title      string           `json:"title"`
	Status     int              `json:"status"`
	Detail     string           `json:"detail,omitempty"`
	Instance   string           `json:"instance"`
	Acceptable []acceptableForm `json:"acceptable,omitempty"`
}

// A fault is the answer a route refuses with, carried up to the one
// place that writes it.
type fault struct {
	status     int
	kind       string
	title      string
	detail     string
	acceptable []acceptableForm
	// What the log line says where the caller is told less. A
	// refusal that names an address, a port, or a path inside the
	// cluster tells a caller who may only read screens something
	// about the shape of the cluster, so the caller gets the fact and
	// the log gets the words.
	log string
	// The fields RFC 9110 requires beside certain status codes:
	// Allow on a 405, Retry-After on a 503, WWW-Authenticate on a
	// 401 and a 403.
	headers [][2]string
}

func (f *fault) Error() string {
	return fmt.Sprintf("%d %s: %s", f.status, f.title, f.detail)
}

// What this refusal writes to the log, which is the detail unless the
// fault carries words of its own.
func (f *fault) logged() string {
	if f.log != "" {
		return f.log
	}
	return f.detail
}

// A fault with no type of its own is about:blank, and its title is
// the status phrase (RFC 9457 section 4.2.1).
func newFault(status int, kind, detail string) *fault {
	return &fault{status: status, kind: kind, title: faultTitle(kind, status), detail: detail}
}

// The titles of the seven typed problems. A title names the condition
// and never the one request it happened to; detail carries that.
var problemTitles = map[string]string{
	problemNoNode:         "The screen cannot be reached",
	problemNotAcceptable:  "No representation of this screen is acceptable",
	problemCaptureBusy:    "The output is already being captured",
	problemUpstreamFailed: "The capture sidecar failed",
	problemCaptureDenied:  "The compositor denied the capture",
	problemCompositorDown: "The compositor is not serving this screen",
	problemEncoderFailed:  "The encoder wrote no picture",
}

func faultTitle(kind string, status int) string {
	if title, named := problemTitles[kind]; named {
		return title
	}
	return http.StatusText(status)
}

// The faults the router, the parser, and the private leg answer
// with. Each one's detail carries the source's own words: the query
// the parser refused, the TokenReview's error, the condition's
// message, or the sidecar's problem, so a person reads the cause from
// the answer and needs no log.
func badRequest(detail string) *fault {
	return newFault(http.StatusBadRequest, problemBlank, detail)
}

// The detail of a 401 that met no token at all. RFC 9457 asks every
// problem for one, and a caller who sent no field reads what the
// field is called and what it carries.
const noTokenDetail = "no Authorization field carries a Bearer token"

func unauthenticated(detail string) *fault {
	// RFC 6750 section 3 gives WWW-Authenticate two forms here: the
	// bare challenge for a request with no token, and invalid_token
	// with the review's own words for a token it refused.
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
	// Retry-After (RFC 9110 section 10.2.3) says how long to wait
	// before the client asks again. Every 503 in the three capture
	// APIs carries the same five seconds, so one client wait fits all
	// of them.
	f.headers = [][2]string{{"Retry-After", retryAfterSeconds}}
	return f
}

func gatewayTimeout(detail string) *fault {
	return newFault(http.StatusGatewayTimeout, problemUpstreamFailed, detail)
}

// The seconds every 503 in the three capture APIs asks the client to
// wait.
const retryAfterSeconds = "5"

// The three methods every route answers. No route takes a body, so a
// POST gets 405 with this field and 415 is never sent.
const allowedMethods = "GET, HEAD, OPTIONS"

// The request id is eight hex characters. It is in the problem's
// instance member and in the request's log line, so a caller's
// report names the line to read.
func newRequestID() string {
	var raw [4]byte
	// A failed read leaves the id all zeros, which still names the
	// request in the log.
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// instance is the request path, a number sign, and the request id,
// the same in all three capture APIs.
func problemInstance(path, id string) string {
	return path + "#" + id
}

// Writing a problem: first the fields every answer of this API
// carries, then the fields the fault carries, then the document. A
// HEAD gets the status and the fields and no body (RFC 9110 section
// 15.5).
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

// The two relations RFC 8631 defines, service-desc and service-doc,
// are on every answer this API writes, an error included. A client
// that met a refusal is the client that most needs the description
// and the manual.
func serviceLinks(w http.ResponseWriter) {
	w.Header().Add("Link", fmt.Sprintf(`<%s/openapi.json>; rel="service-desc"`, apiRoot))
	w.Header().Add("Link", fmt.Sprintf(`<%s>; rel="service-doc"`, serviceDocument))
}
