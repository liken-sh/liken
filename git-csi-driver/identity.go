package main

// identity.go holds the CSI Identity service: the three calls a plugin
// answers before it holds any volume.

import (
	"context"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// driverName is the name every volume and the CSIDriver object use to
// select this driver.
const driverName = "git.liken.sh"

// identity answers the Identity service. Its only state is the store
// path, because readiness is whether the store takes a write.
type identity struct {
	csi.UnimplementedIdentityServer
	store string
}

func (i *identity) GetPluginInfo(
	context.Context, *csi.GetPluginInfoRequest,
) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{Name: driverName, VendorVersion: version}, nil
}

// GetPluginCapabilities declares nothing. The controller service
// arrives with plan 05, and the driver has no topology, because a
// checkout is made on whichever node publishes it.
func (i *identity) GetPluginCapabilities(
	context.Context, *csi.GetPluginCapabilitiesRequest,
) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{}, nil
}

// Probe reports ready when the store takes a write. Every repository
// and work tree lives in the store, so a driver that cannot write there
// can do nothing.
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
