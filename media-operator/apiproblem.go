package main

// This file writes the one error form of this API: an RFC 9457
// problem document, application/problem+json, on every error. A
// client reads one shape for every failure, and the detail carries
// the source's own words, this repository's error rule. The type is a
// URI because RFC 9457 defines it so, and each type names a page in
// the manual, so a client that dereferences it reads what the problem
// means and what clears it.

import (
	"encoding/json"
	"net/http"
)

// The shared problem types. The three capture APIs spell them the same
// way under one host, so a client that relays a sibling's problem
// through this API sees a type it already handles. Each names a page
// in the manual.
const (
	problemNoNode         = "https://liken.sh/problems/no-node"
	problemNotAcceptable  = "https://liken.sh/problems/not-acceptable"
	problemCaptureBusy    = "https://liken.sh/problems/capture-busy"
	problemUpstreamFailed = "https://liken.sh/problems/upstream-failed"
	problemNotPlaying     = "https://liken.sh/problems/not-playing"
	problemAway           = "https://liken.sh/problems/away"
)

// problemContentType is the media type RFC 9457 registers for these
// bodies.
const problemContentType = "application/problem+json"

// acceptableForm is one entry of a 406's acceptable member: a type
// the client may ask for, and the route that serves it, so the client
// need not build the path.
type acceptableForm struct {
	Type string `json:"type"`
	Href string `json:"href"`
}

// problemDocument carries the five members RFC 9457 defines: type,
// title, status, detail, and instance. It adds two extension members,
// which section 3.2 allows and which a client that does not know them
// ignores: upstream, the URL of the sibling whose answer a relayed
// problem carries, and acceptable, the forms a 406 offers.
type problemDocument struct {
	Type       string           `json:"type"`
	Title      string           `json:"title"`
	Status     int              `json:"status"`
	Detail     string           `json:"detail,omitempty"`
	Instance   string           `json:"instance"`
	Upstream   string           `json:"upstream,omitempty"`
	Acceptable []acceptableForm `json:"acceptable,omitempty"`
}

// newProblem builds a document. With the type about:blank, the title
// is the status phrase, per section 4.2.1, whatever title the caller
// gave. The instance is the request path plus # plus the request id,
// and the log line carries the same id, so a client that reports a
// problem names the log line that explains it.
func newProblem(kind string, status int, title, detail, instance string) problemDocument {
	if kind == aboutBlank {
		title = http.StatusText(status)
	}
	return problemDocument{
		Type:     kind,
		Title:    title,
		Status:   status,
		Detail:   detail,
		Instance: instance,
	}
}

// writeProblem sends the document. A HEAD stops at the status and the
// Content-Type and sends no body, because a HEAD response carries no
// content, RFC 9110 section 15.5, on an error as on a success.
func writeProblem(w http.ResponseWriter, head bool, document problemDocument) {
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(document.Status)
	if head {
		return
	}
	_ = json.NewEncoder(w).Encode(document)
}
