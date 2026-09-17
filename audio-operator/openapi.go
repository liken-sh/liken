package main

// The OpenAPI 3.1 document, rendered from the router table in
// apiroutes.go so the published contract and the served routes cannot
// drift.
//
// OpenAPI has no label expansion, so each extension path is its own
// path item with a single-entry content map, where the discovery
// document publishes one template with {.ext}.
//
// The served copy injects the configured public base into servers,
// else the request's own origin. The committed copy at
// docs/static/v1/audio/openapi.json holds a placeholder, and a test
// asserts the two are equal apart from servers.

import (
	"encoding/json"
	"sort"
	"strings"
)

// openAPIPlaceholderServer is what the committed copy carries in
// place of a real origin. A reader of the published document replaces
// it with the origin they reach the API at, and the equality test
// ignores this member.
const openAPIPlaceholderServer = "https://audio-api.liken-system.svc"

// openAPIVersion is the OpenAPI release this document is written
// against. 3.1 is the release whose schema dialect is JSON Schema
// 2020-12.
const openAPIVersion = "3.1.1"

// openAPIDocument is the shape this API publishes. The members are
// ordered maps in JSON, so they are written as map[string]any and
// marshalled by encoding/json, which sorts a map's keys.
type openAPIDocument struct {
	OpenAPI string             `json:"openapi"`
	Info    openAPIInfo        `json:"info"`
	Servers []openAPIServer    `json:"servers"`
	Paths   map[string]any     `json:"paths"`
	Comps   openAPIComponents  `json:"components"`
	Tags    []openAPITag       `json:"tags,omitempty"`
	Secure  []map[string][]any `json:"security"`
}

type openAPIInfo struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
}

type openAPIServer struct {
	URL string `json:"url"`
}

type openAPITag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type openAPIComponents struct {
	Schemas         map[string]any `json:"schemas"`
	SecuritySchemes map[string]any `json:"securitySchemes"`
	Responses       map[string]any `json:"responses"`
}

// newOpenAPI renders the document. server is the origin the served
// copy names; the committed copy passes the placeholder.
//
// info.version is the API's own version and not the build's, so two
// builds of one commit render the same bytes and the committed copy
// holds. The build version is on every document's ETag, which is
// where a client reads it.
func newOpenAPI(server string) openAPIDocument {
	document := openAPIDocument{
		OpenAPI: openAPIVersion,
		Info: openAPIInfo{
			Title:   "audio.liken.sh capture API",
			Version: "v1",
			Summary: "Taps what a Sink plays and what a Source hears, and streams it as " +
				"WAV, FLAC, or Ogg Opus.",
			Description: "The path names the Sink or Source, the extension or Accept " +
				"chooses the format, and a W3C Media Fragments t= in the query sets " +
				"the span. Authenticate with a client certificate signed by the " +
				"cluster's authority, or with a ServiceAccount token for the audience " +
				"audio-api. A tap needs get on sinks/audio or sources/audio in the " +
				"group audio.liken.sh. Nothing is stored. Every response streams " +
				"from the node the endpoint is on.",
		},
		Servers: []openAPIServer{{URL: server}},
		Paths:   map[string]any{},
		Comps: openAPIComponents{
			Schemas:         openAPISchemas(),
			SecuritySchemes: openAPISecuritySchemes(),
			Responses:       openAPIResponses(),
		},
		// A list of two requirement objects is OpenAPI's OR, so a
		// route takes the client certificate or the token, in the
		// order this API reads them.
		Secure: []map[string][]any{{"mutualTLS": {}}, {"bearerToken": {}}},
	}
	for _, route := range apiRoutes {
		document.Paths[openAPIPath(route)] = openAPIPathItem(route)
	}
	return document
}

// openAPIPath is the route's template in OpenAPI's own spelling, which
// is the same {name} the RFC 6570 template uses for simple expansion.
func openAPIPath(route apiRoute) string {
	return route.Template
}

// openAPIPathItem renders one row: the three methods, the query
// parameters the row takes, the content map of the forms it serves,
// and the problem responses it can answer with.
func openAPIPathItem(route apiRoute) map[string]any {
	operation := map[string]any{
		"operationId": openAPIOperationID(route),
		"summary":     route.Answers,
		"parameters":  openAPIParameters(route),
		"responses":   openAPIRouteResponses(route),
	}
	item := map[string]any{
		"get": operation,
		"head": map[string]any{
			"operationId": openAPIOperationID(route) + "Head",
			"summary": "The headers of " + lowerFirst(route.Answers) +
				" HEAD takes no sample and makes no call to the capture container " +
				"(RFC 9110 section 9.3.2).",
			"parameters": openAPIParameters(route),
			"responses":  openAPIRouteResponses(route),
		},
		"options": map[string]any{
			"operationId": openAPIOperationID(route) + "Options",
			"summary": "The methods this route allows, as a 204 with Allow " +
				"(RFC 9110 section 10.2.1).",
			"responses": map[string]any{
				"204": map[string]any{
					"description": "The methods this route allows.",
					"headers": map[string]any{
						"Allow": map[string]any{
							"description": "GET, HEAD, OPTIONS.",
							"schema":      map[string]any{"type": "string"},
						},
					},
				},
			},
		},
	}
	if strings.Contains(route.Template, "{name}") {
		item["parameters"] = []any{map[string]any{
			"name":     "name",
			"in":       "path",
			"required": true,
			"description": "The Sink's or Source's own name, which the operator builds " +
				"from the hardware's identity.",
			"schema": map[string]any{"type": "string"},
		}}
	}
	return item
}

// openAPIOperationID names one operation. A reader of a generated
// client sees these names, so they read as the route does: the plural,
// the aspect, and the format.
func openAPIOperationID(route apiRoute) string {
	switch route.Kind {
	case routeDiscovery:
		return "discovery"
	case routeOpenAPI:
		return "openapi"
	case routeInfo:
		return "get" + title(singular(route.Resource))
	default:
		name := "get" + title(singular(route.Resource)) + title(route.Aspect)
		if route.Extension != "" {
			name += title(route.Extension)
		}
		return name
	}
}

func singular(plural string) string {
	return strings.TrimSuffix(plural, "s")
}

func title(word string) string {
	if word == "" {
		return ""
	}
	return strings.ToUpper(word[:1]) + word[1:]
}

// openAPIParameters renders the query knobs the row takes.
func openAPIParameters(route apiRoute) []any {
	var parameters []any
	for _, knob := range route.Query {
		if !route.takes(knob.Name) {
			continue
		}
		parameters = append(parameters, map[string]any{
			"name":        knob.Name,
			"in":          "query",
			"required":    false,
			"description": knob.Summary,
			"schema":      map[string]any{"type": "string"},
		})
	}
	if parameters == nil {
		return []any{}
	}
	return parameters
}

// openAPIRouteResponses renders the 200 and the problems one row can
// answer with. The problem list is the row's own, from the table.
func openAPIRouteResponses(route apiRoute) map[string]any {
	content := map[string]any{}
	for _, form := range route.Serves {
		schema := map[string]any{"type": "string", "format": "binary"}
		if route.Kind == routeDiscovery {
			schema = map[string]any{"$ref": "#/components/schemas/discovery"}
		}
		if route.Kind == routeInfo {
			schema = map[string]any{"$ref": "#/components/schemas/endpoint"}
		}
		content[form.ContentType] = map[string]any{"schema": schema}
	}
	responses := map[string]any{
		"200": map[string]any{
			"description": route.Answers,
			"content":     content,
			"headers":     openAPIResponseHeaders(route),
		},
	}
	for status, reference := range openAPIStatuses(route) {
		responses[status] = map[string]any{"$ref": "#/components/responses/" + reference}
	}
	return responses
}

// lowerFirst starts a sentence's words in the middle of another one.
func lowerFirst(text string) string {
	if text == "" {
		return ""
	}
	return strings.ToLower(text[:1]) + text[1:]
}

// openAPIStatuses maps the row's problem types onto the statuses that
// carry them, plus the statuses every row can answer.
func openAPIStatuses(route apiRoute) map[string]string {
	statuses := map[string]string{
		// parseKnobs runs for every route, so an unknown query
		// parameter is a 400 on all of them.
		"400": "badRequest",
		"401": "unauthorized",
		"403": "forbidden",
		"405": "methodNotAllowed",
		"406": "notAcceptable",
	}
	// Every route that carries an ETag answers If-None-Match with a
	// 304, which is the two documents and the info route.
	if route.Kind != routeTap {
		statuses["304"] = "notModified"
	}
	for _, kind := range route.Problems {
		switch kind {
		case problemNoNode:
			statuses["404"] = "notFound"
		case problemAway:
			statuses["409"] = "away"
		case problemCaptureBusy:
			statuses["503"] = "unavailable"
		case problemUpstreamFailed:
			statuses["502"] = "badGateway"
			statuses["504"] = "gatewayTimeout"
		case problemWrongTarget:
			statuses["500"] = "wrongTarget"
		}
	}
	// An info route reads the object and the node's graph, so it
	// answers everything a tap answers short of the tap's own
	// failures.
	if route.Kind == routeInfo {
		statuses["503"] = "unavailable"
		statuses["502"] = "badGateway"
		statuses["504"] = "gatewayTimeout"
	}
	return statuses
}

// openAPIResponseHeaders names the headers a 200 carries, which differ
// between a document and a tap.
func openAPIResponseHeaders(route apiRoute) map[string]any {
	headers := map[string]any{
		"Vary": openAPIHeader("Always Accept. RFC 9110 section 12.5.5's second purpose: " +
			"this answer was subject to negotiation, and an Accept could have made it a 406."),
		"Link": openAPIHeader("The service-desc and service-doc relations on every answer, " +
			"describedby on every route that names an object, alternate on the " +
			"extensionless route, and related from an info document to its capture routes."),
	}
	if route.Kind == routeTap {
		headers["Cache-Control"] = openAPIHeader("Always no-store. A capture is never cacheable.")
		headers["Accept-Ranges"] = openAPIHeader("Always none. A live capture has no byte " +
			"identity: offset 44 of two requests is two different moments.")
		headers["Content-Disposition"] = openAPIHeader("The save name a browser gets, with " +
			"the RFC 3339 time's colons replaced by dashes.")
		if route.negotiated() {
			headers["Content-Location"] = openAPIHeader("The extension route that serves " +
				"the representation the negotiation chose.")
		}
		return headers
	}
	headers["Cache-Control"] = openAPIHeader("Always no-cache. A client revalidates with " +
		"If-None-Match and gets a 304.")
	headers["ETag"] = openAPIHeader("The build this API was made from. The router table is " +
		"compiled in, so the build is the whole of a document's identity.")
	return headers
}

func openAPIHeader(description string) map[string]any {
	return map[string]any{
		"description": description,
		"schema":      map[string]any{"type": "string"},
	}
}

// openAPISchemas holds the two documents this API answers with.
func openAPISchemas() map[string]any {
	return map[string]any{
		"problem": map[string]any{
			"type": "object",
			"description": "The RFC 9457 problem document every error answers with. detail " +
				"carries the source's own words: pw-record's stderr, an encoder's " +
				"stderr, or the API server's status message. instance is the request " +
				"path, a number sign, and the request id the log line carries.",
			"required": []any{"type", "title", "status", "instance"},
			"properties": map[string]any{
				"type":     map[string]any{"type": "string", "format": "uri"},
				"title":    map[string]any{"type": "string"},
				"status":   map[string]any{"type": "integer"},
				"detail":   map[string]any{"type": "string"},
				"instance": map[string]any{"type": "string"},
				"acceptable": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":     "object",
						"required": []any{"type", "href"},
						"properties": map[string]any{
							"type": map[string]any{"type": "string"},
							"href": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
		"discovery": map[string]any{
			"type":        "object",
			"description": "The discovery document the three capture APIs share.",
			"required":    []any{"resources", "openapi"},
			"properties": map[string]any{
				"resources": map[string]any{"type": "object"},
				"openapi":   map[string]any{"type": "string"},
			},
		},
		"endpoint": map[string]any{
			"type": "object",
			"description": "One endpoint's own document: the node a tap targets, the rate " +
				"and channel count it would use, the format the node reports now, and " +
				"the forms served.",
			"required": []any{"name", "kind", "node", "mediaTypes", "extensions"},
			"properties": map[string]any{
				"name":           map[string]any{"type": "string"},
				"kind":           map[string]any{"type": "string"},
				"node":           map[string]any{"type": "string"},
				"nodeName":       map[string]any{"type": "string"},
				"connectionType": map[string]any{"type": "string"},
				"rate":           map[string]any{"type": "integer"},
				"channels":       map[string]any{"type": "integer"},
				"format":         map[string]any{"type": "object"},
				"mediaTypes":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"extensions":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
	}
}

// openAPISecuritySchemes names the two credentials this API takes.
func openAPISecuritySchemes() map[string]any {
	return map[string]any{
		"mutualTLS": map[string]any{
			"type": "mutualTLS",
			"description": "A client certificate the cluster's own authority signed. " +
				"The subject's common name is the user and its organization values are " +
				"the groups, which is how the API server reads one.",
		},
		"bearerToken": map[string]any{
			"type":         "http",
			"scheme":       "bearer",
			"bearerFormat": "JWT",
			"description": "A ServiceAccount token minted with the audience audio-api, " +
				"which this API checks with a TokenReview.",
		},
	}
}

// openAPIResponses holds every problem answer once, so a path item
// names it rather than repeating it.
func openAPIResponses() map[string]any {
	responses := map[string]any{}
	noBody := map[string]bool{"notModified": true}
	for name, described := range map[string]string{
		"badRequest": "A t= the grammar refuses, a t=a,b with a at or after b, a begin " +
			"over 60 seconds, a repeated dimension, an unknown query parameter, or a " +
			"knob the format does not take.",
		"unauthorized": "No client certificate and no token, or a token the " +
			"TokenReview refuses. The WWW-Authenticate header carries the review's " +
			"own words.",
		"forbidden": "The SubjectAccessReview said no. The WWW-Authenticate header names " +
			"the scope the caller would need.",
		"notFound":         "No Sink or Source of that name, or PipeWire holds no node for it.",
		"methodNotAllowed": "A method other than GET, HEAD, and OPTIONS.",
		"notAcceptable": "Accept excludes every representation the route serves. The " +
			"acceptable member lists what it does serve, with the URI of each.",
		"away":        "The endpoint has no status.node. The detail says to power the device on.",
		"notModified": "The If-None-Match field matches this document's ETag.",
		"wrongTarget": "The tap's link landed on a node other than the one asked for.",
		"badGateway": "The capture container answered something that is not HTTP or not a " +
			"problem document.",
		"unavailable": "The capture container is at its tap limit, refused the connection, " +
			"is not ready, has no certificate this API trusts, or PipeWire refused " +
			"pw-record.",
		"gatewayTimeout": "The capture container sent no headers within the header timeout.",
	} {
		answer := map[string]any{"description": described}
		if !noBody[name] {
			answer["content"] = map[string]any{
				problemType: map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/problem"},
				},
			}
		}
		responses[name] = answer
	}
	return responses
}

// renderOpenAPI writes the document as the bytes both copies hold:
// two-space indent, sorted keys, and a trailing newline, so the served
// copy and the committed copy compare byte for byte.
func renderOpenAPI(document openAPIDocument) ([]byte, error) {
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// openAPIPaths lists the path items a document holds, in order. The
// coverage test reads it to assert that every row of the router table
// is published.
func openAPIPaths(document openAPIDocument) []string {
	paths := make([]string, 0, len(document.Paths))
	for path := range document.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// sameApartFromServers is the equality the committed copy is held to.
// The served copy names the origin it was fetched from, and that one
// member is the only difference a reader may see.
func sameApartFromServers(left, right []byte) (bool, error) {
	strip := func(raw []byte) (map[string]any, error) {
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			return nil, err
		}
		delete(document, "servers")
		return document, nil
	}
	first, err := strip(left)
	if err != nil {
		return false, err
	}
	second, err := strip(right)
	if err != nil {
		return false, err
	}
	firstBytes, err := json.Marshal(first)
	if err != nil {
		return false, err
	}
	secondBytes, err := json.Marshal(second)
	if err != nil {
		return false, err
	}
	return string(firstBytes) == string(secondBytes), nil
}
