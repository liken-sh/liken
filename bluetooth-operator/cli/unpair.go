package main

// The unpair verb. Deleting the Peripheral is the unpair: the
// operator disconnects the device, waits for any claim on it to
// release, retires it from the slice, and removes the bond. The Secret
// with the keys is owned by the Peripheral, so it goes with the
// object. This verb makes that one delete.

import (
	"context"
	"fmt"
	"io"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
)

// The unpair verb's one positional argument, the
// device to unpair, as an address or as the Peripheral's own name.
type unpairOptions struct {
	Device string
}

// runUnpair deletes one Peripheral. It takes the
// device as the address a person reads off the controller or as the
// Peripheral's name, and names one form to the API.
func runUnpair(ctx context.Context, getter genericclioptions.RESTClientGetter, opts unpairOptions, stderr io.Writer) error {
	if opts.Device == "" {
		return fmt.Errorf("unpair needs a device")
	}
	config, err := getter.ToRESTConfig()
	if err != nil {
		return err
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return err
	}
	return deletePeripheral(ctx, client, peripheralName(opts.Device))
}

// deletePeripheral removes one Peripheral by name. A
// Peripheral already gone is reported, because a person who asked to
// unpair a device that is not there should hear so.
func deletePeripheral(ctx context.Context, client dynamic.Interface, name string) error {
	return client.Resource(peripheralGVR).Delete(ctx, name, metav1.DeleteOptions{})
}

// peripheralName turns an address into the Peripheral
// name the operator uses: lowercase, with dashes in place of colons. A
// name already in that form passes through.
func peripheralName(device string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(device)), ":", "-")
}
