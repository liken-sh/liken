package main

// This file writes the Link header fields of RFC 8288. From one
// response, a client can find the OpenAPI document, the manual, the
// Player on the Kubernetes API server, and the related routes, without
// building any path itself. Each link is its own header field line,
// not part of one comma-separated list, so a client that reads one
// field value at a time gets each link whole.

import (
	"fmt"
	"net/http"
	"strings"
)

// related goes from a redirect to the composed form, and from the info
// document to each capture route. alternate goes on the extensionless
// media route. screen and audio point from a composed response to its
// upstreams. They are extension relation types, which RFC 8288 section
// 2.1.2 allows to be any URI, so they need no IANA registration.
const (
	relRelated     = "related"
	relAlternate   = "alternate"
	relDescribedBy = "describedby"
	relServiceDesc = "service-desc"
	relServiceDoc  = "service-doc"
	relScreen      = "https://liken.sh/rel/screen"
	relAudio       = "https://liken.sh/rel/audio"
)

// The OpenAPI target is a path, so it resolves against the server that
// sent the response, which serves the document. The manual target is a
// full URL because this server does not serve the manual.
const (
	serviceDescTarget = "/v1/media/openapi.json"
	serviceDocTarget  = "https://liken.sh/media/docs/reference/api/"
)

// linkField returns one Link field value: the target in angle
// brackets, then rel, then type when mediaType is not empty. The type
// parameter is a media type without parameters (RFC 8288 section
// 3.4.1), so a link to media.mp4 says video/mp4 with no codecs, which
// are known only after the content is generated.
func linkField(target, relation, mediaType string) string {
	field := fmt.Sprintf("<%s>; rel=%q", target, relation)
	if mediaType != "" {
		field += fmt.Sprintf("; type=%q", mediaType)
	}
	return field
}

func addLink(header http.Header, target, relation, mediaType string) {
	header.Add("Link", linkField(target, relation, mediaType))
}

// addServiceLinks adds the RFC 8631 links. The handler adds them to
// every response except the health and readiness checks, error
// answers included, so a client can find the API description from
// any response.
func addServiceLinks(header http.Header) {
	addLink(header, serviceDescTarget, relServiceDesc, "")
	addLink(header, serviceDocTarget, relServiceDoc, "")
}

// describedByTarget returns the URL of the Player on the Kubernetes
// API server. It is absolute because a relative reference would
// resolve against this server, which does not serve the Player.
func describedByTarget(namespace, name string) string {
	return strings.Join([]string{
		"https://kubernetes.default.svc/apis",
		mediaAPIVersion,
		"namespaces", namespace,
		"players", name,
	}, "/")
}
