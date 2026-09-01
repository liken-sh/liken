package main

// Every Corrosion sidecar finds its peers through the short name
// catalog. That name is in the sidecar image's configuration file,
// because Corrosion reads no bootstrap list from the environment. The
// pod's own search path resolves the name to the Service in the pod's
// namespace, in service.go.
//
// that Service names no selector, so this operator writes the slice
// behind it, over the scanner pods and the screen pods of that namespace and
// no others. So an agent joins its namespace's cluster and no other.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

// Ready is the only condition this operator states, and it carries the
// kubelet's own verdict on the pod. The Service publishes not-ready
// addresses too, so an agent that is starting is still a peer to gossip
// with, and the slice still says which peers are up.
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

// BuildCatalogEndpoints builds the slice for one namespace. It is a
// function of the namespace, the owners, and the pods alone, so two passes
// over the same cluster build the same object. The pass hands in every scanner
// pod and every screen pod in the cluster, and this reads only the ones in its
// namespace, which is what keeps one namespace's agents out of another's
// cluster. Both kinds are peers: a screen's agent gossips like a scanner's, so
// a screen is a bootstrap peer for the next screen when no scanner is up. A
// pod with no address is not a peer yet, and a pod with a deletion timestamp
// is a peer no longer. The endpoints sort by address, so the order the lists
// arrived in never counts as a divergence. The namespace's one Catalog owns
// the slice, so the garbage collector removes it with that Catalog.
func buildCatalogEndpoints(namespace string, owners []OwnerReference, scanners, screens []Pod) *EndpointSlice {
	endpoints := []Endpoint{}
	peers := slices.Concat(scanners, screens)
	for index := range peers {
		pod := &peers[index]
		if pod.Metadata.Namespace != namespace {
			continue
		}
		if pod.Status.PodIP == "" || pod.Metadata.DeletionTimestamp != "" {
			continue
		}
		endpoints = append(endpoints, Endpoint{
			Addresses:  []string{pod.Status.PodIP},
			Conditions: EndpointConditions{Ready: everyContainerReady(pod)},
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

// StandCatalogEndpoints brings the live slice of one namespace into
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
func (o *operator) standCatalogEndpoints(ctx context.Context, namespace string, owners []OwnerReference, scanners, screens []Pod) error {
	desired := buildCatalogEndpoints(namespace, owners, scanners, screens)

	live, err := GetEndpointSlice(ctx, o.client, namespace, catalogServiceName)
	if errors.Is(err, ErrNotFound) {
		_, err := CreateEndpointSlice(ctx, o.client, desired)
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
	_, err = UpdateEndpointSlice(ctx, o.client, desired)
	return err
}

// SameEndpoints compares only what this operator states: the owners,
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

// GetEndpointSlice reads the live catalog slice of one namespace, for
// the owners and endpoints it holds now and for the resourceVersion
// the write is made conditional on. An absent slice is ErrNotFound,
// which the pass answers by creating one.
func GetEndpointSlice(ctx context.Context, c *Client, namespace, name string) (*EndpointSlice, error) {
	slice := &EndpointSlice{}
	if err := c.RequestJSON(ctx, http.MethodGet, endpointSlicesPath(namespace)+"/"+name, nil, slice); err != nil {
		return nil, err
	}
	return slice, nil
}

func CreateEndpointSlice(ctx context.Context, c *Client, slice *EndpointSlice) (*EndpointSlice, error) {
	body, err := json.Marshal(slice)
	if err != nil {
		return nil, err
	}
	created := &EndpointSlice{}
	path := endpointSlicesPath(slice.Metadata.Namespace)
	if err := c.RequestJSON(ctx, http.MethodPost, path, body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateEndpointSlice writes the whole slice back. The resourceVersion
// in the body makes the write conditional, so a slice that changed
// underneath answers ErrConflict, and the next pass reads it again.
func UpdateEndpointSlice(ctx context.Context, c *Client, slice *EndpointSlice) (*EndpointSlice, error) {
	body, err := json.Marshal(slice)
	if err != nil {
		return nil, err
	}
	written := &EndpointSlice{}
	path := endpointSlicesPath(slice.Metadata.Namespace) + "/" + slice.Metadata.Name
	if err := c.RequestJSON(ctx, http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}
