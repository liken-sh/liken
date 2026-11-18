package main

// The unpair command only deletes the Peripheral. The operator then
// disconnects the device, waits until no claim holds it, removes it
// from the ResourceSlice, and removes the bond. The Peripheral owns the
// Secret with the keys, so garbage collection deletes the Secret.

import (
	"context"
	"fmt"
	"io"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
)

type unpairOptions struct {
	// Device is an address or the name of a Peripheral.
	Device string
}

// runUnpair deletes the Peripheral for opts.Device.
func runUnpair(ctx context.Context, getter genericclioptions.RESTClientGetter, opts unpairOptions, stderr io.Writer) error {
	if opts.Device == "" {
		return fmt.Errorf("unpair needs the address or the name of a Peripheral")
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

// deletePeripheral returns the API's not-found error unchanged, so a
// person who unpairs a device that is not paired sees it.
func deletePeripheral(ctx context.Context, client dynamic.Interface, name string) error {
	return client.Resource(peripheralGVR).Delete(ctx, name, metav1.DeleteOptions{})
}

// peripheralName returns the Peripheral name for a device: the address
// in lowercase with dashes instead of colons. A name in that form is
// returned unchanged.
func peripheralName(device string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(device)), ":", "-")
}
