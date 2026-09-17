package main

// This file holds the discovery document, the shape the three liken
// capture APIs share. A client reads from it which resources the API
// serves, which aspect of each it can tap, the RFC 6570 template to
// expand, the media types and extensions it may ask for, and where
// the OpenAPI document is. The shape is the same in every domain so
// one client walks display, audio, and media with one reader.

import "encoding/json"

// resources is keyed by the CRD's plural. Each resource carries its
// self template, the info route, and its aspects.
type discoveryDocument struct {
	Resources map[string]discoveryResource `json:"resources"`
	OpenAPI   string                       `json:"openapi"`
}

type discoveryResource struct {
	Self    string                     `json:"self"`
	Aspects map[string]discoveryAspect `json:"aspects"`
}

// Every aspect carries four members: the template, the media types,
// the extensions, and redirects. redirects is false everywhere in
// this domain, because a Display is the thing itself and stands for
// nothing else; the media API's aspects redirect to a Player's
// screen and sinks, and there it is true.
type discoveryAspect struct {
	Template   string   `json:"template"`
	MediaTypes []string `json:"mediaTypes"`
	Extensions []string `json:"extensions"`
	Redirects  bool     `json:"redirects"`
}

// The document is built from the same forms and templates the router
// matches, so a route that is served is a route that is published.
func discovery() discoveryDocument {
	extensions := make([]string, 0, len(screenForms))
	for _, form := range screenForms {
		extensions = append(extensions, form.ext)
	}
	return discoveryDocument{
		Resources: map[string]discoveryResource{
			displaysPlural: {
				Self: displaySelfTemplate(),
				Aspects: map[string]discoveryAspect{
					screenAspect: {
						Template:   screenTemplate,
						MediaTypes: screenMediaTypes(),
						Extensions: extensions,
						Redirects:  false,
					},
				},
			},
		},
		OpenAPI: apiRoot + "/openapi.json",
	}
}

// Every document this API serves is indented, so a person reads it
// from curl with no tool.
func renderJSON(value any) ([]byte, error) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// What the info route answers: the screen's own numbers, read from
// the sidecar's wl_output events, with the name and node the API
// adds. The links to the routes that capture it go in the Link
// fields, not in the body.
type screenInfo struct {
	Name    string   `json:"name"`
	Node    string   `json:"node"`
	Width   int      `json:"width"`
	Height  int      `json:"height"`
	Scale   int      `json:"scale,omitempty"`
	Refresh int      `json:"refresh"`
	Formats []string `json:"formats,omitempty"`
	// What is wrong with the screen, when something is. A screen that
	// answers carries neither: compositor is "down" for a screen whose
	// compositor is not serving, sidecar is "unreachable" for a node
	// this API cannot call, and detail carries the words of whichever
	// one it is.
	Compositor string `json:"compositor,omitempty"`
	Sidecar    string `json:"sidecar,omitempty"`
	Detail     string `json:"detail,omitempty"`
	// The graph the node encodes with, vaapi or software. A node
	// whose driver has no VA-API post-processing converts on the CPU,
	// which costs four times the cores at 1080p, so the document
	// names it.
	Conversion string `json:"conversion,omitempty"`
}
