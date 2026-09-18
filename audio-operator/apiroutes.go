package main

// The one table that describes the capture API.
//
// The table is the single source of the router, the discovery
// document, and the OpenAPI document, so a route added here appears
// in all three. Both modes of the binary read it: the API matches a
// public request against it, and the capture container matches the
// private request the API forwards.
//
// The path grammar the three capture APIs share is
// /v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}].
// The domain segment is there so that one ingress can later mount
// every domain under one host name with no path clash, and so that a
// future video domain with sinks and sources of its own fits beside
// audio. Both kinds here are cluster-scoped, so no route of this
// domain carries the namespaces segment.

import (
	"slices"
	"strings"
)

// captureMode and apiMode are the two arguments that select the
// capture API's modes, beside declare and endpoints-registered. The
// capture container in the DaemonSet pod runs the first, and the
// audio-api Deployment runs the second.
const (
	captureMode = "capture"
	apiMode     = "api"
)

// apiDomain is the first label of the CRD's API group, so
// audio.liken.sh gives audio, and apiPrefix is the path every route of
// this domain starts with.
const (
	apiDomain = "audio"
	apiPrefix = "/v1/" + apiDomain
)

// audioAspect names the thing tapped. This domain taps one aspect of
// both its kinds.
const audioAspect = "audio"

// apiResources are the two plurals, spelled as the CRDs name them.
var apiResources = []string{"sinks", "sources"}

// apiHost is the site that serves this operator's manual, and the
// host that names this domain's own problem types.
const apiHost = "https://" + DriverName

// representation is one form the audio aspect takes.
//
// MediaType is the bare name that goes in the discovery document, in
// a Link header's type parameter, and in a 406's acceptable list: RFC
// 8288 section 3.4.1 takes no media type parameters, and the other
// two read as that one does. ContentType is what the response sends,
// which is the only place the codecs parameter belongs. Accepted is
// every spelling an Accept header may use for this one
// representation.
type representation struct {
	Extension   string
	MediaType   string
	ContentType string
	Accepted    []string

	// Answers names this form the way the manual's route table names
	// it, which is what the OpenAPI summary of each extension route
	// carries.
	Answers string
}

// audioRepresentations is the server's preference order, which is
// what breaks a tie at equal q.
//
// WAV: the IANA registry's only RIFF WAVE name is audio/vnd.wave from
// RFC 2361, which no client sends, and the WHATWG MIME Sniffing
// Standard names the RIFF signature audio/wave. audio/wav is what
// clients use, so it is the name sent, and the four spellings match
// as one representation. FLAC: audio/flac is RFC 9639 section 12.1,
// and audio/x-flac is its deprecated alias. Ogg Opus: RFC 5334
// registers audio/ogg with the codecs parameter, and RFC 7845 section
// 9 adds opus and the .opus extension.
var audioRepresentations = []representation{
	{
		Extension:   "wav",
		MediaType:   "audio/wav",
		ContentType: "audio/wav",
		Accepted:    []string{"audio/wav", "audio/wave", "audio/x-wav", "audio/vnd.wave"},
		Answers:     "PCM in a RIFF WAVE stream",
	},
	{
		Extension:   "flac",
		MediaType:   "audio/flac",
		ContentType: "audio/flac",
		Accepted:    []string{"audio/flac", "audio/x-flac"},
		Answers:     "FLAC",
	},
	{
		Extension:   "opus",
		MediaType:   "audio/ogg",
		ContentType: "audio/ogg; codecs=opus",
		Accepted:    []string{"audio/ogg"},
		Answers:     "Ogg Opus",
	},
}

// The types the document routes and the problem documents serve. The
// OpenAPI type is provisional: draft-ietf-httpapi-rest-api-mediatypes
// registers it, and it is not in the IANA registry until that draft
// publishes.
const (
	documentType = "application/json"
	openAPIType  = "application/openapi+json"
	problemType  = "application/problem+json"
)

// routeKind is what a route answers. A document carries an ETag and
// answers If-None-Match. An info route reads one object and carries
// an ETag too. A tap route produces bytes from the node's data plane.
type routeKind int

const (
	routeDiscovery routeKind = iota
	routeOpenAPI
	routeInfo
	routeTap
)

// queryParameter is one knob a route takes. Name is the query
// parameter. Extensions names the formats that take it, and an empty
// list means every format the route serves. Summary is the sentence
// the OpenAPI document publishes.
type queryParameter struct {
	Name       string
	Extensions []string
	Summary    string
}

// apiRoute is one row of the table. Template is the RFC 6570 form,
// which is also the route label on every metric and the route field
// in every log line, so no endpoint name reaches Prometheus.
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

	// Answers is what this route answers, in the manual's own words.
	// It is the summary the OpenAPI document publishes, so the
	// document and the route table say the same thing.
	Answers string
}

// apiMethods are the three methods every route answers, and no
// others. No route accepts content, so 415 never occurs.
var apiMethods = []string{"GET", "HEAD", "OPTIONS"}

// The knobs the tap routes take. t= is the W3C Media Fragments
// temporal dimension, on every format. bitrate= is this API's own
// knob, and only Opus takes it, so a bitrate on WAV or FLAC is a 400.
var (
	spanParameter = queryParameter{
		Name: "t",
		Summary: "The W3C Media Fragments temporal dimension in NPT: t=begin,end, " +
			"t=begin, or t=,end. The interval is half-open, and its zero is the " +
			"instant the capture container accepts the request. A begin over 60 " +
			"seconds is a 400.",
	}
	bitrateParameter = queryParameter{
		Name:       "bitrate",
		Extensions: []string{"opus"},
		Summary: "The Opus bitrate in kbit/s per channel, 6 to 256. opusenc chooses " +
			"one from the sample rate when this is absent. A bitrate on WAV or FLAC " +
			"is a 400.",
	}
)

// The problem types each kind of route can answer with, which is what
// the OpenAPI document publishes as each path item's error responses.
var (
	documentProblems = []string{problemNotAcceptable}
	infoProblems     = []string{problemNoNode, problemNotAcceptable, problemAway}
	tapProblems      = []string{
		problemNoNode, problemNotAcceptable, problemAway,
		problemCaptureBusy, problemUpstreamFailed, problemWrongTarget,
	}
)

// apiRoutes is the table. The rows are built rather than written out
// one by one, because the audio aspect takes the same shape on both
// kinds and a form that appeared on one and not the other would be a
// defect nobody could see in a literal list.
var apiRoutes = buildRoutes()

func buildRoutes() []apiRoute {
	routes := []apiRoute{
		{
			Template: apiPrefix,
			Kind:     routeDiscovery,
			Answers:  "The discovery document. It lists every resource this API serves and gives an RFC 6570 template for each aspect.",
			Methods:  apiMethods,
			Serves:   []representation{{MediaType: documentType, ContentType: documentType, Accepted: []string{documentType}}},
			Problems: documentProblems,
		},
		{
			Template: apiPrefix + "/openapi.json",
			Kind:     routeOpenAPI,
			Answers:  "The OpenAPI 3.1 document for this API.",
			Methods:  apiMethods,
			Serves:   []representation{{MediaType: openAPIType, ContentType: openAPIType, Accepted: []string{openAPIType, documentType}}},
			Problems: documentProblems,
		},
	}
	for _, resource := range apiResources {
		base := apiPrefix + "/" + resource + "/{name}"
		heard := "the audio the speakers play now"
		if resource == "sources" {
			heard = "the audio the microphone captures now"
		}
		routes = append(routes, apiRoute{
			Template: base,
			Kind:     routeInfo,
			Resource: resource,
			Answers: "The " + singular(resource) + " format and capture routes. It gives the target node, the rate and channel count for a capture, and the formats the routes serve.",
			Methods:  apiMethods,
			Serves:   []representation{{MediaType: documentType, ContentType: documentType, Accepted: []string{documentType}}},
			Problems: infoProblems,
		})
		routes = append(routes, apiRoute{
			Template: base + "/" + audioAspect,
			Kind:     routeTap,
			Resource: resource,
			Aspect:   audioAspect,
			Answers:  title(heard[:1]) + heard[1:] + ". The Accept header selects the format.",
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
				Answers:   title(heard[:1]) + heard[1:] + ", as " + form.Answers + ".",
				Methods:   apiMethods,
				Serves:    []representation{form},
				Query:     query,
				Problems:  tapProblems,
			})
		}
	}
	return routes
}

// negotiated says whether Accept chooses this route's representation.
// An extension names one fixed representation, so only the tap route
// with no extension negotiates. Vary: Accept is still sent on both,
// because Accept can turn either one into a 406.
func (r apiRoute) negotiated() bool {
	return r.Kind == routeTap && r.Extension == ""
}

// takes says whether this route accepts the named query parameter.
// Every other name in the query is a 400, because a query produces a
// new resource and a client that asked for one must not silently get
// another.
func (r apiRoute) takes(name string) bool {
	for _, knob := range r.Query {
		if knob.Name != name {
			continue
		}
		if len(knob.Extensions) == 0 {
			return true
		}
		if r.Extension == "" {
			return true
		}
		return slices.Contains(knob.Extensions, r.Extension)
	}
	return false
}

// acceptable is the list a 406 carries: every representation the
// aspect serves, with the URI that serves it (RFC 9110 section
// 15.5.7). On an extension route it lists that route's own type
// beside its siblings, so one answer names every way in.
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

// path fills the template's one variable.
func (r apiRoute) path(name string) string {
	return strings.ReplaceAll(r.Template, "{name}", name)
}

// extensionPath names the route that serves one fixed form of this
// route's aspect, which is what Content-Location carries on the
// negotiated route.
func (r apiRoute) extensionPath(name, extension string) string {
	return apiPrefix + "/" + r.Resource + "/" + name + "/" + r.Aspect + "." + extension
}

// matchRoute finds the row whose template the path fits, and the name
// the path carries. The match walks the table rather than using a
// ServeMux because the template is the metric label and the log
// field, so the match has to hand back the row it matched, and both
// modes of the binary need the same answer.
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

// aspectTemplate is the RFC 6570 template the discovery document
// publishes for one aspect: simple expansion for the name (section
// 3.2.2), label expansion for the extension (3.2.5), and form-style
// query expansion for the knobs (3.2.8). Form-style expansion
// percent-encodes the comma in t=5,7, and the parser's Media
// Fragments section 5.1.1 order accepts it.
func aspectTemplate(resource, aspect string) string {
	route, _, found := matchRoute(apiPrefix + "/" + resource + "/{name}/" + aspect)
	if !found {
		return ""
	}
	var knobs []string
	for _, knob := range route.Query {
		knobs = append(knobs, knob.Name)
	}
	return apiPrefix + "/" + resource + "/{name}/" + aspect + "{.ext}{?" + strings.Join(knobs, ",") + "}"
}

// objectURI names the Kubernetes object a response describes. The
// URI is absolute because a relative reference in a Link header
// resolves against this API's own origin, which is the wrong server
// (RFC 8288 section 3.1).
func objectURI(resource, name string) string {
	return "https://kubernetes.default.svc/apis/" + EndpointGroup + "/" + EndpointVersion +
		"/" + resource + "/" + name
}

// representationFor finds the form an extension names.
func representationFor(extension string) (representation, bool) {
	for _, form := range audioRepresentations {
		if form.Extension == extension {
			return form, true
		}
	}
	return representation{}, false
}
