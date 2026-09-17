package main

// This file serves the three documents of the API: discovery at
// /v1/media, the OpenAPI document, and the info document of one
// Player. The first two are compiled into the binary, so the build
// version is their validator and a client revalidates them with one
// conditional request per release. The screen and audio lists in
// discovery are copied from the siblings' own documents at request
// time, because a screen or audio request redirects to a sibling and
// the forms this API can promise are the forms that sibling serves.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

// The media type of each document. application/openapi+json is
// provisional: draft-ietf-httpapi-rest-api-mediatypes registers it and
// IANA does not list it yet.
const (
	jsonDocumentType    = "application/json"
	openAPIDocumentType = "application/openapi+json"
)

// discoveryDocument is the discovery shape the three capture APIs
// share, keyed by the resource's plural, so one client reads all
// three the same way.
type discoveryDocument struct {
	Resources map[string]discoveryResource `json:"resources"`
	OpenAPI   string                       `json:"openapi"`
}

type discoveryResource struct {
	Self    string                     `json:"self"`
	Aspects map[string]discoveryAspect `json:"aspects"`
}

type discoveryAspect struct {
	Template   string   `json:"template"`
	MediaTypes []string `json:"mediaTypes"`
	Extensions []string `json:"extensions"`
	Redirects  bool     `json:"redirects"`
	LeadIn     string   `json:"leadIn,omitempty"`
}

// The query keys each aspect's template offers, as the RFC 6570 form
// expansion at the end of the template.
const (
	screenTemplateKeys = "{?t,xywh,width,height,framerate,quality}"
	audioTemplateKeys  = "{?t,bitrate}"
	mediaTemplateKeys  = "{?t,xywh,width,height,framerate,quality,bitrate}"
)

func (s *apiServer) serveDiscovery(e *apiExchange) {
	if _, _, ok := e.authenticate(); !ok {
		return
	}
	e.document(marshaled(s.discovery(e.request.Context())), jsonDocumentType, e.buildTag())
}

// discovery builds the document. The screen and audio lists start as
// this API's own copies of what the siblings serve and are replaced by
// the siblings' lists at request time, so a sibling that gains a form
// shows it here without a release of this API. A sibling that does
// not answer leaves this API's own list in place, because a discovery
// document with an empty aspect would tell a client the aspect serves
// nothing.
func (s *apiServer) discovery(ctx context.Context) discoveryDocument {
	screen := discoveryAspect{
		Template:   apiPlayerTemplate + "/screen{.ext}" + screenTemplateKeys,
		MediaTypes: formTypes(screenForms),
		Extensions: formExtensions(screenForms),
		Redirects:  true,
	}
	audio := discoveryAspect{
		Template:   apiPlayerTemplate + "/audio{.ext}" + audioTemplateKeys,
		MediaTypes: formTypes(audioForms),
		Extensions: formExtensions(audioForms),
		Redirects:  true,
	}
	s.copySibling(ctx, upstreamDisplay, s.display+"/v1/display", "displays", "screen", &screen)
	s.copySibling(ctx, upstreamAudio, s.audio+"/v1/audio", "sinks", "audio", &audio)
	return discoveryDocument{
		Resources: map[string]discoveryResource{
			"players": {
				Self: apiPlayerTemplate,
				Aspects: map[string]discoveryAspect{
					"screen": screen,
					"audio":  audio,
					"media": {
						Template:   apiPlayerTemplate + "/media{.ext}" + mediaTemplateKeys,
						MediaTypes: formTypes(composedForms),
						Extensions: formExtensions(composedForms),
						Redirects:  false,
						LeadIn:     composeLeadInWord,
					},
				},
			},
		},
		OpenAPI: apiBasePath + "/openapi.json",
	}
}

// copySibling reads one aspect out of a sibling's discovery document
// and takes its media types and extensions, the two lists a redirect
// depends on. The template and the redirects flag stay this API's
// own, because they describe this API's route and not the sibling's.
// A sibling that fails, or answers a document with no such aspect,
// changes nothing.
func (s *apiServer) copySibling(ctx context.Context, upstream, target, plural, aspect string, into *discoveryAspect) {
	stream, fault := s.upstream.open(ctx, upstream, target, upstreamHeaderTimeout)
	if fault != nil {
		return
	}
	defer stream.close()
	var document discoveryDocument
	if err := json.NewDecoder(stream.body).Decode(&document); err != nil {
		return
	}
	found, held := document.Resources[plural].Aspects[aspect]
	if !held || len(found.MediaTypes) == 0 {
		return
	}
	into.MediaTypes = found.MediaTypes
	into.Extensions = found.Extensions
}

func formTypes(forms []mediaForm) []string {
	types := make([]string, len(forms))
	for index, form := range forms {
		types[index] = form.Type
	}
	return types
}

func formExtensions(forms []mediaForm) []string {
	extensions := make([]string, len(forms))
	for index, form := range forms {
		extensions[index] = form.Extension
	}
	return extensions
}

// serveOpenAPI answers the committed document with one change: the
// servers list names the origin this request reached, or the
// configured public base, in place of the committed placeholder.
func (s *apiServer) serveOpenAPI(e *apiExchange) {
	if _, _, ok := e.authenticate(); !ok {
		return
	}
	e.document(servedOpenAPI(s.origin(e)), openAPIDocumentType, e.buildTag())
}

// playerInfoDocument is what a client reads before it asks for a
// capture: the Display and its node, each Sink the Player remembers,
// whether a Play runs, and the stream count. The count is the stream
// count rule applied for the client, which then reads whether
// media.mp4 will compose or redirect before it asks.
type playerInfoDocument struct {
	Namespace string             `json:"namespace"`
	Name      string             `json:"name"`
	Display   *playerInfoDisplay `json:"display,omitempty"`
	Sinks     []PlayerSinkStatus `json:"sinks,omitempty"`
	Playing   bool               `json:"playing"`
	Streams   int                `json:"streams"`
}

type playerInfoDisplay struct {
	Name string `json:"name"`
	Node string `json:"node"`
}

func (s *apiServer) serveInfo(e *apiExchange) {
	if !e.authorize("") {
		return
	}
	player, ok := e.player()
	if !ok {
		return
	}
	document := playerInfoDocument{
		Namespace: player.Metadata.Namespace,
		Name:      player.Metadata.Name,
		Sinks:     player.Status.Sinks,
		Playing:   player.Status.Activity == playerPlaying,
	}
	if player.Status.Screen != nil && player.Status.Screen.Monitor != "" {
		document.Display = &playerInfoDisplay{
			Name: player.Status.Screen.Monitor,
			Node: player.Status.Screen.Node,
		}
		document.Streams++
	}
	document.Streams += len(tappableSinks(player))
	header := e.writer.Header()
	for _, aspect := range captureAspects() {
		addLink(header, e.aspectPath(aspect.name, ""), relRelated, "")
	}
	body := marshaled(document)
	e.document(body, jsonDocumentType, bodyTag(body))
}

// bodyTag is the info document's validator, a digest of its own body
// and not the build version. A revalidation must answer 304 only while
// the Player's facts stand, and the facts change when a claim moves.
func bodyTag(body []byte) string {
	digest := sha256.Sum256(body)
	return `"` + hex.EncodeToString(digest[:]) + `"`
}

// buildTag is the validator of the two documents the router compiles
// in. They change only with a release, so the build version is the
// exact validator.
func (e *apiExchange) buildTag() string {
	return `"` + e.server.version + `"`
}

// marshaled encodes a document. The documents are strings, slices,
// and maps, so the encoding cannot fail, and a nil body is never
// answered.
func marshaled(payload any) []byte {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return body
}

// document answers a document route: an ETag, no-cache so a client
// revalidates on every read, and 304 with no body on a matching
// If-None-Match. It carries none of the capture fields, no file name,
// no range statement, and no chunking, because a document is not a
// capture and a cache may hold it.
func (e *apiExchange) document(body []byte, contentType, tag string) {
	header := e.writer.Header()
	header.Set("ETag", tag)
	header.Set("Cache-Control", "no-cache")
	if matchesETag(e.request.Header.Get("If-None-Match"), tag) {
		e.answer(http.StatusNotModified)
		return
	}
	header.Set("Content-Type", contentType)
	e.answer(http.StatusOK)
	if e.head {
		return
	}
	written, _ := e.writer.Write(body)
	e.bytes = int64(written)
}

// matchesETag reads an If-None-Match field: a list of validators, a
// weak validator with its W/ prefix, or the wildcard, which matches
// any current representation.
func matchesETag(field, tag string) bool {
	if field == "" {
		return false
	}
	for _, candidate := range strings.Split(field, ",") {
		candidate = strings.TrimSpace(candidate)
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == "*" || candidate == tag {
			return true
		}
	}
	return false
}

// serveHealth answers the two probe paths. /healthz answers 200 while
// the process runs. /readyz is a latching startup check: it answers
// 503 until the TLS pair is loaded and one TokenReview of the API's
// own token has succeeded, then 200 for the life of the process. It
// never calls the API server per probe; a later failure is a metric
// and a log line, not a pod the kubelet takes out of the Service.
func (s *apiServer) serveHealth(e *apiExchange) {
	e.writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if e.route == readyTemplate && !s.ready.Load() {
		e.answer(http.StatusServiceUnavailable)
		if !e.head {
			_, _ = e.writer.Write([]byte("not ready\n"))
		}
		return
	}
	e.answer(http.StatusOK)
	if !e.head {
		_, _ = e.writer.Write([]byte("ok\n"))
	}
}
