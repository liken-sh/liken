package main

// The headers every answer carries.
//
// There are two classes of answer. A document route answers with an
// ETag, which is the build version because the router table is
// compiled in, and Cache-Control: no-cache, so a client that sends
// If-None-Match gets a 304. A tap answers with Cache-Control:
// no-store and Accept-Ranges: none, because a live capture has no
// byte identity: offset 44 of two requests is two different moments
// (RFC 9110 section 14.3).
//
// Vary: Accept is on every response, extension routes included.
// Section 12.5.5's second purpose is to tell the recipient the
// response was subject to negotiation, and Accept can turn either
// route's 200 into a 406.
//
// Content-Location is on the negotiated route only. Section 8.7 calls
// it "a more specific identifier for the selected representation".
// Section 8.7's identity guarantee, that a GET on that URI would
// return the same representation, does not hold for a live capture
// and is not claimed.

import (
	"net/http"
	"strings"
	"time"
)

// serviceDescription and serviceDocument are the two service links
// RFC 8631 registers, which every response in the three capture APIs
// carries: the OpenAPI document, and the manual's page for the API.
const (
	serviceDescription = apiPrefix + "/openapi.json"
	serviceDocument    = apiHost + "/docs/reference/api/"
)

// linkHeader builds the whole Link field value for one answer. The
// relations go out as one field line with comma-separated values,
// which RFC 8288 allows. Only registered relations are used; an
// extension relation would go under https://liken.sh/rel/.
func linkHeader(route apiRoute, name string, _ representation) string {
	links := []string{
		"<" + serviceDescription + ">; rel=\"service-desc\"",
		"<" + serviceDocument + ">; rel=\"service-doc\"",
	}
	if route.Resource != "" && name != "" {
		links = append(links, "<"+objectURI(route.Resource, name)+">; rel=\"describedby\"")
	}
	switch {
	case route.negotiated():
		// The extensionless route names each fixed form it can serve.
		for _, form := range audioRepresentations {
			links = append(links, "<"+route.extensionPath(name, form.Extension)+
				">; rel=\"alternate\"; type=\""+form.MediaType+"\"")
		}
	case route.Kind == routeInfo:
		// RFC 4287's related, registered, from the document that
		// describes an endpoint to the routes that tap it.
		for _, form := range audioRepresentations {
			links = append(links, "<"+apiPrefix+"/"+route.Resource+"/"+name+"/"+audioAspect+
				"."+form.Extension+">; rel=\"related\"; type=\""+form.MediaType+"\"")
		}
	}
	return strings.Join(links, ", ")
}

// contentDisposition names a browser's save (RFC 6266). The colons in
// the time become dashes, because a colon is not legal in a file name
// everywhere a browser saves, and RFC 6266 section 4.3 is advice to
// recipients rather than a rule the sender can rely on. This is the
// one place in the API where the time is not in the standard RFC 3339
// form.
func contentDisposition(name, extension string, at time.Time) string {
	stamp := strings.ReplaceAll(at.UTC().Format(time.RFC3339), ":", "-")
	return `inline; filename="` + name + "-" + stamp + "." + extension + `"`
}

// writeAnswerHeaders sets the two fields every answer in this API
// carries, errors included: Vary, because Accept could have turned this
// answer into a 406, and the Link relations, because a client that got
// a refusal still needs the way to the description of what it asked
// for.
func writeAnswerHeaders(w http.ResponseWriter, route apiRoute, name string) {
	header := w.Header()
	header.Set("Vary", "Accept")
	header.Set("Link", linkHeader(route, name, representation{}))
}

// writeDocumentHeaders sets what a discovery, OpenAPI, or info answer
// carries. version is the build the binary was made from, which is the
// whole of a document's identity: the router table is compiled in, so
// two builds of one commit answer alike.
func writeDocumentHeaders(w http.ResponseWriter, route apiRoute, name string,
	form representation, version string) {
	writeAnswerHeaders(w, route, name)
	header := w.Header()
	header.Set("Content-Type", form.ContentType)
	header.Set("Cache-Control", "no-cache")
	header.Set("ETag", documentETag(version))
}

// documentETag is the build version as a strong entity tag.
func documentETag(version string) string {
	return `"` + version + `"`
}

// writeTapHeaders sets what a capture answer carries. at is the accept
// instant, which is both the save name's time and the span's zero.
func writeTapHeaders(w http.ResponseWriter, route apiRoute, name string,
	form representation, at time.Time) {
	writeAnswerHeaders(w, route, name)
	header := w.Header()
	header.Set("Content-Type", form.ContentType)
	header.Set("Cache-Control", "no-store")
	header.Set("Accept-Ranges", "none")
	header.Set("Content-Disposition", contentDisposition(name, form.Extension, at))
	if route.negotiated() {
		header.Set("Content-Location", route.extensionPath(name, form.Extension))
	}
}
