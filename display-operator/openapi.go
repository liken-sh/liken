package main

// This file generates the OpenAPI 3.1 document of display-api from
// apiRoutes, the table the router matches, so the document lists every
// route the router serves. OpenAPI 3.1 has no {.ext} path expansion,
// so each extension form is its own path item.

import (
	"fmt"
	"net/http"
	"os"
	"slices"
)

const openAPIVersion = "3.1.1"

// openAPIPlaceholderServer is the server URL in the copy of the
// document in the manual. The API serves the document with the URL
// from requestOrigin instead.
const openAPIPlaceholderServer = "/"

// openapiMode is the argument that makes the binary print the document
// and exit. docs/Makefile runs it to write the copy in the manual.
const openapiMode = "openapi"

func printOpenAPI() {
	body, err := renderJSON(openAPIDocument(openAPIPlaceholderServer))
	if err != nil {
		fatal("rendering the OpenAPI document: %v", err)
	}
	_, _ = os.Stdout.Write(body)
}

func openAPIDocument(server string) map[string]any {
	paths := map[string]any{}
	for _, route := range apiRoutes {
		paths[route.template] = pathItem(route)
	}
	return map[string]any{
		"openapi": openAPIVersion,
		"info": map[string]any{
			"title":       "display-api",
			"version":     version,
			"summary":     "The screen of every Display in this cluster, as one frame, a clip, or a stream.",
			"description": "The path names the Display. The extension or Accept header selects the format. The query can select a region and time range with W3C Media Fragments 1.0 syntax. The API stores no capture. The manual is at https://liken.sh/display/docs/reference/api/.",
		},
		"servers": []any{map[string]any{
			"url":         server,
			"description": "The base URL of this API.",
		}},
		// Either scheme alone authenticates a request. The API checks a
		// client certificate first and then a token.
		"security": []any{
			map[string]any{"mutualTLS": []any{}},
			map[string]any{"bearer": []any{}},
		},
		"paths": paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"mutualTLS": map[string]any{
					"type":        "mutualTLS",
					"description": "A client certificate the cluster's own authority signed. The subject's common name is the user and its organization values are the groups, which is how the API server reads one.",
				},
				"bearer": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
					"description":  "A Kubernetes ServiceAccount token whose audience is display-api.",
				},
			},
			"schemas": map[string]any{
				"Problem": problemSchema(),
			},
		},
	}
}

func pathItem(route apiRoute) map[string]any {
	item := map[string]any{
		"get":     operation(route, http.MethodGet),
		"head":    operation(route, http.MethodHead),
		"options": optionsOperation(route),
	}
	if parameters := routeParameters(route); len(parameters) > 0 {
		item["parameters"] = parameters
	}
	return item
}

func operation(route apiRoute, method string) map[string]any {
	return map[string]any{
		"operationId": operationID(route, method),
		"summary":     route.summary,
		"responses":   responses(route),
	}
}

func optionsOperation(route apiRoute) map[string]any {
	return map[string]any{
		"operationId": operationID(route, http.MethodOptions),
		"summary":     "The methods this route allows",
		"responses": map[string]any{
			"204": map[string]any{
				"description": "The methods are in the Allow field (RFC 9110 section 10.2.1).",
				"headers": map[string]any{
					"Allow": headerSpec("The methods this route allows."),
				},
			},
		},
	}
}

// operationID depends only on the method and the route template, so
// an ID changes only when its route does.
func operationID(route apiRoute, method string) string {
	name := ""
	for _, char := range route.template {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
			name += string(char)
		case char == '/', char == '.', char == '{', char == '}':
			name += "_"
		}
	}
	return method + name
}

func routeParameters(route apiRoute) []any {
	parameters := []any{}
	if route.kind != documentRoute {
		parameters = append(parameters, map[string]any{
			"name":        "name",
			"in":          "path",
			"required":    true,
			"description": "The name of the Display.",
			"schema":      map[string]any{"type": "string"},
		})
	}
	if route.kind != captureRoute {
		return parameters
	}
	accepted := routeKnobs(route)
	for _, knob := range []struct{ name, kind, note string }{
		{"t", "string", "A W3C Media Fragments 1.0 time range in NPT. Zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two."},
		{"xywh", "string", "A W3C Media Fragments 1.0 frame region. Use pixel: by default or percent: to express the values as percentages. Pixel values use the frame's physical pixels."},
		{"width", "integer", "Scale the region down to this width, keeping the aspect. A value above the source is refused."},
		{"height", "integer", "Scale the region down to this height, keeping the aspect. Given together with width it is refused."},
		{"framerate", "integer", "Frames per second of a clip or a stream, 15 by default, at most the output's refresh."},
		{"quality", "integer", "JPEG quality from 1 to 100, 85 by default."},
	} {
		if !slices.Contains(accepted, knob.name) {
			continue
		}
		parameters = append(parameters, map[string]any{
			"name":        knob.name,
			"in":          "query",
			"required":    false,
			"description": knob.note,
			"schema":      map[string]any{"type": knob.kind},
		})
	}
	return parameters
}

// routeKnobs returns the query parameters a capture route accepts. The
// API rejects framerate on a still image and quality on a PNG or an MP4
// with a 400.
func routeKnobs(route apiRoute) []string {
	knobs := []string{"t", "xywh", "width", "height"}
	if route.ext == "" || route.ext == "mp4" || route.ext == "mjpeg" {
		knobs = append(knobs, "framerate")
	}
	if route.ext == "" || route.ext == "jpg" || route.ext == "mjpeg" {
		knobs = append(knobs, "quality")
	}
	return knobs
}

// responses returns the status codes a route can return. They must
// match the error table in the manual.
func responses(route apiRoute) map[string]any {
	statuses := map[string]any{
		"200": okResponse(route),
		"401": problemResponse("No client certificate and no token, or a token the TokenReview refused."),
		"405": problemResponse("A method other than GET, HEAD and OPTIONS."),
		"406": problemResponse("The Accept field excludes every form this route serves."),
	}
	if route.kind == documentRoute || route.kind == infoRoute {
		statuses["304"] = map[string]any{
			"description": "The document has not changed since the entity tag the client holds.",
		}
	}
	if route.kind == documentRoute {
		return statuses
	}
	statuses["403"] = problemResponse("The SubjectAccessReview refused the subject.")
	statuses["404"] = problemResponse("No Display of that name.")
	statuses["503"] = problemResponse("The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed).")
	if route.kind == infoRoute {
		return statuses
	}
	statuses["400"] = problemResponse("A query the grammar refuses.")
	statuses["409"] = problemResponse("The screen has no panel on its connector (away). A retry does not help until the monitor is connected.")
	statuses["500"] = problemResponse("The compositor denied the capture (capture-denied), or the encoder wrote no picture (encoder-failed).")
	statuses["502"] = problemResponse("The sidecar responded with something that is not HTTP or not a problem document.")
	statuses["504"] = problemResponse("The sidecar sent no headers within the header timeout.")
	return statuses
}

func okResponse(route apiRoute) map[string]any {
	content := map[string]any{}
	types := []string{route.mediaType}
	if route.kind == captureRoute && route.mediaType == "" {
		types = screenMediaTypes()
	}
	for _, mediaType := range types {
		content[mediaType] = map[string]any{
			"schema": map[string]any{"type": "string", "format": "binary"},
		}
	}
	if route.kind != captureRoute {
		for _, mediaType := range types {
			content[mediaType] = map[string]any{"schema": map[string]any{"type": "object"}}
		}
	}
	return map[string]any{
		"description": route.responseDescription,
		"content":     content,
		"headers":     okHeaders(route),
	}
}

func okHeaders(route apiRoute) map[string]any {
	headers := map[string]any{
		"Vary": headerSpec("Accept. The response was subject to negotiation (RFC 9110 section 12.5.5)."),
		"Link": headerSpec("The service-desc, service-doc and describedby relations (RFC 8288)."),
	}
	if route.kind == captureRoute {
		headers["Cache-Control"] = headerSpec("no-store. A capture is never stored (RFC 9111 section 5.2.2.5).")
		headers["Content-Disposition"] = headerSpec("inline, with the file name a browser save gets (RFC 6266 section 4).")
		headers["Accept-Ranges"] = headerSpec("none. The bytes of a capture differ on each request, so a byte range has no meaning (RFC 9110 section 14.3).")
		headers["X-Content-Type-Options"] = headerSpec("nosniff. A browser uses the Content-Type and does not guess a type from the bytes (Fetch Standard section 3.6).")
		if route.ext == "" {
			headers["Content-Location"] = headerSpec("The absolute path of the extension form served (RFC 9110 section 8.7).")
		}
		return headers
	}
	headers["ETag"] = headerSpec("The build this document came from.")
	headers["Cache-Control"] = headerSpec("no-cache. A document is revalidated against its entity tag.")
	return headers
}

func toAnySlice(values []string) []any {
	converted := make([]any, 0, len(values))
	for _, value := range values {
		converted = append(converted, value)
	}
	return converted
}

func headerSpec(note string) map[string]any {
	return map[string]any{
		"description": note,
		"schema":      map[string]any{"type": "string"},
	}
}

func problemResponse(note string) map[string]any {
	return map[string]any{
		"description": note,
		"content": map[string]any{
			problemMediaType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Problem"},
			},
		},
	}
}

// problemSchema describes the RFC 9457 problem document, plus
// acceptable, an extension member that a 406 response adds.
func problemSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"type", "title", "status", "instance"},
		"properties": map[string]any{
			"type": map[string]any{
				"type":   "string",
				"format": "uri",
				"enum":   toAnySlice(problemTypes()),
			},
			"title":    map[string]any{"type": "string"},
			"status":   map[string]any{"type": "integer"},
			"detail":   map[string]any{"type": "string"},
			"instance": map[string]any{"type": "string"},
			"acceptable": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type": map[string]any{"type": "string"},
						"href": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}

// requestOrigin returns the server URL for the served document: the
// configured public base, or else the scheme and host of the request,
// so a client on a port-forward sees localhost.
func requestOrigin(r *http.Request, configured string) string {
	if configured != "" {
		return configured
	}
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}
