package main

import (
	"context"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// driverName is the name that a CSIDriver object and a volume's
// `driver` field use to select this driver.
const driverName = "per-node.liken.sh"

type identity struct {
	csi.UnimplementedIdentityServer
	// store is the directory that Probe writes a test file to.
	store string
}

func (i *identity) GetPluginInfo(
	context.Context, *csi.GetPluginInfoRequest,
) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{Name: driverName, VendorVersion: version}, nil
}

// GetPluginCapabilities returns no capabilities, because the driver
// has no Controller service and does not report topology.
func (i *identity) GetPluginCapabilities(
	context.Context, *csi.GetPluginCapabilitiesRequest,
) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{}, nil
}

// Probe reports ready only when the store accepts a write, because
// every volume's copy is a directory under the store.
func (i *identity) Probe(context.Context, *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{Ready: wrapperspb.Bool(i.storeIsWriteable())}, nil
}

// storeIsWriteable creates and removes a file, because the directory's
// mode bits do not show a read-only mount or a failing device.
func (i *identity) storeIsWriteable() bool {
	file, err := os.CreateTemp(i.store, ".probe-")
	if err != nil {
		return false
	}
	file.Close()
	os.Remove(file.Name())
	return true
}
