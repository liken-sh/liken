package main

// The CLI does not import the operator, so this file copies the API
// group, the resources, and the objects that the CLI sends through the
// dynamic client. Each step also works with kubectl: create a
// PairingRequest, patch its spec.device to approve a device, and delete
// a Peripheral to unpair it.

import (
	"context"
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	// The API group and version of the operator's objects.
	driverGroup   = "bluetooth.liken.sh"
	driverVersion = "v1alpha1"

	pairingRequestKind = "PairingRequest"
)

// PairingRequest is namespaced. Adapter and Peripheral are
// cluster-scoped, because a radio and a bond belong to a machine.
var (
	pairingRequestGVR = schema.GroupVersionResource{Group: driverGroup, Version: driverVersion, Resource: "pairingrequests"}
	peripheralGVR     = schema.GroupVersionResource{Group: driverGroup, Version: driverVersion, Resource: "peripherals"}
	adapterGVR        = schema.GroupVersionResource{Group: driverGroup, Version: driverVersion, Resource: "adapters"}
)

// listPeripherals returns the names of all Peripherals.
func listPeripherals(ctx context.Context, client dynamic.Interface) ([]string, error) {
	list, err := client.Resource(peripheralGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}
	return names, nil
}

// newPairingRequest returns a PairingRequest for the adapter. It leaves
// spec.device empty, so the operator lists the devices it sees and pairs
// none until a person approves one. A windowSeconds of 0 leaves
// spec.windowSeconds unset.
func newPairingRequest(namespace, name, adapter string, windowSeconds int) *unstructured.Unstructured {
	spec := map[string]any{"adapter": adapter}
	if windowSeconds > 0 {
		spec["windowSeconds"] = int64(windowSeconds)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": driverGroup + "/" + driverVersion,
		"kind":       pairingRequestKind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"spec": spec,
	}}
}

// approvalPatch returns the merge patch that approves a device by
// setting spec.device to its address.
func approvalPatch(address string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"spec": map[string]any{"device": address},
	})
}
