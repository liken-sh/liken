package main

// The pairing API as the CLI reaches it. The CLI shares no code
// with the operator, so it names the group, the resources, and the
// fields it reads and writes here, and talks to them through the
// dynamic client. A pairing is three calls a person makes with
// kubectl: create a PairingRequest, patch its spec.device to approve a
// device, and delete a Peripheral to unpair one. This file names each.

import (
	"context"
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	// The driver's own API group and version, the same
	// the operator publishes its objects under.
	driverGroup   = "bluetooth.liken.sh"
	driverVersion = "v1alpha1"

	pairingRequestKind = "PairingRequest"
)

// The three resources the pairing verbs touch. The
// PairingRequest is namespaced; the Adapter and the Peripheral are
// cluster-scoped, because a radio and a bond belong to a machine.
var (
	pairingRequestGVR = schema.GroupVersionResource{Group: driverGroup, Version: driverVersion, Resource: "pairingrequests"}
	peripheralGVR     = schema.GroupVersionResource{Group: driverGroup, Version: driverVersion, Resource: "peripherals"}
	adapterGVR        = schema.GroupVersionResource{Group: driverGroup, Version: driverVersion, Resource: "adapters"}
)

// listPeripherals lists the Peripheral names through a dynamic
// client; Peripheral is cluster-scoped, so the list carries no
// namespace>
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

// newPairingRequest builds the object the CLI posts to
// open a window. An empty spec.device pairs nothing, so the window
// only scans until a person approves a device by name.
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

// approvalPatch is the merge patch that approves one
// device. Writing the address into spec.device is the approval, the
// same write a person makes with kubectl patch.
func approvalPatch(address string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"spec": map[string]any{"device": address},
	})
}
