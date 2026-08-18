package main

// These tests run one prepare call end to end: a claim read from a
// test API server, a fake sysfs tree, and the CDI spec file that the
// container runtime would read. The spec file states which device
// nodes a consumer's container receives, so it is the thing worth
// asserting on.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

const testClaimUID = "0f8b1a2c-3d4e-5f60-8172-93a4b5c6d7e8"

// allocatedClaim serves one ResourceClaim whose allocation holds these
// results.
func allocatedClaim(t *testing.T, results ...AllocatedDevice) http.Handler {
	t.Helper()
	claim := map[string]any{
		"metadata": map[string]any{"name": "player-one", "namespace": "arcade", "uid": testClaimUID},
		"status": map[string]any{
			"allocation": map[string]any{"devices": map[string]any{"results": results}},
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/apis/resource.k8s.io/v1/namespaces/arcade/resourceclaims/player-one"
		if r.URL.Path != want {
			t.Errorf("claim read from %q, want %q", r.URL.Path, want)
		}
		_ = json.NewEncoder(w).Encode(claim)
	})
}

// preparePlugin wires a plugin to a test API server, with sysfs and
// the CDI directory pointed at directories the test owns.
func preparePlugin(t *testing.T, claims http.Handler, devices ...fakeHID) *draPlugin {
	t.Helper()
	cdiTempDir(t)
	previous := draSysfsRoot
	draSysfsRoot = fakeSysfs(t, devices...)
	t.Cleanup(func() { draSysfsRoot = previous })
	return &draPlugin{client: testClient(t, claims)}
}

func testClaim() *drav1.Claim {
	return &drav1.Claim{Namespace: "arcade", Name: "player-one", Uid: testClaimUID}
}

func TestPrepareClaimDeliversOneControllersInputNodes(t *testing.T) {
	// Two controllers are connected. The claim allocated one of them,
	// and the container must receive that one's nodes and no others.
	plugin := preparePlugin(t,
		allocatedClaim(t, AllocatedDevice{
			Request: "controller",
			Driver:  DriverName,
			Pool:    "liken-1",
			Device:  "a0-ab-51-33-b7-12",
		}),
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5", "input/js0"),
		dualSense("0002", "a0:ab:51:33:b7:12", "input/event6"),
		dualSense("0003", "b4:8c:9d:11:22:33", "input/event7"),
	)

	resp := plugin.prepareClaim(testClaim())
	if resp.Error != "" {
		t.Fatalf("prepare failed: %s", resp.Error)
	}
	if len(resp.Devices) != 1 {
		t.Fatalf("devices = %+v", resp.Devices)
	}
	device := resp.Devices[0]
	if device.DeviceName != "a0-ab-51-33-b7-12" || device.PoolName != "liken-1" {
		t.Errorf("device = %+v", device)
	}
	wantID := "bluetooth.liken.sh/controller=" + testClaimUID + "-a0-ab-51-33-b7-12"
	if len(device.CdiDeviceIds) != 1 || device.CdiDeviceIds[0] != wantID {
		t.Errorf("cdiDeviceIds = %v, want [%s]", device.CdiDeviceIds, wantID)
	}

	// The file name starts with this driver's prefix, so liken's own specs
	// in the same directory never collide with these.
	path := filepath.Join(cdiDir, "bluetooth.liken.sh-"+testClaimUID+".json")
	spec := readSpec(t, path)
	if len(spec.Devices) != 1 {
		t.Fatalf("spec devices = %+v", spec.Devices)
	}
	var paths []string
	for _, node := range spec.Devices[0].ContainerEdits.DeviceNodes {
		paths = append(paths, node.Path)
	}
	// Both of the allocated controller's HID devices contribute, the
	// other controller contributes nothing, and joydev's node stays
	// out.
	want := []string{"/dev/input/event5", "/dev/input/event6"}
	if len(paths) != len(want) {
		t.Fatalf("nodes = %v, want %v", paths, want)
	}
	for i, path := range want {
		if paths[i] != path {
			t.Fatalf("nodes = %v, want %v", paths, want)
		}
	}
}

func TestPrepareClaimLeavesAnotherDriversAllocationAlone(t *testing.T) {
	// The operator's own pod holds a claim on liken's adapter. The
	// kubelet asks every registered driver about every claim, and this
	// driver has nothing to prepare for that one.
	plugin := preparePlugin(t,
		allocatedClaim(t, AllocatedDevice{
			Request: "adapter",
			Driver:  "liken.sh",
			Pool:    "liken-1",
			Device:  "usb-3-1-1-0",
		}),
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
	)

	resp := plugin.prepareClaim(testClaim())
	if resp.Error != "" {
		t.Fatalf("prepare failed: %s", resp.Error)
	}
	if len(resp.Devices) != 0 {
		t.Fatalf("devices = %+v, want none", resp.Devices)
	}
	if _, err := os.Stat(cdiSpecPath(testClaimUID)); !os.IsNotExist(err) {
		t.Fatal("a claim this driver prepares nothing for still wrote a spec file")
	}
}

func TestPrepareClaimWaitsForAControllerThatIsOffTheAir(t *testing.T) {
	// The controller is paired and switched off, so it registers no
	// node. Failing per claim holds the pod in ContainerCreating with
	// a reason a describe of the pod shows, which is the correct
	// outcome while the device's NoSchedule taint keeps the next pod
	// parked.
	plugin := preparePlugin(t, allocatedClaim(t, AllocatedDevice{
		Request: "controller",
		Driver:  DriverName,
		Pool:    "liken-1",
		Device:  "a0-ab-51-33-b7-12",
	}))

	resp := plugin.prepareClaim(testClaim())
	if resp.Error == "" {
		t.Fatal("prepare succeeded for a controller with no input node")
	}
	if _, err := os.Stat(cdiSpecPath(testClaimUID)); !os.IsNotExist(err) {
		t.Fatal("a failed prepare wrote a spec file")
	}
}

func TestPrepareClaimRefusesARecreatedClaim(t *testing.T) {
	// A claim deleted and recreated under the same name is a different
	// grant, and it is not the one this pod was scheduled against.
	plugin := preparePlugin(t, allocatedClaim(t, AllocatedDevice{
		Driver: DriverName,
		Device: "a0-ab-51-33-b7-12",
	}))

	claim := testClaim()
	claim.Uid = "99999999-0000-0000-0000-000000000000"
	if resp := plugin.prepareClaim(claim); resp.Error == "" {
		t.Fatal("prepare accepted a claim whose UID had changed")
	}
}

func TestUnprepareRemovesTheSpecAndRepeats(t *testing.T) {
	plugin := preparePlugin(t,
		allocatedClaim(t, AllocatedDevice{
			Request: "controller",
			Driver:  DriverName,
			Pool:    "liken-1",
			Device:  "a0-ab-51-33-b7-12",
		}),
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
	)
	if resp := plugin.prepareClaim(testClaim()); resp.Error != "" {
		t.Fatalf("prepare failed: %s", resp.Error)
	}

	req := &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "arcade", Name: "player-one", Uid: testClaimUID}},
	}
	// The kubelet repeats an unprepare whenever it has no record that
	// the call succeeded, so the second one must answer the same way.
	for range 2 {
		resp, err := plugin.NodeUnprepareResources(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		if got := resp.Claims[testClaimUID]; got == nil || got.Error != "" {
			t.Fatalf("unprepare = %+v", got)
		}
	}
	if _, err := os.Stat(cdiSpecPath(testClaimUID)); !os.IsNotExist(err) {
		t.Fatal("the spec file is still there")
	}
}
