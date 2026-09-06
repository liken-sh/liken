package main

// The progress cluster's Service and EndpointSlice. The agents of one
// namespace's progress cluster find each other through the short name
// progress, which the image's own configuration bootstraps to and the
// pod's search path resolves to the Service in the pod's namespace.
//
// The catalog cluster and the progress cluster stand side by side in
// every namespace and never mix. They differ in the name their agents
// bootstrap to and the port they gossip on, so the builders in
// service.go and endpoints.go serve both, and this file names the
// progress cluster's half of the pair.

import "context"

// The Service the progress agents bootstrap to, and the port they
// gossip on. The port is one above the catalog cluster's, so a screen
// pod that runs both agents binds two ports and joins two clusters.
const (
	progressServiceName = "progress"
	progressPort        = 8788
)

// The progress cluster, as the builders in service.go and endpoints.go
// take it.
var progressGossip = gossipCluster{
	service:   progressServiceName,
	port:      progressPort,
	container: progressContainer,
}

// The progress Service of one namespace, owned by the namespace's one
// Catalog, so the garbage collector removes it with that Catalog.
func buildProgressService(namespace string, owners []OwnerReference) *Service {
	return buildGossipService(progressGossip, namespace, owners)
}

func (o *operator) standProgressService(ctx context.Context, namespace string, owners []OwnerReference) error {
	return o.standGossipService(ctx, progressGossip, namespace, owners)
}

// The slice behind that Service, written over the pods of the namespace
// that hold a progress agent. The pass hands in every pod in the
// cluster that carries the progress member label.
func buildProgressEndpoints(namespace string, owners []OwnerReference, members []Pod) *EndpointSlice {
	return buildGossipEndpoints(progressGossip, namespace, owners, members)
}

func (o *operator) standProgressEndpoints(ctx context.Context, namespace string, owners []OwnerReference, members []Pod) error {
	return o.standGossipEndpoints(ctx, progressGossip, namespace, owners, members)
}
