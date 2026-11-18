package main

// The discovery document has the same shape as those of media-operator
// and display-operator. Every aspect has a redirects field, which is
// false here because an audio route returns sound and does not redirect
// to another API.

// discoveryDocument is the body of GET /v1/audio.
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

// endpointDocument is the body of GET /v1/audio/sinks/{name} and
// /v1/audio/sources/{name}. Rate and Channels are what the capture
// container read from the PipeWire graph. Format is status.format,
// which is absent while the node is suspended. A tap on a suspended
// node still returns silence at Rate, because a suspended node plays
// silence.
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
