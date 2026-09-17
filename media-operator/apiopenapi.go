package main

// This file writes the OpenAPI 3.1 document. It is generated from the
// router and committed, so the document and the routes come from one
// list and a test fails when they differ; `go generate` rewrites it.
// Each extension route is its own path item with no {.ext}, because
// OpenAPI has no form for a partial path segment and a client
// generator reads one fixed path per representation. The served copy
// changes one thing about the committed one: its servers entry names
// the origin the request reached.

//go:generate go test -run TestTheOpenAPIDocumentMatchesTheRouter -update-openapi

import (
	_ "embed"
	"encoding/json"
	"slices"
	"strings"
)

// openAPIServerPlaceholder is the server the committed copy names. The
// served copy replaces it with the configured public base, or else
// the origin the request reached.
const openAPIServerPlaceholder = "https://media-api.liken-system.svc"

//go:embed openapi.json
var committedOpenAPI []byte

// openAPISpecification is the document's shape, narrow on purpose: the
// paths, the three methods, the parameters, and the statuses each
// route answers. No schemas, because every body is a live capture, a
// problem document, or one of the three documents the manual shows.
type openAPISpecification struct {
	OpenAPI    string                    `json:"openapi"`
	Info       openAPIInfo               `json:"info"`
	Servers    []openAPIServer           `json:"servers"`
	Security   []map[string][]string     `json:"security"`
	Paths      map[string]openAPIPathset `json:"paths"`
	Components openAPIComponents         `json:"components"`
}

// The two ways a caller names itself, as OpenAPI spells them.
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

// routeTemplates lists every template the router serves, built from
// the same aspects and forms the router is built from, so the document
// and the router can never name different routes.
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

// openAPIDocument builds the document: one path item per template,
// with GET, HEAD, and OPTIONS, the aspect's own query keys as
// parameters, and the statuses the route table states.
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
		// A list of two requirement objects is OpenAPI's OR, so a
		// route takes the client certificate or the token, in the
		// order this API reads them.
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

// operationID derives the id from the template, playerMediaMp4 for
// the media.mp4 route, so a new form gets an id without a table to
// keep in step and no two routes share one.
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

// openAPIParameters lists the two path variables of a Player route,
// then the query keys the route's aspect takes.
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

// aspectOf finds the aspect a template names and the form where the
// template names one. An empty form means the extensionless route,
// which negotiates.
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

// templateKeys is the query keys the template's aspect takes, and
// none for a document route.
func templateKeys(template string) []string {
	aspect, _, held := aspectOf(template)
	if !held {
		return nil
	}
	return aspect.keys
}

// openAPISummary is one line on what each route answers, as the route
// table states it.
func openAPISummary(template string) string {
	switch template {
	case apiBasePath:
		return "The routes this API serves, as RFC 6570 templates."
	case apiBasePath + "/openapi.json":
		return "This OpenAPI document."
	case apiPlayerTemplate:
		return "The Player's Display, its Sinks, whether a Play runs, and the stream count."
	}
	aspect, form, held := aspectOf(template)
	if !held {
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

// openAPIResponses lists the statuses a route answers. Every route
// answers 401, 403, and 405. A document route adds 200 and 304, and
// the info route 404. A capture route adds its own 200, 307, or 409,
// then the statuses an upstream or the grammar can produce.
func openAPIResponses(template string) map[string]openAPIResponse {
	responses := map[string]openAPIResponse{
		"401": {Description: "No client certificate and no token, or a token the TokenReview refused."},
		"403": {Description: "The SubjectAccessReview denied the subject."},
		"405": {Description: "A method other than GET, HEAD, or OPTIONS."},
	}
	aspect, _, held := aspectOf(template)
	if !held {
		responses["200"] = openAPIResponse{Description: documentAnswer(template)}
		responses["304"] = openAPIResponse{Description: "The document matches the If-None-Match field."}
		if template == apiPlayerTemplate {
			responses["404"] = openAPIResponse{Description: "No Player of that name."}
		}
		return responses
	}
	switch aspect.name {
	case screenAspectName:
		responses["307"] = openAPIResponse{Description: "The display-api route for this Player's Display."}
		responses["409"] = openAPIResponse{Description: "The Player carries no status.screen."}
	case audioAspectName:
		responses["307"] = openAPIResponse{Description: "The audio-api route for this Player's Sink."}
		responses["409"] = openAPIResponse{Description: "No Play runs on this Player, or it resolves no Sink."}
	default:
		responses["200"] = openAPIResponse{
			Description: "The composed stream: one video track where the Player has a screen, " +
				"and one audio track per Sink in spec.sinks order.",
		}
		responses["307"] = openAPIResponse{Description: "The one stream this Player resolves, where it resolves only one."}
		responses["409"] = openAPIResponse{Description: "The Player resolves no stream at all."}
	}
	responses["400"] = openAPIResponse{Description: "A query the grammar refuses, or an upstream 400."}
	responses["404"] = openAPIResponse{Description: "No Player of that name, or an upstream 404."}
	responses["406"] = openAPIResponse{Description: "Nothing this route serves is acceptable."}
	responses["502"] = openAPIResponse{Description: "An upstream answer this API cannot relay."}
	responses["503"] = openAPIResponse{Description: "An upstream is busy, or this API is at its composition limit."}
	responses["504"] = openAPIResponse{Description: "An upstream sent no headers within ten seconds plus the t= begin."}
	return responses
}

// documentAnswer is the 200 description of each of the three document
// routes.
func documentAnswer(template string) string {
	switch template {
	case apiBasePath:
		return "The discovery document, keyed by plural, with one entry per aspect."
	case apiBasePath + "/openapi.json":
		return "This document, with the servers entry naming the origin the request reached."
	default:
		return "The Player's capture facts, with a related link to every capture route."
	}
}

// servedOpenAPI sets the one field the served copy owns, servers,
// to the origin given. The committed copy cannot carry it, because
// the origin is known only at request time and differs between a
// port-forward and a public name. Any failure serves the committed
// copy unchanged.
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

// openAPIPaths lists the paths the committed document names, sorted,
// for the test that asks whether the router and the document agree.
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
