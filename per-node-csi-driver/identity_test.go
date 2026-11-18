package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestGetPluginInfoNamesTheDriverAndTheVersion(t *testing.T) {
	client := csi.NewIdentityClient(startServer(t, io.Discard))
	info, err := client.GetPluginInfo(t.Context(), &csi.GetPluginInfoRequest{})
	if err != nil {
		t.Fatalf("getting the plugin info: %v", err)
	}
	if info.GetName() != "per-node.liken.sh" {
		t.Errorf("the plugin name is %q, want %q", info.GetName(), "per-node.liken.sh")
	}
	if info.GetVendorVersion() != version {
		t.Errorf("the plugin version is %q, want %q", info.GetVendorVersion(), version)
	}
}

func TestGetPluginCapabilitiesDeclaresNoControllerService(t *testing.T) {
	client := csi.NewIdentityClient(startServer(t, io.Discard))
	capabilities, err := client.GetPluginCapabilities(t.Context(),
		&csi.GetPluginCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("getting the plugin capabilities: %v", err)
	}
	if got := capabilities.GetCapabilities(); len(got) != 0 {
		t.Errorf("the plugin capabilities are %v, want none", got)
	}
}

func TestProbeIsReadyWhenTheStoreIsWriteable(t *testing.T) {
	client := csi.NewIdentityClient(startServer(t, io.Discard))
	probe, err := client.Probe(t.Context(), &csi.ProbeRequest{})
	if err != nil {
		t.Fatalf("probing the driver: %v", err)
	}
	if !probe.GetReady().GetValue() {
		t.Error("Probe reports not ready, want ready")
	}
}

func TestProbeIsNotReadyWhenTheStoreIsGone(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	client := csi.NewIdentityClient(start(t, &config{
		endpoint: "unix://" + filepath.Join(dir, "csi.sock"),
		nodeID:   "node-1",
		store:    store,
	}, io.Discard))
	if err := os.RemoveAll(store); err != nil {
		t.Fatalf("removing the store: %v", err)
	}
	probe, err := client.Probe(t.Context(), &csi.ProbeRequest{})
	if err != nil {
		t.Fatalf("probing the driver: %v", err)
	}
	if probe.GetReady().GetValue() {
		t.Error("Probe reports ready, want not ready after the store is removed")
	}
}
