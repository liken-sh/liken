package main

// The discovery document lists what this API serves: for each resource,
// the aspects a client can capture, with the RFC 6570 URL template, the
// media types, and the file extensions of each, and the location of the
// OpenAPI document. The audio and media APIs serve a document of the
// same shape, so one client can read all three.

import "encoding/json"

// Resources is keyed by the CRD's plural name. Each resource has the
// template for its info route (self) and its aspects.
type discoveryDocument struct {
	Resources map[string]discoveryResource `json:"resources"`
	OpenAPI   string                       `json:"openapi"`
}

type discoveryResource struct {
	Self    string                     `json:"self"`
	Aspects map[string]discoveryAspect `json:"aspects"`
}

// Redirects is false for every aspect in this API, because a Display
// is captured directly. The media API sets it to true, because its
// aspects redirect to a Player's screen and sinks.
type discoveryAspect struct {
	Template   string   `json:"template"`
	MediaTypes []string `json:"mediaTypes"`
	Extensions []string `json:"extensions"`
	Redirects  bool     `json:"redirects"`
}

// discovery builds the document from the same forms and templates that
// the router matches, so every route the API serves is listed.
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

// renderJSON indents the output so that curl shows readable JSON.
func renderJSON(value any) ([]byte, error) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// screenInfo is the body of the info route: the screen's size and
// refresh rate, read from the sidecar's wl_output events, plus the
// name and node that the API adds. Links to the capture routes are
// sent in Link headers, not in the body.
type screenInfo struct {
	Name    string   `json:"name"`
	Node    string   `json:"node"`
	Width   int      `json:"width"`
	Height  int      `json:"height"`
	Scale   int      `json:"scale,omitempty"`
	Refresh int      `json:"refresh"`
	Formats []string `json:"formats,omitempty"`
	// Codecs is the RFC 6381 codecs parameter of this screen's
	// screen.mp4 capture, for a client that must choose a decoder
	// before it requests the capture. The capture's Content-Type has
	// the same value.
	Codecs string `json:"codecs,omitempty"`
	// Panel, Compositor, and Sidecar are set only when the screen has
	// a problem, and Detail describes it. Panel is "disconnected" when
	// no monitor is on the connector. Compositor is "down" when the
	// compositor is not serving. Sidecar is "unreachable" when the API
	// cannot call the node's sidecar.
	Panel      string `json:"panel,omitempty"`
	Compositor string `json:"compositor,omitempty"`
	Sidecar    string `json:"sidecar,omitempty"`
	Detail     string `json:"detail,omitempty"`
	// Conversion is the filter graph the node encodes with, vaapi or
	// software. A node whose driver has no VA-API post-processing
	// converts frames on the CPU, which costs much more CPU, so the
	// info route reports it.
	Conversion string `json:"conversion,omitempty"`
}
