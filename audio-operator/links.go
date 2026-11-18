package main

// This file sets the caching, negotiation, and Link headers of the API's
// responses.
//
// A document route (discovery, OpenAPI, info) responds with an ETag and
// Cache-Control: no-cache. The ETag is the build version, because the
// router table is compiled in, so two builds of one commit return the
// same documents, and a client that sends If-None-Match gets a 304. A
// tap responds with Cache-Control: no-store and
// Accept-Ranges: none, because a live capture has no byte identity:
// offset 44 of two requests is two different moments (RFC 9110 section
// 14.3).
//
// Every response has Vary: Accept, including the responses of extension
// routes and errors. Besides listing the fields that selected the
// representation, Vary tells the recipient that the response was
// subject to negotiation (RFC 9110 section 12.5.5), and Accept can turn
// the 200 of any route into a 406.
//
// Only the negotiated route has Content-Location, which names the
// extension route of the chosen format. RFC 9110 section 8.7 calls that
// "a more specific identifier for the selected representation". The
// same section also says that a GET on that URI would return the same
// representation, which is never true of a live capture, so a client
// cannot read Content-Location that way here.

import (
	"net/http"
	"strings"
	"time"
)

// serviceDescription and serviceDocument are the targets of the
// service-desc and service-doc relations that RFC 8631 registers. Every
// response of the capture APIs links to the OpenAPI document and to the
// manual's page for the API.
const (
	serviceDescription = apiPrefix + "/openapi.json"
	serviceDocument    = "https://liken.sh/audio/docs/reference/api/"
)

// linkHeader returns the Link field value for one response. It sends
// every relation in one field line, separated by commas, which RFC 8288
// allows. It uses registered relation types only. An extension relation
// would go under https://liken.sh/rel/.
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
		// The route without an extension links to each fixed-form route
		// that it can serve.
		for _, form := range audioRepresentations {
			links = append(links, "<"+route.extensionPath(name, form.Extension)+
				">; rel=\"alternate\"; type=\""+form.MediaType+"\"")
		}
	case route.Kind == routeInfo:
		// The info document of an endpoint links to the routes that tap
		// it, with the related relation that RFC 4287 registers.
		for _, form := range audioRepresentations {
			links = append(links, "<"+apiPrefix+"/"+route.Resource+"/"+name+"/"+audioAspect+
				"."+form.Extension+">; rel=\"related\"; type=\""+form.MediaType+"\"")
		}
	}
	return strings.Join(links, ", ")
}

// contentDisposition returns the Content-Disposition value that names
// the file a browser saves (RFC 6266). It replaces the colons of the
// time with dashes, because some file systems that a browser saves to
// do not allow a colon in a file name, and RFC 6266 section 4.3 only
// advises recipients to sanitize names.
func contentDisposition(name, extension string, at time.Time) string {
	stamp := strings.ReplaceAll(at.UTC().Format(time.RFC3339), ":", "-")
	return `inline; filename="` + name + "-" + stamp + "." + extension + `"`
}

// writeVaryAndLink sets Vary and Link on every response, errors
// included. A client that was refused still needs the link to the
// description of the route it requested.
func writeVaryAndLink(w http.ResponseWriter, route apiRoute, name string) {
	header := w.Header()
	header.Set("Vary", "Accept")
	header.Set("Link", linkHeader(route, name, representation{}))
}

// writeDocumentHeaders sets the headers of a discovery, OpenAPI, or info
// response. version is the build version of the binary, and the ETag.
func writeDocumentHeaders(w http.ResponseWriter, route apiRoute, name string,
	form representation, version string) {
	writeVaryAndLink(w, route, name)
	header := w.Header()
	header.Set("Content-Type", form.ContentType)
	header.Set("Cache-Control", "no-cache")
	header.Set("ETag", documentETag(version))
}

// documentETag is the build version as a strong entity tag.
func documentETag(version string) string {
	return `"` + version + `"`
}

// writeTapHeaders sets the headers of a capture response, except
// Content-Type and X-Content-Type-Options. serveTap sets those for a
// HEAD, and copyFlushing sets them when it writes the body. at is the
// time that the operator accepted the request, and the file name that
// a browser saves includes it.
func writeTapHeaders(w http.ResponseWriter, route apiRoute, name string,
	form representation, at time.Time) {
	writeVaryAndLink(w, route, name)
	header := w.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("Accept-Ranges", "none")
	header.Set("Content-Disposition", contentDisposition(name, form.Extension, at))
	if route.negotiated() {
		header.Set("Content-Location", route.extensionPath(name, form.Extension))
	}
}
