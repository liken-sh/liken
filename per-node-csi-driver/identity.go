package main

// identity.go implements the CSI Identity service. The plugin receives
// these three calls before it publishes a volume.

import (
	"context"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// driverName is the name every volume and the CSIDriver object use to
// select this driver.
const driverName = "per-node.liken.sh"

// identity implements the Identity service. Its only state is the store
// path, because readiness depends on whether the store accepts a write.
type identity struct {
	csi.UnimplementedIdentityServer
	store string
}

func (i *identity) GetPluginInfo(
	context.Context, *csi.GetPluginInfoRequest,
) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{Name: driverName, VendorVersion: version}, nil
}

// GetPluginCapabilities declares no capability. The driver has no
// Controller service and no topology, so the kubelet and the registrar
// call the Identity and Node services alone.
func (i *identity) GetPluginCapabilities(
	context.Context, *csi.GetPluginCapabilitiesRequest,
) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{}, nil
}

// Probe reports ready while the store accepts a write. Every copy is a
// directory under the store, so a store that refuses a write can
// publish no volume.
func (i *identity) Probe(context.Context, *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{Ready: wrapperspb.Bool(i.storeIsWriteable())}, nil
}

// storeIsWriteable creates and removes a file instead of reading the
// directory's mode. A mode says what a user may do, and a write says
// what this process did.
func (i *identity) storeIsWriteable() bool {
	file, err := os.CreateTemp(i.store, ".probe-")
	if err != nil {
		return false
	}
	file.Close()
	os.Remove(file.Name())
	return true
}
