package main

// The RFC 9457 problem document every error in the capture API
// answers with. Both modes of the binary write the same shape, so the
// API relays the capture container's own document to the caller
// unchanged. detail carries the source's own words verbatim, by the
// rule in AGENTS.md: pw-record's stderr, an encoder's stderr, or the
// API server's status.message.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// The problem types. A type is a URL, and it names the kind of
// failure rather than the status code, so one status can carry more
// than one type. The first five are shared by the three capture APIs
// under liken's own host, and wrong-target is this domain's own under
// the audio host.
const (
	problemBase       = "https://liken.sh/problems/"
	problemDomainBase = apiHost + "/problems/"

	problemNoNode         = problemBase + "no-node"
	problemNotAcceptable  = problemBase + "not-acceptable"
	problemCaptureBusy    = problemBase + "capture-busy"
	problemUpstreamFailed = problemBase + "upstream-failed"
	problemAway           = problemBase + "away"

	problemWrongTarget = problemDomainBase + "wrong-target"

	// about:blank is what RFC 9457 section 4.2.1 names for a problem
	// with no type of its own, and its title is then the status
	// phrase.
	problemBlank = "about:blank"
)

// acceptableType is one entry of a 406's acceptable member: a
// representation the route serves, and the URI that serves it.
type acceptableType struct {
	Type string `json:"type"`
	Href string `json:"href"`
}

// problem is the document itself.
//
// Instance is the request path plus a number sign plus the request
// id. The one log line for the request carries the same id, so a
// reader joins the answer a client saw to the record the API kept.
type problem struct {
	Type       string           `json:"type"`
	Title      string           `json:"title"`
	Status     int              `json:"status"`
	Detail     string           `json:"detail,omitempty"`
	Instance   string           `json:"instance"`
	Acceptable []acceptableType `json:"acceptable,omitempty"`
}

// newProblem builds one document. A type of about:blank takes the
// status phrase as its title, which is what RFC 9457 section 4.2.1
// says it means.
func newProblem(kind string, status int, detail, instance string) problem {
	return problem{
		Type:     kind,
		Title:    problemTitle(kind, status),
		Status:   status,
		Detail:   strings.TrimSpace(detail),
		Instance: instance,
	}
}

// problemTitle names the failure. Each shared type has one title, and
// about:blank takes the status phrase. The titles are short noun
// phrases rather than sentences, because a title is a label a client
// may show beside the detail.
func problemTitle(kind string, status int) string {
	switch kind {
	case problemNoNode:
		return "No node"
	case problemNotAcceptable:
		return "Not acceptable"
	case problemCaptureBusy:
		return "Capture busy"
	case problemUpstreamFailed:
		return "Upstream failed"
	case problemAway:
		return "Away"
	case problemWrongTarget:
		return "Wrong target"
	default:
		return http.StatusText(status)
	}
}

// instanceOf is the instance member: the request's own path and the
// id the log line carries.
func instanceOf(path, requestID string) string {
	return path + "#" + requestID
}

// writeProblem sends one document. A HEAD carries the headers and no
// body (RFC 9110 section 15.5), so the caller says which it is. A
// problem is never chunked: it is one small document, encoded into
// memory first so its length goes out with the status line.
func writeProblem(w http.ResponseWriter, method string, document problem) {
	body, err := json.Marshal(document)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	body = append(body, '\n')
	w.Header().Set("Content-Type", problemType)
	if method == http.MethodHead {
		w.WriteHeader(document.Status)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(document.Status)
	_, _ = w.Write(body)
}

// readProblem reads a document the capture container answered with.
// An answer that is not a problem document is what the API turns into
// a 502, so the caller needs to know which it got.
func readProblem(body []byte) (problem, bool) {
	var document problem
	if err := json.Unmarshal(body, &document); err != nil {
		return problem{}, false
	}
	if document.Type == "" || document.Status == 0 {
		return problem{}, false
	}
	return document, true
}

// writeJSON sends one document with a length. Every document this API
// answers with is small and built in memory, so none of them is
// chunked, and a HEAD carries the headers alone (RFC 9110 section
// 9.3.2).
func writeJSON(w http.ResponseWriter, method string, status int, document any) {
	body, err := json.Marshal(document)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	body = append(body, '\n')
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", documentType)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if method == http.MethodHead {
		w.WriteHeader(status)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
