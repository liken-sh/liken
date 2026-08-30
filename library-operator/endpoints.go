package main

// Every Corrosion sidecar finds its peers through one name,
// catalog.liken-system.svc:8787. That name is in the sidecar image's
// configuration file, because Corrosion reads no bootstrap list from
// the environment. A headless Service with a selector cannot serve
// that name: a selector sees only its own namespace, and a Library
// can be in any namespace. So the Service carries no selector, and this
// operator writes the EndpointSlice behind it: every scanner pod that
// has an address, wherever it runs.

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
// port name matches the port name in deploy/catalog-service.yaml.
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

// buildCatalogEndpoints builds the slice the scanner pods become. It
// is a function of the namespace and the pods alone, so two passes
// over the same pods build the same object. A pod with no address is
// not a peer yet, and a pod with a deletion timestamp is a peer no
// longer. The endpoints sort by address, so the order the list arrived
// in never counts as a divergence.
func buildCatalogEndpoints(namespace string, pods []Pod) *EndpointSlice {
	endpoints := []Endpoint{}
	for index := range pods {
		pod := &pods[index]
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
		},
		AddressType: endpointSliceAddressType,
		Endpoints:   endpoints,
		Ports: []EndpointPort{
			{Name: catalogPortName, Protocol: catalogPortProtocol, Port: catalogPort},
		},
	}
}

// standCatalogEndpoints brings the live slice into line with the one
// this pass built. It writes on divergence only: it reads the live
// slice, compares the endpoints and the ports, and writes only when
// they differ. That is this project's rule for an object a pass
// rebuilds every ten seconds, because an unconditional write wakes
// every watcher of the object for nothing.
//
// The live slice's resourceVersion makes the write conditional, so a
// slice that something else changed underneath answers a conflict
// instead of being overwritten. A conflict on the create means another
// writer got there first, which is success: the next pass reads what
// that writer wrote.
func (o *operator) standCatalogEndpoints(pods []Pod) error {
	desired := buildCatalogEndpoints(o.namespace, pods)

	live, err := GetEndpointSlice(o.client, o.namespace, catalogServiceName)
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

// sameEndpoints compares only what this operator states: the
// endpoints and the ports. It compares the counts first, so an absent
// list and an empty list read as the same thing. A cluster with no
// scanner pod leaves an absent list behind, and without this rule it
// would be rewritten every pass.
func sameEndpoints(live, desired *EndpointSlice) bool {
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
