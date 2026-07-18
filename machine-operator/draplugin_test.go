package main

// The kubelet plugin's behavior: what prepare delivers, what it
// refuses, and that both gRPC services actually answer on their
// sockets the way the kubelet will call them.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
	regv1 "k8s.io/kubelet/pkg/apis/pluginregistration/v1"

	"github.com/liken-sh/liken/kubernetes"
)

// draFixture stands up everything one prepare call touches: a fake
// sysfs with the stick plugged in, a fake API server holding one
// allocated claim, and a CDI directory to write into. The package
// seams (draSysfsRoot, cdiDir) are pointed at the fixture and
// restored afterward.
type draFixture struct {
	plugin *draPlugin
	cdi    string
}

func newDRAFixture(t *testing.T) *draFixture {
	t.Helper()

	sysfs := t.TempDir()
	stick := filepath.Join(sysfs, "bus", "usb", "devices", "2-1:1.0")
	sda := filepath.Join(stick, "host0", "target0:0:0", "0:0:0:0", "block", "sda")
	if err := os.MkdirAll(sda, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(stick, "modalias"): "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00\n",
		filepath.Join(stick, "driver"):   "", // symlinked below
		filepath.Join(sda, "dev"):        "8:0\n",
		filepath.Join(sda, "uevent"):     "DEVNAME=sda\n",
	}
	for path, content := range files {
		if content == "" {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	driver := filepath.Join(sysfs, "bus", "usb", "drivers", "usb-storage")
	if err := os.MkdirAll(driver, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(driver, filepath.Join(stick, "driver")); err != nil {
		t.Fatal(err)
	}
	block := filepath.Join(sysfs, "class", "block")
	if err := os.MkdirAll(block, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(block, filepath.Join(sda, "subsystem")); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/resource.k8s.io/v1/namespaces/media/resourceclaims/stick" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"name": "stick", "namespace": "media", "uid": "claim-1"},
			"status": map[string]any{
				"allocation": map[string]any{
					"devices": map[string]any{
						"results": []map[string]any{{
							"request": "disk",
							"driver":  "liken.sh",
							"pool":    "node-1",
							"device":  "usb-2-1-1-0",
						}},
					},
				},
			},
		})
	}))
	t.Cleanup(server.Close)
	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("t"), 0o600); err != nil {
		t.Fatal(err)
	}

	cdi := t.TempDir()
	origSysfs, origCDI := draSysfsRoot, cdiDir
	draSysfsRoot, cdiDir = sysfs, cdi
	t.Cleanup(func() { draSysfsRoot, cdiDir = origSysfs, origCDI })

	return &draFixture{
		plugin: &draPlugin{client: kubernetes.NewClient(server.URL, server.Client(), credentials)},
		cdi:    cdi,
	}
}

func TestPrepareDeliversTheClaimedDevicesNodes(t *testing.T) {
	fixture := newDRAFixture(t)

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	answer := resp.Claims["claim-1"]
	if answer == nil || answer.Error != "" {
		t.Fatalf("answer = %+v", answer)
	}
	if len(answer.Devices) != 1 {
		t.Fatalf("devices = %+v", answer.Devices)
	}
	d := answer.Devices[0]
	if d.PoolName != "node-1" || d.DeviceName != "usb-2-1-1-0" {
		t.Errorf("device = %+v", d)
	}
	if len(d.CdiDeviceIds) != 1 || d.CdiDeviceIds[0] != "liken.sh/device=claim-1-usb-2-1-1-0" {
		t.Errorf("cdi ids = %v", d.CdiDeviceIds)
	}

	raw, err := os.ReadFile(filepath.Join(fixture.cdi, "liken.sh-claim-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Kind != "liken.sh/device" || len(spec.Devices) != 1 {
		t.Fatalf("spec = %+v", spec)
	}
	nodes := spec.Devices[0].ContainerEdits.DeviceNodes
	if len(nodes) != 1 || nodes[0].Path != "/dev/sda" {
		t.Errorf("device nodes = %+v", nodes)
	}
}

func TestPrepareRefusesAClaimWhoseUIDChanged(t *testing.T) {
	fixture := newDRAFixture(t)

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "an-older-claim"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Claims["an-older-claim"].Error == "" {
		t.Error("a recreated claim is a different grant and must be refused")
	}
}

func TestPrepareReportsAMissingDevicePerClaim(t *testing.T) {
	fixture := newDRAFixture(t)
	// Unplug the stick: the walk no longer finds the allocated name.
	if err := os.RemoveAll(filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1:1.0")); err != nil {
		t.Fatal(err)
	}

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Claims["claim-1"].Error == "" {
		t.Error("hardware that left must fail the claim, in-band, for the kubelet to retry")
	}
}

func TestUnprepareRemovesTheSpecAndIsIdempotent(t *testing.T) {
	fixture := newDRAFixture(t)
	if err := writeCDISpec("claim-1", []cdiDevice{{Name: "x"}}); err != nil {
		t.Fatal(err)
	}

	req := &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	}
	resp, err := fixture.plugin.NodeUnprepareResources(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Claims["claim-1"].Error != "" {
		t.Fatalf("answer = %+v", resp.Claims["claim-1"])
	}
	if _, err := os.Stat(filepath.Join(fixture.cdi, "liken.sh-claim-1.json")); !os.IsNotExist(err) {
		t.Error("the spec file must be gone")
	}

	// A second unprepare of the same claim still succeeds.
	resp, err = fixture.plugin.NodeUnprepareResources(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Claims["claim-1"].Error != "" {
		t.Errorf("unprepare must be idempotent: %+v", resp.Claims["claim-1"])
	}
}

func TestServeAnswersOnBothSockets(t *testing.T) {
	fixture := newDRAFixture(t)

	sockets := t.TempDir()
	origRegistry, origPlugin := draRegistryDir, draPluginDir
	draRegistryDir = sockets
	draPluginDir = filepath.Join(sockets, "liken.sh")
	t.Cleanup(func() { draRegistryDir, draPluginDir = origRegistry, origPlugin })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serveDRAPlugin(ctx, fixture.plugin.client) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	// The registration handshake, exactly as the kubelet's plugin
	// watcher performs it.
	registration := filepath.Join(sockets, "liken.sh-reg.sock")
	waitForSocket(t, registration)
	regConn, err := grpc.NewClient("unix://"+registration, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer regConn.Close()
	info, err := regv1.NewRegistrationClient(regConn).GetInfo(t.Context(), &regv1.InfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != regv1.DRAPlugin || info.Name != "liken.sh" {
		t.Errorf("info = %+v", info)
	}
	if len(info.SupportedVersions) != 1 || info.SupportedVersions[0] != "v1.DRAPlugin" {
		t.Errorf("versions = %v", info.SupportedVersions)
	}

	// Then dial the endpoint the registration announced and prepare a
	// claim over the wire.
	draConn, err := grpc.NewClient("unix://"+info.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer draConn.Close()
	resp, err := drav1.NewDRAPluginClient(draConn).NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer := resp.Claims["claim-1"]; answer == nil || answer.Error != "" {
		t.Errorf("answer = %+v", answer)
	}
}

// waitForSocket polls for a unix socket to exist: the server sets
// its sockets up in a goroutine, and the test can outrun it.
func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket %s never appeared", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
