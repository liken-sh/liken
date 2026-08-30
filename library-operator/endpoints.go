package main

// Every Corrosion sidecar finds its peers through the short name
// catalog. That name is in the sidecar image's configuration file,
// because Corrosion reads no bootstrap list from the environment. The
// pod's own search path resolves the name to the Service in the pod's
// namespace, in service.go. That Service names no selector, so this
// operator writes the slice behind it, over the scanner pods of that
// namespace and no other. So an agent joins its namespace's cluster
// and no other.

import (
	"errors"
	"reflect"
	"slices"
	"strings"
)

// The API group the slice belongs to, and the address family it
// holds. The address type is one field of the slice, and every address
// in the slice shares it.
const (
	endpointSliceAPIVersion  = "discovery.k8s.io/v1"
	endpointSliceAddressType = "IPv4"
)

// The Service the slice belongs to, and the port the agents gossip
// on. The protocol is UDP because Corrosion gossips over QUIC. The
// Service in service.go is built from these same constants, so the
// port names cannot drift apart.
const (
	catalogServiceName  = "catalog"
	catalogPortName     = "gossip"
	catalogPortProtocol = "UDP"
	catalogPort         = 8787
)

// The two labels the slice carries. The service-name label is how a
// Service's slices are found, and it is the whole tie between this
// slice and the Service, which names no selector. The managed-by
// label names this operator, which keeps the slice controllers in
// kube-controller-manager from rewriting or deleting the slice.
const (
	serviceNameLabel     = "kubernetes.io/service-name"
	managedByLabel       = "endpointslice.kubernetes.io/managed-by"
	endpointSliceManager = "library-operator"
)

// The EndpointSlice, in the same hand-written form as the other
// objects. Endpoints and ports carry no omitempty, because an empty
// endpoints list is the state of a cluster with no scanner pod, and it
// must reach the API server as a list and not as null.
type EndpointSlice struct {
	APIVersion  string         `json:"apiVersion,omitempty"`
	Kind        string         `json:"kind,omitempty"`
	Metadata    ObjectMeta     `json:"metadata"`
	AddressType string         `json:"addressType"`
	Endpoints   []Endpoint     `json:"endpoints"`
	Ports       []EndpointPort `json:"ports"`
}

// One endpoint is one pod. Addresses holds the pod's own address.
// NodeName lets a reader find the endpoints local to a node. TargetRef
// names the pod the address belongs to, so kubectl describe reports
// the pod and not the address alone.
type Endpoint struct {
	Addresses  []string           `json:"addresses"`
	Conditions EndpointConditions `json:"conditions"`
	NodeName   string             `json:"nodeName,omitempty"`
	TargetRef  *ObjectReference   `json:"targetRef,omitempty"`
}

// Ready is the only condition this operator states. The Service
// publishes not-ready addresses too, so an agent that is starting is
// still a peer to gossip with.
type EndpointConditions struct {
	Ready bool `json:"ready"`
}

// An ObjectReference names one object. These are the four fields the
// targetRef of a pod endpoint carries.
type ObjectReference struct {
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	UID       string `json:"uid,omitempty"`
}

// One port of the Service. The name ties this port to the port of the
// same name on the Service.
type EndpointPort struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Port     int32  `json:"port"`
}

// buildCatalogEndpoints builds the slice for one namespace. It is a
// function of the namespace, the owners, and the pods alone, so two
// passes over the same cluster build the same object. The pass hands
// in every scanner pod in the cluster, and this reads only the ones in
// its namespace, which is what keeps one namespace's agents out of
// another's cluster. A pod with no address is not a peer yet, and a
// pod with a deletion timestamp is a peer no longer. The endpoints
// sort by address, so the order the list arrived in never counts as a
// divergence. The owners are every Library in the namespace, so the
// garbage collector removes the slice when the last one goes.
func buildCatalogEndpoints(namespace string, owners []OwnerReference, pods []Pod) *EndpointSlice {
	endpoints := []Endpoint{}
	for index := range pods {
		pod := &pods[index]
		if pod.Metadata.Namespace != namespace {
			continue
		}
		if pod.Status.PodIP == "" || pod.Metadata.DeletionTimestamp != "" {
			continue
		}
		endpoints = append(endpoints, Endpoint{
			Addresses:  []string{pod.Status.PodIP},
			Conditions: EndpointConditions{Ready: true},
			NodeName:   pod.Spec.NodeName,
			TargetRef: &ObjectReference{
				Kind:      "Pod",
				Namespace: pod.Metadata.Namespace,
				Name:      pod.Metadata.Name,
				UID:       pod.Metadata.UID,
			},
		})
	}
	slices.SortFunc(endpoints, func(one, other Endpoint) int {
		return strings.Compare(one.Addresses[0], other.Addresses[0])
	})
	return &EndpointSlice{
		APIVersion: endpointSliceAPIVersion,
		Kind:       "EndpointSlice",
		Metadata: ObjectMeta{
			Name:      catalogServiceName,
			Namespace: namespace,
			Labels: map[string]string{
				serviceNameLabel: catalogServiceName,
				managedByLabel:   endpointSliceManager,
			},
			OwnerReferences: owners,
		},
		AddressType: endpointSliceAddressType,
		Endpoints:   endpoints,
		Ports: []EndpointPort{
			{Name: catalogPortName, Protocol: catalogPortProtocol, Port: catalogPort},
		},
	}
}

// standCatalogEndpoints brings the live slice of one namespace into
// line with the one this pass built. It writes on divergence only: it
// reads the live slice, compares the owners, the endpoints, and the
// ports, and writes only when they differ. That is this project's rule
// for an object a pass rebuilds every ten seconds, because an
// unconditional write wakes every watcher of the object for nothing.
//
// The live slice's resourceVersion makes the write conditional, so a
// slice that something else changed underneath answers a conflict
// instead of being overwritten. A conflict on the create means another
// writer got there first, which is success: the next pass reads what
// that writer wrote.
func (o *operator) standCatalogEndpoints(namespace string, owners []OwnerReference, pods []Pod) error {
	desired := buildCatalogEndpoints(namespace, owners, pods)

	live, err := GetEndpointSlice(o.client, namespace, catalogServiceName)
	if errors.Is(err, ErrNotFound) {
		_, err := CreateEndpointSlice(o.client, desired)
		if errors.Is(err, ErrConflict) {
			return nil
		}
		return err
	}
	if err != nil {
		return err
	}

	if sameEndpoints(live, desired) {
		return nil
	}
	desired.Metadata.ResourceVersion = live.Metadata.ResourceVersion
	_, err = UpdateEndpointSlice(o.client, desired)
	return err
}

// sameEndpoints compares only what this operator states: the owners,
// the endpoints, and the ports. It compares the counts first, so an
// absent list and an empty list read as the same thing. A namespace
// with no scanner pod leaves an absent list behind, and without this
// rule it would be rewritten every pass.
func sameEndpoints(live, desired *EndpointSlice) bool {
	if !slices.Equal(live.Metadata.OwnerReferences, desired.Metadata.OwnerReferences) {
		return false
	}
	if len(live.Endpoints) != len(desired.Endpoints) || len(live.Ports) != len(desired.Ports) {
		return false
	}
	for index := range desired.Endpoints {
		if !reflect.DeepEqual(live.Endpoints[index], desired.Endpoints[index]) {
			return false
		}
	}
	for index := range desired.Ports {
		if live.Ports[index] != desired.Ports[index] {
			return false
		}
	}
	return true
}
