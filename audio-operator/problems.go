package main

// The capture API returns an RFC 9457 problem document for every
// error. The API mode and the capture container mode of the binary
// write the same shape, so the API passes the capture container's
// document to the caller with only its instance replaced. The detail
// member holds the failing source's own text word for word, as the
// section "Errors include their source's text" of AGENTS.md requires:
// pw-record's stderr, an encoder's stderr, or the API server's
// status.message.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// A problem type is a URL that names the kind of failure, not the
// status code, so one status can have several types. The types under
// problemBase mean the same in the display and media capture APIs, so a
// client handles each one the same way whichever API returned it. A
// type that only audio returns is under problemDomainBase.
const (
	problemBase       = "https://liken.sh/problems/"
	problemDomainBase = "https://liken.sh/audio/problems/"

	problemNoNode         = problemBase + "no-node"
	problemNotAcceptable  = problemBase + "not-acceptable"
	problemCaptureBusy    = problemBase + "capture-busy"
	problemUpstreamFailed = problemBase + "upstream-failed"
	problemAway           = problemBase + "away"

	problemWrongTarget = problemDomainBase + "wrong-target"

	// RFC 9457 section 4.2.1 defines about:blank as the type of a
	// problem with no type of its own. Its title is the HTTP status
	// phrase.
	problemBlank = "about:blank"
)

// acceptableType is one entry of the acceptable member of a 406
// document: a representation that the route serves, and the URI that
// serves it.
type acceptableType struct {
	Type string `json:"type"`
	Href string `json:"href"`
}

// problem is the RFC 9457 document. Instance is the request path, a
// "#", and the request ID. The API writes one log line per request
// with the same ID, so a reader can match a client's response to that
// line.
type problem struct {
	Type       string           `json:"type"`
	Title      string           `json:"title"`
	Status     int              `json:"status"`
	Detail     string           `json:"detail,omitempty"`
	Instance   string           `json:"instance"`
	Acceptable []acceptableType `json:"acceptable,omitempty"`
}

func newProblem(kind string, status int, detail, instance string) problem {
	return problem{
		Type:     kind,
		Title:    problemTitle(kind, status),
		Status:   status,
		Detail:   strings.TrimSpace(detail),
		Instance: instance,
	}
}

// problemTitle returns the title of a problem: one fixed title for each
// type, and the status phrase for about:blank. A title is a short noun
// phrase and not a sentence, because a client may show it as a label
// beside the detail.
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

func instanceOf(path, requestID string) string {
	return path + "#" + requestID
}

// writeProblem sends a problem document. A response to HEAD has the
// headers and no body (RFC 9110 section 9.3.2), so the caller passes
// the request method. The document is small, so it is marshaled into
// memory first and sent with a Content-Length instead of chunked.
func writeProblem(w http.ResponseWriter, method string, document problem) {
	body, err := json.Marshal(document)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	body = append(body, '\n')
	w.Header().Set("Content-Type", problemType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if method == http.MethodHead {
		w.WriteHeader(document.Status)
		return
	}
	w.WriteHeader(document.Status)
	_, _ = w.Write(body)
}

// readProblem parses the body of a capture container's error response,
// and returns false when the body is not a problem document. When the
// API relays a capture container's error, it answers a body that is not
// a problem document with a 502 upstream-failed problem.
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

// writeJSON sends a JSON document with a Content-Length, and only the
// headers in a response to HEAD. The Content-Type is application/json
// unless the caller already set one.
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
