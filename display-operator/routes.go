package main

// apiRoutes is the route table of the API. The router matches
// requests against it, and the discovery document and the OpenAPI
// document are generated from it. With one table, the router and both
// documents always agree on which routes exist.

import (
	"fmt"
	"strings"
)

// Every liken capture API builds its paths as
// /v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}].
// The domain is the first label of the API group, display.liken.sh.
// The domain segment lets one ingress serve the APIs of all domains
// under one host name without path clashes. Until that ingress
// exists, each domain's API has its own Service.
const (
	apiDomain      = "display"
	apiRoot        = "/v1/" + apiDomain
	displaysPlural = "displays"
	screenAspect   = "screen"
)

// Every liken capture API serves a discovery document and an OpenAPI
// document in addition to its captures. The application/openapi+json
// media type is only proposed (draft-ietf-httpapi-rest-api-mediatypes)
// and IANA has not registered it.
const (
	jsonMediaType    = "application/json"
	openAPIMediaType = "application/openapi+json"
)

// The representations of a screen. Content negotiation prefers the
// earlier form on a tie, so a request with no Accept header, or with
// Accept: image/*, gets image/png.
var screenForms = []struct {
	ext       string
	mediaType string
}{
	{"png", "image/png"},
	{"jpg", "image/jpeg"},
	{"mp4", "video/mp4"},
	{"mjpeg", "multipart/x-mixed-replace"},
}

// IANA registers multipart/x-mixed-replace with a required boundary
// parameter, and no RFC defines the type. ffmpeg's mpjpeg muxer writes
// the boundary "ffmpeg" between parts, so the Content-Type must name
// that boundary.
const mjpegContentType = "multipart/x-mixed-replace; boundary=ffmpeg"

// The RFC 6570 template that the discovery document publishes for the
// screen aspect. Form-style expansion percent-encodes commas, so a
// client that expands it sends t=5%2C7. The query parser decodes each
// value before it splits the value on commas, so it reads t=5%2C7 as
// t=5,7.
const screenTemplate = apiRoot + "/" + displaysPlural +
	"/{name}/" + screenAspect + "{.ext}{?t,xywh,width,height,framerate,quality}"

// The kinds of route. A document route has an ETag and
// Cache-Control: no-cache, and returns 304 for a matching
// If-None-Match. An info route returns a document about one Display,
// and the caller needs the get permission on that Display. A capture
// route returns live frames with Cache-Control: no-store, because a
// cache must never serve an old picture of a screen.
type routeKind int

const (
	documentRoute routeKind = iota
	infoRoute
	captureRoute
)

// One route. template is the path as an RFC 6570 template. Metrics and
// log lines use it as the route label instead of the request path, so
// the label does not vary with the Display name. mediaType is the
// type of a route with a fixed form, and it is empty on the route that
// negotiates the form.
type apiRoute struct {
	template  string
	kind      routeKind
	mediaType string
	ext       string
	aspect    string
	// The summary of the route and the description of its 200
	// response, as the OpenAPI document shows them.
	summary             string
	responseDescription string
}

// apiRoutes lists every route of the API.
var apiRoutes = []apiRoute{
	{
		template:            apiRoot,
		kind:                documentRoute,
		mediaType:           jsonMediaType,
		summary:             "The discovery document",
		responseDescription: "The routes this API serves, as RFC 6570 templates.",
	},
	{
		template:            apiRoot + "/openapi.json",
		kind:                documentRoute,
		mediaType:           openAPIMediaType,
		summary:             "The OpenAPI 3.1 description of this API",
		responseDescription: "This document.",
	},
	{
		template:            apiRoot + "/" + displaysPlural + "/{name}",
		kind:                infoRoute,
		mediaType:           jsonMediaType,
		summary:             "The screen's size, scale, refresh and formats",
		responseDescription: "The size, scale, refresh, formats, and clip codecs of one screen, read from the node. If the compositor is not serving or this API cannot reach the node, the response still includes the name, node, and mode from the Display object. The compositor or sidecar field identifies the failure, and the response omits the other fields.",
	},
	{
		template:            apiRoot + "/" + displaysPlural + "/{name}/" + screenAspect,
		kind:                captureRoute,
		aspect:              screenAspect,
		summary:             "The screen, in the format chosen by Accept",
		responseDescription: "The screen in the type Accept selected, image/png by default.",
	},
	{
		template:            apiRoot + "/" + displaysPlural + "/{name}/" + screenAspect + ".png",
		kind:                captureRoute,
		mediaType:           "image/png",
		ext:                 "png",
		aspect:              screenAspect,
		summary:             "One frame of the screen as PNG",
		responseDescription: "One frame of the screen, encoded as PNG.",
	},
	{
		template:            apiRoot + "/" + displaysPlural + "/{name}/" + screenAspect + ".jpg",
		kind:                captureRoute,
		mediaType:           "image/jpeg",
		ext:                 "jpg",
		aspect:              screenAspect,
		summary:             "One frame of the screen as JPEG",
		responseDescription: "One frame of the screen, encoded as JPEG.",
	},
	{
		template:            apiRoot + "/" + displaysPlural + "/{name}/" + screenAspect + ".mp4",
		kind:                captureRoute,
		mediaType:           "video/mp4",
		ext:                 "mp4",
		aspect:              screenAspect,
		summary:             "A clip of the screen as H.264 in fragmented MP4",
		responseDescription: "A clip of the screen in H.264 fragmented MP4. The clip ends at the t= end or when the client closes the connection. The Content-Type carries the RFC 6381 codecs parameter: avc1.640029 for High profile level 4.1 up to 1920x1080 at 60 fps, and avc1.640033 for level 5.1 above that.",
	},
	{
		template:            apiRoot + "/" + displaysPlural + "/{name}/" + screenAspect + ".mjpeg",
		kind:                captureRoute,
		mediaType:           "multipart/x-mixed-replace",
		ext:                 "mjpeg",
		aspect:              screenAspect,
		summary:             "A stream of the screen, one JPEG per frame",
		responseDescription: "A stream of the screen with one image/jpeg part per frame. The stream ends at the t= end or when the client closes the connection.",
	},
}

// stillForm reports whether the type is a single image. A single
// image has no t= end, no framerate, and none of the other options
// that a clip takes.
func stillForm(mediaType string) bool {
	return mediaType == "image/png" || mediaType == "image/jpeg"
}

// formExtension returns the path extension of a media type, or an
// empty string for a type that has none. Content-Location and the
// alternate links use it to name a form of the screen.
func formExtension(mediaType string) string {
	for _, form := range screenForms {
		if form.mediaType == mediaType {
			return form.ext
		}
	}
	return ""
}

func contentTypeOf(mediaType string) string {
	if mediaType == "multipart/x-mixed-replace" {
		return mjpegContentType
	}
	return mediaType
}

// captureContentType adds the RFC 6381 codecs parameter to the
// Content-Type of a clip. A client that must choose a decoder before
// the first byte arrives, and media-api when it adds sound to the
// stream, read the codec from it.
func captureContentType(mediaType string, width, height, framerate int) string {
	if mediaType != "video/mp4" {
		return contentTypeOf(mediaType)
	}
	return fmt.Sprintf(`video/mp4; codecs="%s"`, h264Codecs(width, height, framerate))
}

// h264Codecs returns the RFC 6381 codecs string for a clip: avc1, then
// the profile, constraint, and level bytes. The encoder uses High
// profile (0x64) with no constraint flags (0x00). A clip up to
// 1920x1080 at 60 fps gets level 4.1 (0x29), and a larger or faster
// clip gets level 5.1 (0x33).
func h264Codecs(width, height, framerate int) string {
	if width > 1920 || height > 1080 || framerate > 60 {
		return "avc1.640033"
	}
	return "avc1.640029"
}

// h264Profile returns the ffmpeg options that fix the profile and
// level that h264Codecs reports. Without them h264_vaapi chooses the
// level from the stream, so the Content-Type could not be sent before
// the encode starts.
func h264Profile(width, height, framerate int) []string {
	level := "4.1"
	if h264Codecs(width, height, framerate) == "avc1.640033" {
		level = "5.1"
	}
	return []string{"-profile:v", "high", "-level", level}
}

// screenMediaTypes returns the media types of the screen aspect, in
// the order of screenForms.
func screenMediaTypes() []string {
	types := make([]string, 0, len(screenForms))
	for _, form := range screenForms {
		types = append(types, form.mediaType)
	}
	return types
}

// screenAcceptable returns the list for the acceptable member of a
// 406 document: every form of the screen aspect and the absolute path
// that serves it.
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

// capturePath returns the absolute path of one form of the screen. A
// Link header with an absolute path resolves against the origin of
// this API (RFC 8288 section 3.1).
func capturePath(name, ext string) string {
	return apiRoot + "/" + displaysPlural + "/" + name + "/" + screenAspect + "." + ext
}

// matchRoute finds the route for a path and returns it with the
// Display name from the path. It ignores the method, so a path that
// is not in the table gets a 404 and never a 405. The caller checks
// the method after it knows the route.
func matchRoute(path string) (apiRoute, string, bool) {
	for _, route := range apiRoutes {
		name, ok := matchTemplate(route.template, path)
		if ok {
			return route, name, true
		}
	}
	return apiRoute{}, "", false
}

// matchTemplate compares a path with a route template one segment at
// a time. {name} is the only variable in a template. It matches one
// non-empty segment, so a name that contains a slash matches no route.
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

// displaySelfTemplate returns the template that the discovery
// document gives as the self link of the displays resource, which is
// the template of the info route. The describedby link points to the
// Kubernetes object instead.
func displaySelfTemplate() string {
	return apiRoot + "/" + displaysPlural + "/{name}"
}
