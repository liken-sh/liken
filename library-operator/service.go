package main

// A namespace is a boundary, and the agents of one namespace form one
// Corrosion cluster. They find each other through a headless Service
// named catalog in that namespace. The sidecar image bootstraps to the
// short name catalog, and the pod's own search path resolves it to
// the Service in the pod's namespace. The Service names no selector,
// because this operator writes the EndpointSlice behind it, in
// endpoints.go. The operator creates the Service rather than a
// manifest, because no manifest can know which namespaces hold a
// Library.

import (
	"context"
	"errors"
	"maps"
	"slices"
)

// The API group the Service belongs to, and the clusterIP value that
// makes a Service headless. None is the word Kubernetes uses for
// "assign no address": the name resolves to the endpoints themselves.
const (
	serviceAPIVersion = "v1"
	headlessClusterIP = "None"
)

// The fields the operator reads and writes on the Service, and
// nothing else. ClusterIP is None, which makes the Service headless:
// the name resolves to every peer's own address, and no proxy is in
// the gossip path. ClusterIPs is read and written back untouched,
// because the API server fills it and both fields are immutable.
// PublishNotReadyAddresses is set because an agent that is starting is
// still a peer to gossip with, and a peer list that drops every
// not-ready agent is empty at the moment a cluster forms.
type Service struct {
	APIVersion string      `json:"apiVersion,omitempty"`
	Kind       string      `json:"kind,omitempty"`
	Metadata   ObjectMeta  `json:"metadata"`
	Spec       ServiceSpec `json:"spec"`
}

// Selector is omitted where it is empty. The catalog Service must state no
// selector, because a Service that states one takes its endpoints from the
// API server in place of the slice this operator writes.
type ServiceSpec struct {
	ClusterIP                string            `json:"clusterIP,omitempty"`
	ClusterIPs               []string          `json:"clusterIPs,omitempty"`
	PublishNotReadyAddresses bool              `json:"publishNotReadyAddresses"`
	Selector                 map[string]string `json:"selector,omitempty"`
	Ports                    []ServicePort     `json:"ports"`
}

// One port of the Service. The name ties this port to the port of the
// same name on the slice the operator writes. The protocol is UDP
// because Corrosion gossips over QUIC.
type ServicePort struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Port     int32  `json:"port"`
}

// buildCatalogService builds the Service for one namespace. It is a
// function of the namespace and the owners alone, so two passes build
// the same object. The namespace's one Catalog owns the Service, so the
// garbage collector removes it with that Catalog, and the operator needs
// no delete verb.
func buildCatalogService(namespace string, owners []OwnerReference) *Service {
	return &Service{
		APIVersion: serviceAPIVersion,
		Kind:       "Service",
		Metadata: ObjectMeta{
			Name:            catalogServiceName,
			Namespace:       namespace,
			OwnerReferences: owners,
		},
		Spec: ServiceSpec{
			ClusterIP:                headlessClusterIP,
			PublishNotReadyAddresses: true,
			Ports: []ServicePort{
				{Name: catalogPortName, Protocol: catalogPortProtocol, Port: catalogPort},
			},
		},
	}
}

// standCatalogService brings the live Service into line with the one
// this pass built. It writes on divergence only, the same rule
// standCatalogEndpoints follows. A conflict on the create means
// another writer got there first, which is success.
//
// The update writes the live object with the operator's own fields
// set on it, never the object the pass built. clusterIP and clusterIPs
// are immutable and the API server assigned them, so the write carries
// back what the read answered.
func (o *operator) standCatalogService(ctx context.Context, namespace string, owners []OwnerReference) error {
	desired := buildCatalogService(namespace, owners)

	live, err := GetService(ctx, o.client, namespace, catalogServiceName)
	if errors.Is(err, ErrNotFound) {
		_, err := CreateService(ctx, o.client, desired)
		if errors.Is(err, ErrConflict) {
			return nil
		}
		return err
	}
	if err != nil {
		return err
	}

	if sameService(live, desired) {
		return nil
	}
	live.Metadata.OwnerReferences = desired.Metadata.OwnerReferences
	live.Spec.PublishNotReadyAddresses = desired.Spec.PublishNotReadyAddresses
	live.Spec.Selector = desired.Spec.Selector
	live.Spec.Ports = desired.Spec.Ports
	_, err = UpdateService(ctx, o.client, live)
	return err
}

// sameService compares only what the operator states: the owners, that the
// Service is headless, that it publishes not-ready addresses, that it states
// no selector, and the ports. Everything else on a live Service belongs to
// the API server, and a comparison of it would rewrite the object every pass.
//
// The selector is compared because a selector added by hand would make the
// API server write the endpoints, in place of the EndpointSlice this operator
// writes, and the agents would lose their peer list. The update above clears
// it again.
func sameService(live, desired *Service) bool {
	if !slices.Equal(live.Metadata.OwnerReferences, desired.Metadata.OwnerReferences) {
		return false
	}
	if live.Spec.ClusterIP != desired.Spec.ClusterIP {
		return false
	}
	if live.Spec.PublishNotReadyAddresses != desired.Spec.PublishNotReadyAddresses {
		return false
	}
	if !maps.Equal(live.Spec.Selector, desired.Spec.Selector) {
		return false
	}
	return slices.Equal(live.Spec.Ports, desired.Spec.Ports)
}
