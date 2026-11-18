package main

// Every error of this API is an RFC 9457 problem document, served as
// application/problem+json, so a client parses one format for every
// failure. The detail member has the text of the underlying error.

import (
	"encoding/json"
	"net/http"
)

// Each problem type is the URL of a manual page that says what the
// problem means and how to clear it. The display-operator and
// audio-operator capture APIs use the same URLs under
// https://liken.sh/problems/ for the problems they share with this API,
// so a problem that this API relays from one of them has a type that
// the client already handles.
const (
	problemNoNode         = "https://liken.sh/problems/no-node"
	problemNotAcceptable  = "https://liken.sh/problems/not-acceptable"
	problemCaptureBusy    = "https://liken.sh/problems/capture-busy"
	problemUpstreamFailed = "https://liken.sh/problems/upstream-failed"
	problemNotPlaying     = "https://liken.sh/problems/not-playing"
	problemAway           = "https://liken.sh/problems/away"
)

const problemContentType = "application/problem+json"

// acceptableForm is one entry of the acceptable member of a 406: a
// media type the client may request, and the route that serves it, so
// the client does not have to build the path.
type acceptableForm struct {
	Type string `json:"type"`
	Href string `json:"href"`
}

// problemDocument has the members that RFC 9457 defines, and two
// extension members, which section 3.2 allows and a client that does
// not know them ignores. upstream is the URL of the other operator's
// API that returned a relayed problem, and acceptable lists the forms
// that a 406 offers.
type problemDocument struct {
	Type       string           `json:"type"`
	Title      string           `json:"title"`
	Status     int              `json:"status"`
	Detail     string           `json:"detail,omitempty"`
	Instance   string           `json:"instance"`
	Upstream   string           `json:"upstream,omitempty"`
	Acceptable []acceptableForm `json:"acceptable,omitempty"`
}

// newProblem builds a document. When the type is about:blank, section
// 4.2.1 says the title should be the HTTP status phrase, so newProblem
// ignores the title argument. Callers pass the request path, #, and the
// request id as the instance. The request's log line has the same id,
// so a client that reports a problem points to the log line that
// explains it.
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

// writeProblem writes the document. For a HEAD request it writes only
// the status and the Content-Type, because RFC 9110 says a response to
// HEAD has no content, for an error as for a success.
func writeProblem(w http.ResponseWriter, head bool, document problemDocument) {
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(document.Status)
	if head {
		return
	}
	_ = json.NewEncoder(w).Encode(document)
}
