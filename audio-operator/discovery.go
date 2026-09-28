package main

// The discovery document at GET /v1/audio, and the info document each
// endpoint's own route answers.
//
// The discovery shape is the one the three capture APIs share. Every
// template in it is RFC 6570: simple expansion for the name (section
// 3.2.2), label expansion for the extension (3.2.5), and form-style
// query expansion for the knobs (3.2.8).
//
// redirects is present on every aspect. It is false on both of this
// domain's, because an audio route answers with the sound rather than
// pointing at another API.

// discoveryDocument is the whole of GET /v1/audio.
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
}

// newDiscovery reads the router table, so a route that is added there
// appears here with no second edit.
func newDiscovery() discoveryDocument {
	document := discoveryDocument{
		Resources: map[string]discoveryResource{},
		OpenAPI:   apiPrefix + "/openapi.json",
	}
	for _, resource := range apiResources {
		aspect := discoveryAspect{
			Template:  aspectTemplate(resource, audioAspect),
			Redirects: false,
		}
		for _, form := range audioRepresentations {
			aspect.MediaTypes = append(aspect.MediaTypes, form.MediaType)
			aspect.Extensions = append(aspect.Extensions, form.Extension)
		}
		document.Resources[resource] = discoveryResource{
			Self:    apiPrefix + "/" + resource + "/{name}",
			Aspects: map[string]discoveryAspect{audioAspect: aspect},
		}
	}
	return document
}

// endpointDocument is what GET /v1/audio/sinks/{name} answers: the node
// a tap would target, how the endpoint is attached, the rate and
// channel count a tap would use, the format the node reports now, and
// the forms served.
//
// Rate and Channels are what the container read from the graph.
// Format is status.format as the operator last reported it, which is
// absent while the node is suspended. A tap on a suspended node still
// answers, with silence at Rate, because the route is "what the
// speakers play now" and silence is that answer.
type endpointDocument struct {
	Name           string          `json:"name"`
	Kind           string          `json:"kind"`
	Node           string          `json:"node"`
	NodeName       string          `json:"nodeName"`
	ConnectionType string          `json:"connectionType,omitempty"`
	Rate           int             `json:"rate,omitempty"`
	Channels       int             `json:"channels,omitempty"`
	Format         *EndpointFormat `json:"format,omitempty"`
	MediaTypes     []string        `json:"mediaTypes"`
	Extensions     []string        `json:"extensions"`
}

// newEndpointDocument composes one endpoint's answer from the object
// the API server holds and the format the capture container read from
// the graph.
func newEndpointDocument(kind, name string, status EndpointStatus, format captureFormat) endpointDocument {
	document := endpointDocument{
		Name:           name,
		Kind:           kind,
		Node:           status.Node,
		NodeName:       status.NodeName,
		ConnectionType: status.ConnectionType,
		Rate:           format.Rate,
		Channels:       format.Channels,
		Format:         status.Format,
	}
	for _, form := range audioRepresentations {
		document.MediaTypes = append(document.MediaTypes, form.MediaType)
		document.Extensions = append(document.Extensions, form.Extension)
	}
	return document
}
