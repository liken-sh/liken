package main

// The route table of the capture API.
//
// The router, the discovery document, and the OpenAPI document are each
// built from this table, so a route added here appears in each of them.
// In API mode the binary matches a public request against the table,
// and in capture mode it matches the request that the API forwards.
//
// Paths follow /v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}],
// the same grammar as the display and media capture APIs. The domain
// segment lets one ingress mount every domain under one host name with
// no path clash. Sinks and sources are cluster-scoped, so no route here
// has a namespaces segment.

import (
	"slices"
	"strings"
)

// captureMode and apiMode are command-line modes of the binary. The
// capture container of the DaemonSet pod runs capture, and the audio-api
// Deployment runs api.
const (
	captureMode = "capture"
	apiMode     = "api"
)

// apiDomain is the first label of the CRD API group (audio.liken.sh),
// and apiPrefix is the start of every route path.
const (
	apiDomain = "audio"
	apiPrefix = "/v1/" + apiDomain
)

// audioAspect is the aspect segment of the tap routes, and the only
// aspect of a sink or a source.
const audioAspect = "audio"

var apiResources = []string{"sinks", "sources"}

// representation is one audio format that a tap route can return.
//
// MediaType has no parameters, because RFC 8288 section 3.4.1 allows
// none in a Link header, and the discovery document and the acceptable
// list of a 406 use the same value. Only ContentType carries the codecs
// parameter. Accepted lists every spelling of the format that an Accept
// header can use.
type representation struct {
	Extension   string
	MediaType   string
	ContentType string
	Accepted    []string

	// Summary is the format's name in the manual's route table, and the
	// OpenAPI summary of each extension route includes it.
	Summary string
}

// audioRepresentations is in the server's order of preference, which
// decides between formats that an Accept header ranks with equal q.
//
// The IANA registry names RIFF WAVE only as audio/vnd.wave (RFC 2361),
// which no client sends, and the WHATWG MIME Sniffing Standard calls it
// audio/wave. Clients send audio/wav, so the response uses that, and
// each of the spellings selects WAV. RFC 9639 section 12.1 registers
// audio/flac, with audio/x-flac as a deprecated alias. RFC 5334
// registers audio/ogg with a codecs parameter, and RFC 7845 section 9
// adds opus and the .opus extension.
var audioRepresentations = []representation{
	{
		Extension:   "wav",
		MediaType:   "audio/wav",
		ContentType: "audio/wav",
		Accepted:    []string{"audio/wav", "audio/wave", "audio/x-wav", "audio/vnd.wave"},
		Summary:     "PCM in a RIFF WAVE stream",
	},
	{
		Extension:   "flac",
		MediaType:   "audio/flac",
		ContentType: "audio/flac",
		Accepted:    []string{"audio/flac", "audio/x-flac"},
		Summary:     "FLAC",
	},
	{
		Extension:   "opus",
		MediaType:   "audio/ogg",
		ContentType: "audio/ogg; codecs=opus",
		Accepted:    []string{"audio/ogg"},
		Summary:     "Ogg Opus",
	},
}

// application/openapi+json is not in the IANA registry until
// draft-ietf-httpapi-rest-api-mediatypes, which registers it, is
// published.
const (
	documentType = "application/json"
	openAPIType  = "application/openapi+json"
	problemType  = "application/problem+json"
)

// routeKind is what a route returns. The discovery and OpenAPI routes
// return documents, with an ETag, and respond to If-None-Match. An info
// route returns one object, with an ETag. A tap route streams audio
// that the capture container on the sink's or source's node captures.
type routeKind int

const (
	routeDiscovery routeKind = iota
	routeOpenAPI
	routeInfo
	routeTap
)

// Extensions lists the formats that accept the parameter, and an empty
// list means every format. The OpenAPI document publishes Summary.
type queryParameter struct {
	Name       string
	Extensions []string
	Summary    string
}

// Template is the RFC 6570 form of the path. It is also the route label
// of every metric and the route field of every log line, so metric
// labels never contain a sink or source name.
type apiRoute struct {
	Template  string
	Kind      routeKind
	Resource  string
	Aspect    string
	Extension string
	Methods   []string
	Serves    []representation
	Query     []queryParameter
	Problems  []string

	// Summary describes what the route returns, in the wording of the
	// manual's route table. The OpenAPI document publishes it as the
	// summary.
	Summary string
}

// apiMethods are the methods every route serves. No route accepts a
// request body, so the API never returns 415.
var apiMethods = []string{"GET", "HEAD", "OPTIONS"}

// t= is the W3C Media Fragments temporal dimension, and every format
// accepts it. bitrate= belongs to this API, and only Opus accepts it.
var (
	spanParameter = queryParameter{
		Name: "t",
		Summary: "The part of the capture to return, as a W3C Media Fragments time in NPT: " +
			"`t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and " +
			"excludes `end`. Zero is the moment that the capture container accepts the " +
			"request. A `begin` over 60 seconds gets a 400.",
	}
	bitrateParameter = queryParameter{
		Name:       "bitrate",
		Extensions: []string{"opus"},
		Summary: "The Opus bitrate in kbit/s per channel, from 6 to 256. When the " +
			"request has no `bitrate`, `opusenc` chooses one from the sample rate. A " +
			"request for WAV or FLAC that has a `bitrate` gets a 400.",
	}
)

// The OpenAPI document publishes these problem types as the error
// responses of each kind of route.
var (
	documentProblems = []string{problemNotAcceptable}
	infoProblems     = []string{problemNoNode, problemNotAcceptable, problemAway}
	tapProblems      = []string{
		problemNoNode, problemNotAcceptable, problemAway,
		problemCaptureBusy, problemUpstreamFailed, problemWrongTarget,
	}
)

// buildRoutes makes the sink and source routes in one loop, so the two
// kinds always serve the same formats.
var apiRoutes = buildRoutes()

func buildRoutes() []apiRoute {
	routes := []apiRoute{
		{
			Template: apiPrefix,
			Kind:     routeDiscovery,
			Summary:  "The discovery document. It lists each resource that this API serves, with an RFC 6570 URI template for its tap routes.",
			Methods:  apiMethods,
			Serves:   []representation{{MediaType: documentType, ContentType: documentType, Accepted: []string{documentType}}},
			Problems: documentProblems,
		},
		{
			Template: apiPrefix + "/openapi.json",
			Kind:     routeOpenAPI,
			Summary:  "The OpenAPI 3.1 document that describes this API.",
			Methods:  apiMethods,
			Serves:   []representation{{MediaType: openAPIType, ContentType: openAPIType, Accepted: []string{openAPIType, documentType}}},
			Problems: documentProblems,
		},
	}
	for _, resource := range apiResources {
		base := apiPrefix + "/" + resource + "/{name}"
		capturedAudio := "the sound that the speakers of the `Sink` are playing now"
		if resource == "sources" {
			capturedAudio = "the sound that the microphone of the `Source` is capturing now"
		}
		routes = append(routes, apiRoute{
			Template: base,
			Kind:     routeInfo,
			Resource: resource,
			Summary:  "The info document of the `" + title(singular(resource)) + "`: the node that a tap runs on, the rate and channel count that a tap uses, and the formats that the tap routes return.",
			Methods:  apiMethods,
			Serves:   []representation{{MediaType: documentType, ContentType: documentType, Accepted: []string{documentType}}},
			Problems: infoProblems,
		})
		routes = append(routes, apiRoute{
			Template: base + "/" + audioAspect,
			Kind:     routeTap,
			Resource: resource,
			Aspect:   audioAspect,
			Summary:  "Streams " + capturedAudio + ", in the format that the `Accept` header selects. The default is WAV.",
			Methods:  apiMethods,
			Serves:   audioRepresentations,
			Query:    []queryParameter{spanParameter, bitrateParameter},
			Problems: tapProblems,
		})
		for _, form := range audioRepresentations {
			query := []queryParameter{spanParameter}
			if slices.Contains(bitrateParameter.Extensions, form.Extension) {
				query = append(query, bitrateParameter)
			}
			routes = append(routes, apiRoute{
				Template:  base + "/" + audioAspect + "." + form.Extension,
				Kind:      routeTap,
				Resource:  resource,
				Aspect:    audioAspect,
				Extension: form.Extension,
				Summary:   "Streams " + capturedAudio + ", as " + form.Summary + ".",
				Methods:   apiMethods,
				Serves:    []representation{form},
				Query:     query,
				Problems:  tapProblems,
			})
		}
	}
	return routes
}

// negotiated reports whether the Accept header picks the format. An
// extension route still sends Vary: Accept, because an Accept header
// can produce a 406 on it too.
func (r apiRoute) negotiated() bool {
	return r.Kind == routeTap && r.Extension == ""
}

// accepts reports whether the route accepts the named query parameter.
// The API refuses any other name with a 400, because a query produces a
// new resource, and ignoring a parameter would return a resource that
// the client did not ask for.
//
// The negotiated route accepts every parameter in its Query, because
// the format is not chosen yet. The API forwards the request to the
// capture container's extension route, which refuses a parameter that
// the chosen format does not accept.
func (r apiRoute) accepts(name string) bool {
	for _, parameter := range r.Query {
		if parameter.Name != name {
			continue
		}
		if len(parameter.Extensions) == 0 {
			return true
		}
		if r.Extension == "" {
			return true
		}
		return slices.Contains(parameter.Extensions, r.Extension)
	}
	return false
}

// acceptable returns the list of a 406 body (RFC 9110 section 15.5.7).
// A tap route lists every format of the aspect with its URI, extension
// routes included, so the client can pick another format from one
// response.
func (r apiRoute) acceptable(name string) []acceptableType {
	if r.Kind != routeTap {
		var only []acceptableType
		for _, form := range r.Serves {
			only = append(only, acceptableType{Type: form.MediaType, Href: r.path(name)})
		}
		return only
	}
	var forms []acceptableType
	for _, form := range audioRepresentations {
		forms = append(forms, acceptableType{
			Type: form.MediaType,
			Href: apiPrefix + "/" + r.Resource + "/" + name + "/" + r.Aspect + "." + form.Extension,
		})
	}
	return forms
}

func (r apiRoute) path(name string) string {
	return strings.ReplaceAll(r.Template, "{name}", name)
}

// The negotiated route sends extensionPath as Content-Location.
func (r apiRoute) extensionPath(name, extension string) string {
	return apiPrefix + "/" + r.Resource + "/" + name + "/" + r.Aspect + "." + extension
}

// matchRoute walks the table and does not use a ServeMux, because the
// caller needs the matched row. Its Template is the metric label and
// the log field, and API mode and capture mode must match a path to the
// same row.
func matchRoute(path string) (apiRoute, string, bool) {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, route := range apiRoutes {
		want := strings.Split(strings.TrimPrefix(route.Template, "/"), "/")
		if len(want) != len(segments) {
			continue
		}
		name := ""
		matched := true
		for index, segment := range want {
			if segment == "{name}" {
				if segments[index] == "" {
					matched = false
					break
				}
				name = segments[index]
				continue
			}
			if segment != segments[index] {
				matched = false
				break
			}
		}
		if matched {
			return route, name, true
		}
	}
	return apiRoute{}, "", false
}

// aspectTemplate returns the RFC 6570 template that the discovery
// document publishes for one aspect. It uses simple expansion for the
// name (section 3.2.2), label expansion for the extension (3.2.5), and
// form-style query expansion for the query parameters (3.2.8).
// Form-style expansion percent-encodes the comma in t=5,7, and the
// parse order of Media Fragments section 5.1.1 accepts that.
func aspectTemplate(resource, aspect string) string {
	route, _, found := matchRoute(apiPrefix + "/" + resource + "/{name}/" + aspect)
	if !found {
		return ""
	}
	var names []string
	for _, parameter := range route.Query {
		names = append(names, parameter.Name)
	}
	return apiPrefix + "/" + resource + "/{name}/" + aspect + "{.ext}{?" + strings.Join(names, ",") + "}"
}

// objectURI is absolute because a relative reference in a Link header
// resolves against this API's origin, and the object is on the
// Kubernetes API server (RFC 8288 section 3.1).
func objectURI(resource, name string) string {
	return "https://kubernetes.default.svc/apis/" + EndpointGroup + "/" + EndpointVersion +
		"/" + resource + "/" + name
}

func representationFor(extension string) (representation, bool) {
	for _, form := range audioRepresentations {
		if form.Extension == extension {
			return form, true
		}
	}
	return representation{}, false
}
