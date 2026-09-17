package main

// This file writes the Link fields, RFC 8288. They are this API's map
// of itself: a client that holds one response finds the OpenAPI
// document, the manual, the Player on the API server, and the routes
// the response relates to, with no path built by hand. Each field
// goes out as its own line on the wire rather than one folded list,
// so a client that reads one field value at a time reads each link
// whole.

import (
	"fmt"
	"net/http"
	"strings"
)

// The relations this API sends. Five are registered with IANA: related
// (RFC 4287) from a redirect to the composed form and from the info
// document to each capture route, alternate on the extensionless
// media route, describedby to the Player on the API server, and
// service-desc and service-doc (RFC 8631). The other two are
// extension relations, which section 2.1.2 allows as a URI that
// identifies the relation on its own. They stay unregistered because
// registration needs a specification and expert review, and the URI
// already identifies the relation.
const (
	relRelated     = "related"
	relAlternate   = "alternate"
	relDescribedBy = "describedby"
	relServiceDesc = "service-desc"
	relServiceDoc  = "service-doc"
	relScreen      = "https://liken.sh/rel/screen"
	relAudio       = "https://liken.sh/rel/audio"
)

// The two documents RFC 8631 names: service-desc, a machine-readable
// description, and service-doc, the documentation a person reads. The
// OpenAPI target is a path, because it resolves against this server
// and the served copy names the same origin. The manual's target is a
// URL, because the manual is not served here.
const (
	serviceDescTarget = "/v1/media/openapi.json"
	serviceDocTarget  = "https://media.liken.sh/docs/reference/api/"
)

// linkField renders one field value: the target in angle brackets,
// then rel, then type when the caller gives one. The type parameter
// carries a bare media type with no parameters, section 3.4.1, so a
// link to media.mp4 says video/mp4 and never the codecs, which are
// determined while generating the content.
func linkField(target, relation, mediaType string) string {
	field := fmt.Sprintf("<%s>; rel=%q", target, relation)
	if mediaType != "" {
		field += fmt.Sprintf("; type=%q", mediaType)
	}
	return field
}

// addLink adds one Link field line and never folds it into an
// existing one, so every link is a line of its own.
func addLink(header http.Header, target, relation, mediaType string) {
	header.Add("Link", linkField(target, relation, mediaType))
}

// addServiceLinks adds the two links every response carries, so a
// client that holds any one response, an error included, finds the
// whole API.
func addServiceLinks(header http.Header) {
	addLink(header, serviceDescTarget, relServiceDesc, "")
	addLink(header, serviceDocTarget, relServiceDoc, "")
}

// describedByTarget is the Player on the API server, as an absolute
// URL. A relative reference would resolve against this server, which
// does not serve the Player.
func describedByTarget(namespace, name string) string {
	return strings.Join([]string{
		"https://kubernetes.default.svc/apis",
		mediaAPIVersion,
		"namespaces", namespace,
		"players", name,
	}, "/")
}
