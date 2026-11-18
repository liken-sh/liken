package main

// identity.go implements the CSI Identity service, which the kubelet and
// the CSI sidecars call to read the plugin's name, its capabilities, and
// whether it is ready.

import (
	"context"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// driverName is the name that the CSIDriver object and every volume use
// to select this driver.
const driverName = "git.liken.sh"

type identity struct {
	csi.UnimplementedIdentityServer
	store      string
	controller bool
}

func (i *identity) GetPluginInfo(
	context.Context, *csi.GetPluginInfoRequest,
) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{Name: driverName, VendorVersion: version}, nil
}

// GetPluginCapabilities declares CONTROLLER_SERVICE on the controller
// plugin and no capability on the node plugin. The driver declares no
// topology, because any node can make a checkout.
func (i *identity) GetPluginCapabilities(
	context.Context, *csi.GetPluginCapabilitiesRequest,
) (*csi.GetPluginCapabilitiesResponse, error) {
	response := &csi.GetPluginCapabilitiesResponse{}
	if i.controller {
		response.Capabilities = []*csi.PluginCapability{{
			Type: &csi.PluginCapability_Service_{
				Service: &csi.PluginCapability_Service{
					Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
				},
			},
		}}
	}
	return response, nil
}

// Probe returns ready only when the store accepts a write, because the
// store holds every repository and work tree.
func (i *identity) Probe(context.Context, *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{Ready: wrapperspb.Bool(i.storeIsWriteable())}, nil
}

// storeIsWriteable creates and removes a file in the store instead of
// checking the directory's mode, because the mode does not show a
// read-only filesystem or a permission that this process lacks.
func (i *identity) storeIsWriteable() bool {
	file, err := os.CreateTemp(i.store, ".probe-")
	if err != nil {
		return false
	}
	file.Close()
	os.Remove(file.Name())
	return true
}
