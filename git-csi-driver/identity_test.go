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
		t.Fatalf("calling GetPluginInfo: %v", err)
	}
	if info.GetName() != "git.liken.sh" {
		t.Errorf("GetPluginInfo returned name %q, want %q", info.GetName(), "git.liken.sh")
	}
	if info.GetVendorVersion() != version {
		t.Errorf("GetPluginInfo returned version %q, want %q", info.GetVendorVersion(), version)
	}
}

func TestTheNodePluginDeclaresNoCapability(t *testing.T) {
	client := csi.NewIdentityClient(startServer(t, io.Discard))
	capabilities, err := client.GetPluginCapabilities(t.Context(),
		&csi.GetPluginCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("calling GetPluginCapabilities: %v", err)
	}
	if got := capabilities.GetCapabilities(); len(got) != 0 {
		t.Errorf("GetPluginCapabilities returned %v, want none", got)
	}
}

func TestProbeIsReadyWhenTheStoreIsWriteable(t *testing.T) {
	client := csi.NewIdentityClient(startServer(t, io.Discard))
	probe, err := client.Probe(t.Context(), &csi.ProbeRequest{})
	if err != nil {
		t.Fatalf("calling Probe: %v", err)
	}
	if !probe.GetReady().GetValue() {
		t.Error("Probe returned not ready, want ready")
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
		t.Fatalf("calling Probe: %v", err)
	}
	if probe.GetReady().GetValue() {
		t.Error("Probe returned ready, want not ready")
	}
}
