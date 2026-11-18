package main

// This file serves the discovery document at /v1/media, the OpenAPI
// document, and the info document of one Player.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

// The media type of each document. application/openapi+json comes from
// draft-ietf-httpapi-rest-api-mediatypes, and IANA does not list it yet.
const (
	jsonDocumentType    = "application/json"
	openAPIDocumentType = "application/openapi+json"
)

// discoveryDocument is the discovery format that this API, the display
// API, and the audio API share. Resources are keyed by their plural name,
// so one client can read all three.
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

// The query parameters of each aspect's URI template, written as the
// RFC 6570 form-style query expansion that ends the template.
const (
	screenTemplateKeys = "{?t,xywh,width,height,framerate,quality}"
	audioTemplateKeys  = "{?t,bitrate}"
	mediaTemplateKeys  = "{?t,xywh,width,height,framerate,quality,bitrate}"
)

func (s *apiServer) serveDiscovery(e *apiExchange) {
	if _, ok := e.authenticate(); !ok {
		return
	}
	e.document(marshaled(s.discovery(e.request.Context())), jsonDocumentType, e.buildTag())
}

// discovery builds the discovery document. A screen or audio request
// redirects to the display API or the audio API, so those two aspects
// list the forms that the sibling API serves. Each starts with this API's
// built-in list of forms, and copySibling replaces it with the sibling's
// list when the sibling responds. The built-in list stays when it does
// not, because an empty list would tell a client that the aspect serves
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

// copySibling fetches a sibling's discovery document and copies the media
// types and extensions of one aspect into into. It keeps the template and
// the redirects flag, because they describe this API's route. When the
// request fails, or the sibling lists no media types for the aspect,
// copySibling changes nothing.
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
	found, ok := document.Resources[plural].Aspects[aspect]
	if !ok || len(found.MediaTypes) == 0 {
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

// serveOpenAPI serves the committed OpenAPI document with its servers list
// replaced by the origin that the request reached, or by the configured
// public base when there is one.
func (s *apiServer) serveOpenAPI(e *apiExchange) {
	if _, ok := e.authenticate(); !ok {
		return
	}
	e.document(servedOpenAPI(s.origin(e)), openAPIDocumentType, e.buildTag())
}

// playerInfoDocument is what a client can read before it requests a
// capture: the Display and its node, the Sinks in the Player's status,
// whether a Play is running, and how many streams a capture would
// combine. The client can use the count to tell whether media.mp4 will
// compose a stream or fail with 409.
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

// bodyTag is the ETag of the info document: a SHA-256 digest of its body,
// because the document changes with the Player's status.
func bodyTag(body []byte) string {
	digest := sha256.Sum256(body)
	return `"` + hex.EncodeToString(digest[:]) + `"`
}

// buildTag is the ETag of the discovery and OpenAPI documents: the build
// version. The committed OpenAPI document changes only with a release.
// The discovery document's screen and audio lists come from the sibling
// APIs, so it can change without a release while its ETag stays the same.
func (e *apiExchange) buildTag() string {
	return `"` + e.server.version + `"`
}

// marshaled encodes a document as JSON. The documents hold only strings,
// numbers, slices, and maps, so encoding cannot fail.
func marshaled(payload any) []byte {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return body
}

// document writes a document response with an ETag and
// Cache-Control: no-cache, so a client revalidates on every read. A
// matching If-None-Match gets 304 with no body. Unlike a capture, the
// response has no file name, no range information, and no chunked
// encoding.
func (e *apiExchange) document(body []byte, contentType, tag string) {
	header := e.writer.Header()
	header.Set("ETag", tag)
	header.Set("Cache-Control", "no-cache")
	if matchesETag(e.request.Header.Get("If-None-Match"), tag) {
		e.writeStatus(http.StatusNotModified)
		return
	}
	header.Set("Content-Type", contentType)
	e.writeStatus(http.StatusOK)
	if e.head {
		return
	}
	written, _ := e.writer.Write(body)
	e.bytes = int64(written)
}

// matchesETag reports whether an If-None-Match field matches tag. The
// field is a comma-separated list. An entry can have a W/ weak prefix,
// and * matches any current representation.
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

// serveHealth serves /healthz and /readyz. /healthz returns 200 while the
// process runs. /readyz returns 503 until startup sets s.ready, after a
// TokenReview of this API's own token gets a response from the API server,
// and then 200 for the life of the process. A probe does not call the API
// server, so a later TokenReview failure shows only in a metric and a log
// line and does not remove the pod from the Service.
func (s *apiServer) serveHealth(e *apiExchange) {
	e.writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if e.route == readyTemplate && !s.ready.Load() {
		e.writeStatus(http.StatusServiceUnavailable)
		if !e.head {
			_, _ = e.writer.Write([]byte("not ready\n"))
		}
		return
	}
	e.writeStatus(http.StatusOK)
	if !e.head {
		_, _ = e.writer.Write([]byte("ok\n"))
	}
}
