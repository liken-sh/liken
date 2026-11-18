package main

// This file builds the OpenAPI 3.1 document of media-api, which is
// committed as openapi.json. A test fails when the committed copy
// differs from the router, and `go generate` rewrites it. Each
// extension route, such as media.mp4, is its own path item, because
// OpenAPI cannot express a partial path segment like {.ext} and client
// generators expect one fixed path for each representation.

//go:generate go test -run TestTheOpenAPIDocumentMatchesTheRouter -update-openapi

import (
	_ "embed"
	"encoding/json"
	"slices"
	"strings"
)

// openAPIServerPlaceholder is the server URL in the committed copy. The
// served copy replaces it with the configured public base URL, or with
// the origin of the request when none is configured.
const openAPIServerPlaceholder = "https://media-api.liken-system.svc"

//go:embed openapi.json
var committedOpenAPI []byte

// openAPISpecification has only the parts of OpenAPI that this
// document uses. It has no schemas, because each response body is a
// live capture, a problem document, or a document that the manual
// describes.
type openAPISpecification struct {
	OpenAPI    string                    `json:"openapi"`
	Info       openAPIInfo               `json:"info"`
	Servers    []openAPIServer           `json:"servers"`
	Security   []map[string][]string     `json:"security"`
	Paths      map[string]openAPIPathset `json:"paths"`
	Components openAPIComponents         `json:"components"`
}

type openAPIComponents struct {
	SecuritySchemes map[string]openAPISecurityScheme `json:"securitySchemes"`
}

type openAPISecurityScheme struct {
	Type         string `json:"type"`
	Scheme       string `json:"scheme,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty"`
	Description  string `json:"description,omitempty"`
}

type openAPIInfo struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

type openAPIServer struct {
	URL string `json:"url"`
}

type openAPIPathset struct {
	Get     *openAPIOperation `json:"get,omitempty"`
	Head    *openAPIOperation `json:"head,omitempty"`
	Options *openAPIOperation `json:"options,omitempty"`
}

type openAPIOperation struct {
	OperationID string                     `json:"operationId"`
	Summary     string                     `json:"summary"`
	Parameters  []openAPIParameter         `json:"parameters,omitempty"`
	Responses   map[string]openAPIResponse `json:"responses"`
}

type openAPIParameter struct {
	Name     string        `json:"name"`
	In       string        `json:"in"`
	Required bool          `json:"required,omitempty"`
	Schema   openAPISchema `json:"schema"`
}

type openAPISchema struct {
	Type string `json:"type"`
}

type openAPIResponse struct {
	Description string `json:"description"`
}

// routeTemplates returns every route template that the router serves.
// It reads the same captureAspects as the router, so a new aspect or
// form reaches the document without an edit here.
func routeTemplates() []string {
	templates := []string{
		apiBasePath,
		apiBasePath + "/openapi.json",
		apiPlayerTemplate,
	}
	for _, aspect := range captureAspects() {
		templates = append(templates, apiPlayerTemplate+"/"+aspect.name)
		for _, form := range aspect.forms {
			templates = append(templates, apiPlayerTemplate+"/"+aspect.name+"."+form.Extension)
		}
	}
	return templates
}

func openAPIDocument() []byte {
	specification := openAPISpecification{
		OpenAPI: "3.1.0",
		Info: openAPIInfo{
			Title:   "media-api",
			Version: "v1alpha1",
			Description: "media-api serves the HTTP routes for a Player. Its screen routes redirect to " +
				"display-api, its audio routes redirect to audio-api, and its media routes " +
				"compose the two into one muxed stream.",
		},
		Servers: []openAPIServer{{URL: openAPIServerPlaceholder}},
		// Separate requirement objects in the list are alternatives, so
		// a request authenticates with either a client certificate or
		// a token.
		Security: []map[string][]string{
			{"mutualTLS": {}},
			{"bearer": {}},
		},
		Paths: map[string]openAPIPathset{},
		Components: openAPIComponents{
			SecuritySchemes: map[string]openAPISecurityScheme{
				"mutualTLS": {
					Type: "mutualTLS",
					Description: "A client certificate the cluster's own authority signed. " +
						"The subject's common name is the user and its organization values " +
						"are the groups, which is how the API server reads one.",
				},
				"bearer": {
					Type:         "http",
					Scheme:       "bearer",
					BearerFormat: "JWT",
					Description: "A Kubernetes ServiceAccount token minted with the audience " +
						"media-api, which this API checks with a TokenReview.",
				},
			},
		},
	}
	for _, template := range routeTemplates() {
		operation := &openAPIOperation{
			OperationID: operationID(template),
			Summary:     openAPISummary(template),
			Parameters:  openAPIParameters(template),
			Responses:   openAPIResponses(template),
		}
		head := *operation
		head.OperationID = operation.OperationID + "Head"
		head.Summary = operation.Summary + " The headers alone, with no body, no capture, and no upstream call."
		specification.Paths[template] = openAPIPathset{
			Get:  operation,
			Head: &head,
			Options: &openAPIOperation{
				OperationID: operation.OperationID + "Options",
				Summary:     "The methods this route allows.",
				Responses: map[string]openAPIResponse{
					"204": {Description: "Allow names GET, HEAD, and OPTIONS."},
				},
			},
		}
	}
	body, err := json.MarshalIndent(specification, "", "  ")
	if err != nil {
		return nil
	}
	return append(body, '\n')
}

// operationID derives the operation id from the template, such as
// playerMediaMp4 for the media.mp4 route, so a new form needs no entry
// in a lookup table.
func operationID(template string) string {
	switch template {
	case apiBasePath:
		return "discovery"
	case apiBasePath + "/openapi.json":
		return "openapi"
	case apiPlayerTemplate:
		return "player"
	}
	return "player" + capitalized(strings.TrimPrefix(template, apiPlayerTemplate+"/"))
}

func capitalized(text string) string {
	cleaned := []rune{}
	upper := true
	for _, letter := range text {
		if letter == '.' {
			upper = true
			continue
		}
		if upper && letter >= 'a' && letter <= 'z' {
			letter = letter - 'a' + 'A'
		}
		upper = false
		cleaned = append(cleaned, letter)
	}
	return string(cleaned)
}

// openAPIParameters returns the namespace and name path parameters of a
// Player route, then the query keys of the route's aspect.
func openAPIParameters(template string) []openAPIParameter {
	var parameters []openAPIParameter
	if strings.HasPrefix(template, apiPlayerTemplate) {
		parameters = append(parameters,
			openAPIParameter{Name: "namespace", In: "path", Required: true, Schema: openAPISchema{Type: "string"}},
			openAPIParameter{Name: "name", In: "path", Required: true, Schema: openAPISchema{Type: "string"}})
	}
	for _, key := range templateKeys(template) {
		parameters = append(parameters,
			openAPIParameter{Name: key, In: "query", Schema: openAPISchema{Type: "string"}})
	}
	return parameters
}

// aspectOf returns the aspect of a template and, when the template has
// an extension, its form. The form is empty for the route without an
// extension, which chooses the format from the Accept header.
func aspectOf(template string) (captureAspect, mediaForm, bool) {
	for _, aspect := range captureAspects() {
		prefix := apiPlayerTemplate + "/" + aspect.name
		if template == prefix {
			return aspect, mediaForm{}, true
		}
		for _, form := range aspect.forms {
			if template == prefix+"."+form.Extension {
				return aspect, form, true
			}
		}
	}
	return captureAspect{}, mediaForm{}, false
}

// templateKeys returns the query keys of the template's aspect, or nil
// for a document route.
func templateKeys(template string) []string {
	aspect, _, ok := aspectOf(template)
	if !ok {
		return nil
	}
	return aspect.keys
}

func openAPISummary(template string) string {
	switch template {
	case apiBasePath:
		return "The routes this API serves, as RFC 6570 templates."
	case apiBasePath + "/openapi.json":
		return "This OpenAPI document."
	case apiPlayerTemplate:
		return "The Player's Display, its Sinks, whether a Play runs, and the stream count."
	}
	aspect, form, ok := aspectOf(template)
	if !ok {
		return ""
	}
	subject := map[string]string{
		screenAspectName: "The Player's screen",
		audioAspectName:  "The Player's sound",
		mediaAspectName:  "The Player's screen and sound in one stream",
	}[aspect.name]
	if form.Type == "" {
		return subject + ", in the format chosen by Accept."
	}
	return subject + ", as " + form.Type + "."
}

func openAPIResponses(template string) map[string]openAPIResponse {
	responses := map[string]openAPIResponse{
		"401": {Description: "No client certificate and no token, or a token the TokenReview refused."},
		"403": {Description: "The SubjectAccessReview denied the subject."},
		"405": {Description: "A method other than GET, HEAD, or OPTIONS."},
	}
	aspect, _, ok := aspectOf(template)
	if !ok {
		responses["200"] = openAPIResponse{Description: documentResponse(template)}
		responses["304"] = openAPIResponse{Description: "The document matches the If-None-Match field."}
		if template == apiPlayerTemplate {
			responses["404"] = openAPIResponse{Description: "No Player of that name."}
		}
		return responses
	}
	switch aspect.name {
	case screenAspectName:
		responses["307"] = openAPIResponse{Description: "The display-api route for this Player's Display."}
		responses["409"] = openAPIResponse{Description: "The Player has no status.screen."}
	case audioAspectName:
		responses["307"] = openAPIResponse{Description: "The audio-api route for this Player's Sink."}
		responses["409"] = openAPIResponse{Description: "No Play runs on this Player, or it resolves no Sink."}
	default:
		responses["200"] = openAPIResponse{
			Description: "The composed stream: one video track where the Player has a screen, " +
				"and one audio track per Sink in spec.sinks order.",
		}
		responses["307"] = openAPIResponse{Description: "The stream's route, returned when this Player resolves exactly one stream."}
		responses["409"] = openAPIResponse{Description: "The Player resolves no stream at all, " +
			"or an upstream reports its Display, Sink, or Source away."}
	}
	responses["400"] = openAPIResponse{Description: "A query the grammar refuses, or an upstream 400."}
	responses["404"] = openAPIResponse{Description: "No Player of that name, or an upstream 404."}
	responses["406"] = openAPIResponse{Description: "Nothing this route serves is acceptable."}
	responses["502"] = openAPIResponse{Description: "An upstream response that this API cannot relay."}
	responses["503"] = openAPIResponse{Description: "An upstream is busy, or this API is at its composition limit."}
	responses["504"] = openAPIResponse{Description: "An upstream sent no headers within ten seconds plus the t= begin."}
	return responses
}

// documentResponse returns the description of a document route's 200
// response.
func documentResponse(template string) string {
	switch template {
	case apiBasePath:
		return "The discovery document, keyed by plural, with one entry per aspect."
	case apiBasePath + "/openapi.json":
		return "This document. Its servers entry is the configured public base URL, or else the origin that the request used."
	default:
		return "The Player's capture facts, with a related link to every capture route."
	}
}

// servedOpenAPI returns the committed document with its servers entry
// set to origin, which differs between a port-forward and a public
// name. On any error it returns the committed copy unchanged.
func servedOpenAPI(origin string) []byte {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(committedOpenAPI, &document); err != nil {
		return committedOpenAPI
	}
	servers, err := json.Marshal([]openAPIServer{{URL: origin}})
	if err != nil {
		return committedOpenAPI
	}
	document["servers"] = servers
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return committedOpenAPI
	}
	return append(body, '\n')
}

// openAPIPaths returns the sorted paths of the committed document, for
// the test that compares it with the router.
func openAPIPaths(document []byte) []string {
	var specification struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(document, &specification); err != nil {
		return nil
	}
	paths := make([]string, 0, len(specification.Paths))
	for path := range specification.Paths {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}
