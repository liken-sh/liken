package main

// This file holds the one route table of the API. The router matches
// requests against it, the discovery document lists it, and the
// OpenAPI document renders from it. One table keeps the three in
// agreement: a route the router serves is a route the documents
// describe, and a route the documents describe is one the router
// serves, with no second list to fall out of step.
//
// The domain segment of every path is the first label of the CRD's
// API group, display.liken.sh, so a path names the same domain the
// Kubernetes object does.

import (
	"fmt"
	"strings"
)

// The path grammar every liken capture API shares is
// /v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}].
// The domain segment lets one ingress mount every domain's API under
// one host name with no path clash, and this API is one Service per
// domain until that ingress exists.
const (
	apiDomain      = "display"
	apiRoot        = "/v1/" + apiDomain
	displaysPlural = "displays"
	screenAspect   = "screen"
)

// Every liken capture API serves two documents beside its captures:
// the discovery document and the OpenAPI document. The OpenAPI type
// application/openapi+json is provisional
// (draft-ietf-httpapi-rest-api-mediatypes) and not yet registered.
const (
	jsonMediaType    = "application/json"
	openAPIMediaType = "application/openapi+json"
)

// The four representations of a screen. Negotiation resolves every
// tie and every wildcard in this order, so image/png is the answer
// to no Accept at all and to Accept: image/*.
var screenForms = []struct {
	ext       string
	mediaType string
}{
	{"png", "image/png"},
	{"jpg", "image/jpeg"},
	{"mp4", "video/mp4"},
	{"mjpeg", "multipart/x-mixed-replace"},
}

// multipart/x-mixed-replace is registered with IANA (W3C, 2014) with
// boundary as a required parameter, and no RFC defines it. ffmpeg's
// mpjpeg muxer writes the parts with the boundary "ffmpeg", so the
// Content-Type states that one.
const mjpegContentType = "multipart/x-mixed-replace; boundary=ffmpeg"

// The RFC 6570 template the discovery document publishes for the
// screen aspect. Form-style expansion percent-encodes commas and
// colons, so a client that expands it sends t=5%2C7, and the query
// parser decodes that after it splits.
const screenTemplate = apiRoot + "/" + displaysPlural +
	"/{name}/" + screenAspect + "{.ext}{?t,xywh,width,height,framerate,quality}"

// The three kinds of route, each with its own conduct. A document
// route carries an ETag and Cache-Control: no-cache and answers
// If-None-Match with 304. An info route is a document about one
// Display and needs get on it. A capture route streams a live
// frame and carries Cache-Control: no-store, because no cache may
// answer for a screen.
type routeKind int

const (
	documentRoute routeKind = iota
	infoRoute
	captureRoute
)

// One route. The template is the RFC 6570 form of the path, and it
// is the route label on every metric and every log line, never the
// concrete path. mediaType is the one type a fixed form serves, and
// it is empty on the negotiated route.
type apiRoute struct {
	template  string
	kind      routeKind
	mediaType string
	ext       string
	aspect    string
	// The route's two labels in the OpenAPI document: what the route
	// is, and what its 200 carries.
	summary string
	answer  string
}

// This table is the whole surface of the API. The router, the
// discovery document, and the OpenAPI document each read it, and
// none of them holds a list of its own.
var apiRoutes = []apiRoute{
	{
		template:  apiRoot,
		kind:      documentRoute,
		mediaType: jsonMediaType,
		summary:   "The discovery document",
		answer:    "The routes this API serves, as RFC 6570 templates.",
	},
	{
		template:  apiRoot + "/openapi.json",
		kind:      documentRoute,
		mediaType: openAPIMediaType,
		summary:   "The OpenAPI 3.1 description of this API",
		answer:    "This document.",
	},
	{
		template:  apiRoot + "/" + displaysPlural + "/{name}",
		kind:      infoRoute,
		mediaType: jsonMediaType,
		summary:   "The screen's size, scale, refresh and formats",
		answer:    "The size, scale, refresh, formats and clip codecs of one screen, read from the node. A screen whose compositor is not serving, or whose node this API cannot reach, answers the name, the node and the mode from the Display object, with compositor or sidecar naming what is wrong and the rest left out.",
	},
	{
		template: apiRoot + "/" + displaysPlural + "/{name}/" + screenAspect,
		kind:     captureRoute,
		aspect:   screenAspect,
		summary:  "The screen in the type Accept selects",
		answer:   "The screen in the type Accept selected, image/png by default.",
	},
	{
		template:  apiRoot + "/" + displaysPlural + "/{name}/" + screenAspect + ".png",
		kind:      captureRoute,
		mediaType: "image/png",
		ext:       "png",
		aspect:    screenAspect,
		summary:   "One frame of the screen as PNG",
		answer:    "One frame of the screen, encoded as PNG.",
	},
	{
		template:  apiRoot + "/" + displaysPlural + "/{name}/" + screenAspect + ".jpg",
		kind:      captureRoute,
		mediaType: "image/jpeg",
		ext:       "jpg",
		aspect:    screenAspect,
		summary:   "One frame of the screen as JPEG",
		answer:    "One frame of the screen, encoded as JPEG.",
	},
	{
		template:  apiRoot + "/" + displaysPlural + "/{name}/" + screenAspect + ".mp4",
		kind:      captureRoute,
		mediaType: "video/mp4",
		ext:       "mp4",
		aspect:    screenAspect,
		summary:   "A clip of the screen as H.264 in fragmented MP4",
		answer:    "A clip of the screen, H.264 in fragmented MP4, until the t= end or the client hangs up. The Content-Type carries the codecs parameter RFC 6381 defines: avc1.640029, High profile at level 4.1, up to 1920x1080 at 60 fps, and avc1.640033, level 5.1, above that.",
	},
	{
		template:  apiRoot + "/" + displaysPlural + "/{name}/" + screenAspect + ".mjpeg",
		kind:      captureRoute,
		mediaType: "multipart/x-mixed-replace",
		ext:       "mjpeg",
		aspect:    screenAspect,
		summary:   "A stream of the screen, one JPEG per frame",
		answer:    "A stream of the screen, one image/jpeg part per frame, until the t= end or the client hangs up.",
	},
}

// A still is one frame, so it takes no t= end, no framerate, and
// none of a clip's other knobs.
func stillForm(mediaType string) bool {
	return mediaType == "image/png" || mediaType == "image/jpeg"
}

// The extension form of one media type. Content-Location and the
// alternate links name a screen by this form.
func formExtension(mediaType string) string {
	for _, form := range screenForms {
		if form.mediaType == mediaType {
			return form.ext
		}
	}
	return ""
}

// The Content-Type field a form is served under. The multipart form
// carries its boundary; the rest are the type alone.
func contentTypeOf(mediaType string) string {
	if mediaType == "multipart/x-mixed-replace" {
		return mjpegContentType
	}
	return mediaType
}

// The Content-Type of a capture, which for a clip names the codec
// the body carries. RFC 6381 writes an H.264 codec as avc1 and three
// bytes: the profile, the constraint flags, and the level. This
// encoder pins High profile with no constraint flags, so a clip up
// to 1920x1080 at 60 fps reads avc1.640029 and anything larger
// avc1.640033. A client that has to choose a decoder before the
// first byte, and media-api composing this stream with sound, both
// read it from here.
func captureContentType(mediaType string, width, height, framerate int) string {
	if mediaType != "video/mp4" {
		return contentTypeOf(mediaType)
	}
	return fmt.Sprintf(`video/mp4; codecs="%s"`, h264Codecs(width, height, framerate))
}

// The levels this encoder pins, and the sizes each one covers.
// Level 4.1 is 0x29 and carries 1920x1080 at 60 fps; level 5.1 is
// 0x33 and carries 4096x2160 at 60. High profile is 0x64 and the
// constraint byte is 0x00.
func h264Codecs(width, height, framerate int) string {
	if width > 1920 || height > 1080 || framerate > 60 {
		return "avc1.640033"
	}
	return "avc1.640029"
}

// The two options that pin the profile and the level the codecs
// parameter states. Without them h264_vaapi picks a level from the
// stream and a caller cannot be told the string before the encode.
func h264Profile(width, height, framerate int) []string {
	level := "4.1"
	if h264Codecs(width, height, framerate) == "avc1.640033" {
		level = "5.1"
	}
	return []string{"-profile:v", "high", "-level", level}
}

// The types the screen aspect offers, in the order negotiation
// resolves ties and wildcards in.
func screenMediaTypes() []string {
	types := make([]string, 0, len(screenForms))
	for _, form := range screenForms {
		types = append(types, form.mediaType)
	}
	return types
}

// The 406 document lists every form of the screen aspect with the
// absolute path that serves it, the "representation characteristics
// and corresponding resource identifiers" RFC 9110 section 15.5.7
// asks for.
func screenAcceptable(name string) []acceptableForm {
	forms := make([]acceptableForm, 0, len(screenForms))
	for _, form := range screenForms {
		forms = append(forms, acceptableForm{
			Type: form.mediaType,
			Href: capturePath(name, form.ext),
		})
	}
	return forms
}

// The absolute path of one extension form. A Link that carries it
// resolves against the API's own origin, per RFC 8288 section 3.1.
func capturePath(name, ext string) string {
	return apiRoot + "/" + displaysPlural + "/" + name + "/" + screenAspect + "." + ext
}

// A match reads the path alone, so a request for a path outside the
// table is a 404 and never a 405. The method is judged after the
// route is known.
func matchRoute(path string) (apiRoute, string, bool) {
	for _, route := range apiRoutes {
		name, ok := matchTemplate(route.template, path)
		if ok {
			return route, name, true
		}
	}
	return apiRoute{}, "", false
}

// The one variable in every template is the resource's name. A
// segment-by-segment comparison means a name with a slash in it
// matches no route.
func matchTemplate(template, path string) (string, bool) {
	want := strings.Split(strings.TrimPrefix(template, "/"), "/")
	have := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(want) != len(have) {
		return "", false
	}
	name := ""
	for i, segment := range want {
		if segment == "{name}" {
			if have[i] == "" {
				return "", false
			}
			name = have[i]
			continue
		}
		if segment != have[i] {
			return "", false
		}
	}
	return name, true
}

// The route the discovery document names as self for the displays
// resource. It is the info route's template, and a describedby link
// points at the Kubernetes object instead, never at this route.
func displaySelfTemplate() string {
	return apiRoot + "/" + displaysPlural + "/{name}"
}
