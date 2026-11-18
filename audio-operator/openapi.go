package main

// The OpenAPI document is built from the route table in apiroutes.go,
// which the router also reads, so the document cannot differ from the
// routes that the API serves.
//
// OpenAPI has no equivalent of the RFC 6570 {.ext} expansion that the
// discovery document uses, so each extension path is its own path item.
//
// The served copy sets servers to the configured public base, or to the
// origin of the request when none is configured. The copy committed at
// docs/static/v1/audio/openapi.json names a placeholder origin, and a
// test checks that the two copies differ only in servers.

import (
	"encoding/json"
	"sort"
	"strings"
)

// A reader of the committed copy replaces this origin with the one
// where they reach the API.
const openAPIPlaceholderServer = "https://audio-api.liken-system.svc"

// OpenAPI 3.1 uses JSON Schema 2020-12 for its schemas.
const openAPIVersion = "3.1.1"

// Paths and the components are maps, and encoding/json writes map keys
// in sorted order, so the rendered bytes are the same on every run.
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

// info.version is the version of the API, not of the build, because a
// build version would make the committed copy differ from the rendered
// one on every build. A client reads the build version from the ETag.
func newOpenAPI(server string) openAPIDocument {
	document := openAPIDocument{
		OpenAPI: openAPIVersion,
		Info: openAPIInfo{
			Title:   "audio.liken.sh capture API",
			Version: "v1",
			Summary: "Streams the sound of a `Sink` or a `Source` as " +
				"WAV, FLAC, or Ogg Opus.",
			Description: "The path names the `Sink` or `Source` to tap. You choose the format " +
				"with the extension of the path or with the `Accept` header, and a W3C " +
				"Media Fragments `t=` query parameter limits the time span. Authenticate " +
				"with a client certificate that the cluster's certificate authority " +
				"signed, or with a `ServiceAccount` token that has the audience " +
				"`audio-api`. A tap requires `get` on `sinks/audio` or `sources/audio` " +
				"in the `audio.liken.sh` API group. The capture container on the node that " +
				"has the `Sink` or `Source` records the sound, and nothing stores it.",
		},
		Servers: []openAPIServer{{URL: server}},
		Paths:   map[string]any{},
		Comps: openAPIComponents{
			Schemas:         openAPISchemas(),
			SecuritySchemes: openAPISecuritySchemes(),
			Responses:       openAPIResponses(),
		},
		// The entries are alternatives, so a request needs either the
		// client certificate or the bearer token.
		Secure: []map[string][]any{{"mutualTLS": {}}, {"bearerToken": {}}},
	}
	for _, route := range apiRoutes {
		document.Paths[openAPIPath(route)] = openAPIPathItem(route)
	}
	return document
}

// OpenAPI path templating and RFC 6570 simple expansion both write a
// variable as {name}, so the template needs no conversion.
func openAPIPath(route apiRoute) string {
	return route.Template
}

func openAPIPathItem(route apiRoute) map[string]any {
	operation := map[string]any{
		"operationId": openAPIOperationID(route),
		"summary":     route.Summary,
		"parameters":  openAPIParameters(route),
		"responses":   openAPIRouteResponses(route),
	}
	item := map[string]any{
		"get": operation,
		"head": map[string]any{
			"operationId": openAPIOperationID(route) + "Head",
			"summary": "The headers that GET returns for this route, with no body. HEAD " +
				"records no sound and makes no request to the capture container " +
				"(RFC 9110 section 9.3.2).",
			"parameters": openAPIParameters(route),
			"responses":  openAPIRouteResponses(route),
		},
		"options": map[string]any{
			"operationId": openAPIOperationID(route) + "Options",
			"summary": "Returns 204 with an `Allow` header that lists the methods this " +
				"route accepts (RFC 9110 section 10.2.1).",
			"responses": map[string]any{
				"204": map[string]any{
					"description": "No body. The `Allow` header lists the methods.",
					"headers": map[string]any{
						"Allow": map[string]any{
							"description": "Always `GET, HEAD, OPTIONS`.",
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
			"description": "The name of the `Sink` or `Source`. The operator builds the name " +
				"from the hardware identity of the device.",
			"schema": map[string]any{"type": "string"},
		}}
	}
	return item
}

// openAPIOperationID returns the operationId of a route's GET, such as
// getSinkAudioWav, which a client generator uses as the method name.
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

// openAPIParameters never returns nil, because a nil slice marshals as
// null and OpenAPI requires an array.
func openAPIParameters(route apiRoute) []any {
	var parameters []any
	for _, parameter := range route.Query {
		if !route.accepts(parameter.Name) {
			continue
		}
		parameters = append(parameters, map[string]any{
			"name":        parameter.Name,
			"in":          "query",
			"required":    false,
			"description": parameter.Summary,
			"schema":      map[string]any{"type": "string"},
		})
	}
	if parameters == nil {
		return []any{}
	}
	return parameters
}

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
			"description": route.Summary,
			"content":     content,
			"headers":     openAPIResponseHeaders(route),
		},
	}
	for status, reference := range openAPIStatuses(route) {
		responses[status] = map[string]any{"$ref": "#/components/responses/" + reference}
	}
	return responses
}

// lowerFirst lets a route's summary continue a sentence.
func lowerFirst(text string) string {
	if text == "" {
		return ""
	}
	return strings.ToLower(text[:1]) + text[1:]
}

// openAPIStatuses maps each status that a route can return to the name
// of its entry in components.responses.
func openAPIStatuses(route apiRoute) map[string]string {
	statuses := map[string]string{
		// parseCaptureQuery runs for every route, so an unknown query
		// parameter is a 400 on every route.
		"400": "badRequest",
		"401": "unauthorized",
		"403": "forbidden",
		"405": "methodNotAllowed",
		"406": "notAcceptable",
	}
	// Every route except a tap has an ETag, and returns 304 when
	// If-None-Match matches it.
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
	// An info route reads the Sink or Source from the API server and the
	// format from the capture container, so it can fail with 502, 503,
	// and 504 as a tap does. Only a tap returns 500.
	if route.Kind == routeInfo {
		statuses["503"] = "unavailable"
		statuses["502"] = "badGateway"
		statuses["504"] = "gatewayTimeout"
	}
	return statuses
}

func openAPIResponseHeaders(route apiRoute) map[string]any {
	headers := map[string]any{
		"Vary": openAPIHeader("Always `Accept`. The response depends on negotiation, because an " +
			"`Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5)."),
		"Link": openAPIHeader("Every response has the `service-desc` and `service-doc` " +
			"relations. A route that names an object adds `describedby`. The route with no " +
			"extension adds `alternate` for each format, and the info document of a `Sink` or " +
			"`Source` adds `related` for each tap route."),
	}
	if route.Kind == routeTap {
		headers["Cache-Control"] = openAPIHeader("Always `no-store`, because a live capture never returns the same bytes twice.")
		headers["Accept-Ranges"] = openAPIHeader("Always `none`, because a live capture has no fixed " +
			"bytes: byte 44 of two requests comes from two different moments.")
		headers["X-Content-Type-Options"] = openAPIHeader("Always `nosniff`, so a browser uses the " +
			"`Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6).")
		headers["Content-Disposition"] = openAPIHeader("The file name that a browser saves: `<name>-<time>.<ext>`, " +
			"where the time is RFC 3339 UTC with the colons replaced by dashes.")
		if route.negotiated() {
			headers["Content-Location"] = openAPIHeader("The extension route that serves " +
				"the format that negotiation chose.")
		}
		return headers
	}
	headers["Cache-Control"] = openAPIHeader("Always `no-cache`. A client revalidates with " +
		"`If-None-Match` and gets a 304.")
	headers["ETag"] = openAPIHeader("The version of the build of this API. The route table is " +
		"compiled in, so the build version identifies the document.")
	return headers
}

func openAPIHeader(description string) map[string]any {
	return map[string]any{
		"description": description,
		"schema":      map[string]any{"type": "string"},
	}
}

func openAPISchemas() map[string]any {
	return map[string]any{
		"problem": map[string]any{
			"type": "object",
			"description": "The RFC 9457 problem document that every error response contains. " +
				"`detail` contains the error text of the part that failed, word for word: the `stderr` of " +
				"`pw-record` or of an encoder, or the status message of the API server. " +
				"`instance` is the request path, a `#`, and the request ID, which the log " +
				"line of the request also has.",
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
			"description": "The discovery document. The audio, display, and media capture APIs use the same shape.",
			"required":    []any{"resources", "openapi"},
			"properties": map[string]any{
				"resources": map[string]any{"type": "object"},
				"openapi":   map[string]any{"type": "string"},
			},
		},
		"endpoint": map[string]any{
			"type": "object",
			"description": "The info document of a `Sink` or `Source`: the node that a tap runs " +
				"on, the rate and channel count that a tap uses, the format that the node " +
				"reports now, and the media types and extensions that the capture routes " +
				"serve.",
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

func openAPISecuritySchemes() map[string]any {
	return map[string]any{
		"mutualTLS": map[string]any{
			"type": "mutualTLS",
			"description": "A client certificate that the cluster's own certificate authority " +
				"signed. The subject's common name is the user name and its organization " +
				"values are the groups, as the Kubernetes API server reads a certificate.",
		},
		"bearerToken": map[string]any{
			"type":         "http",
			"scheme":       "bearer",
			"bearerFormat": "JWT",
			"description": "A `ServiceAccount` token with the audience `audio-api`. This API " +
				"validates it with a `TokenReview`.",
		},
	}
}

// openAPIResponses returns the problem responses that path items refer
// to with $ref.
func openAPIResponses() map[string]any {
	responses := map[string]any{}
	noBody := map[string]bool{"notModified": true}
	for name, described := range map[string]string{
		"badRequest": "The `t=` value does not match the grammar, `t=a,b` has `a` at or " +
			"after `b`, the begin is over 60 seconds, a dimension is repeated, a query " +
			"parameter is unknown, or the format does not accept the parameter.",
		"unauthorized": "The request has no client certificate and no token, or the " +
			"`TokenReview` refuses the token. The `WWW-Authenticate` header contains " +
			"the review's message.",
		"forbidden": "The `SubjectAccessReview` denied the request. The " +
			"`WWW-Authenticate` header names the scope that the caller needs.",
		"notFound":         "No `Sink` or `Source` has that name, or PipeWire has no node for it.",
		"methodNotAllowed": "The method is not GET, HEAD, or OPTIONS.",
		"notAcceptable": "`Accept` excludes every representation that the route serves. The " +
			"`acceptable` member lists the representations that it does serve, each " +
			"with its URI.",
		"away":        "The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device.",
		"notModified": "`If-None-Match` matches the `ETag` of this document.",
		"wrongTarget": "The tap connected to a node other than the one that the request asked for.",
		"badGateway": "The capture container returned a response that is not HTTP, or an " +
			"error response that is not a problem document.",
		"unavailable": "The API could not read the `Sink` or `Source` from the API server, " +
			"the node has no Ready capture container, or the capture container is at its " +
			"tap limit, refused the connection, has no certificate that this API trusts, " +
			"or could not read the PipeWire graph, or PipeWire refused `pw-record`.",
		"gatewayTimeout": "The capture container sent no response headers within the header timeout.",
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

// renderOpenAPI writes the format that both the served copy and the
// committed copy use.
func renderOpenAPI(document openAPIDocument) ([]byte, error) {
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func openAPIPaths(document openAPIDocument) []string {
	paths := make([]string, 0, len(document.Paths))
	for path := range document.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

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
