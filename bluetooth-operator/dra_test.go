package main

// These tests run a prepare call from start to finish: a claim read
// from a test API server, a relay over a fake kernel, and the CDI spec
// file that the container runtime reads. The tests assert on the spec
// file, because it lists the device nodes a container receives.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

const testClaimUID = "0f8b1a2c-3d4e-5f60-8172-93a4b5c6d7e8"

// allocatedClaim serves a ResourceClaim whose allocation has these
// results and no configuration, so the claim receives every input
// class.
func allocatedClaim(t *testing.T, results ...AllocatedDevice) http.Handler {
	t.Helper()
	return configuredClaim(t, nil, results...)
}

// configuredClaim serves a ResourceClaim whose allocation has
// configuration blocks as well as results.
func configuredClaim(t *testing.T, config []AllocatedConfig, results ...AllocatedDevice) http.Handler {
	t.Helper()
	devices := map[string]any{"results": results}
	if config != nil {
		devices["config"] = config
	}
	claim := map[string]any{
		"metadata": map[string]any{"name": "player-one", "namespace": "arcade", "uid": testClaimUID},
		"status": map[string]any{
			"allocation": map[string]any{"devices": devices},
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

// preparePlugin returns a plugin that reads claims from a test API
// server and writes CDI specs to a temporary directory. It starts one
// relay for each controller that these HID devices belong to. A
// controller with no device here has no relay, like a bond that has
// never connected.
func preparePlugin(t *testing.T, claims http.Handler, devices ...fakeHID) *draPlugin {
	t.Helper()
	cdiTempDir(t)
	inputRelays := relaysFor(t, devices...)
	for _, device := range devices {
		inputRelays.ensure(device.Uevent["HID_UNIQ"], realNodes(device))
	}
	return &draPlugin{client: testClient(t, claims), relays: inputRelays}
}

func controllerAllocation() AllocatedDevice {
	return AllocatedDevice{
		Request: "controller",
		Driver:  DriverName,
		Pool:    "liken-1",
		Device:  "a0-ab-51-33-b7-12",
	}
}

// specNodes returns the device node paths of one device in a CDI spec.
func specNodes(t *testing.T, spec cdiSpec, device int) []string {
	t.Helper()
	var paths []string
	for _, node := range spec.Devices[device].ContainerEdits.DeviceNodes {
		paths = append(paths, node.Path)
	}
	return paths
}

func testClaim() *drav1.Claim {
	return &drav1.Claim{Namespace: "arcade", Name: "player-one", Uid: testClaimUID}
}

func TestPrepareClaimGrantsOnlyTheAllocatedControllersNodes(t *testing.T) {
	// Two controllers are connected. The claim has one of them, so the
	// container gets only that controller's nodes.
	plugin := preparePlugin(t,
		allocatedClaim(t, controllerAllocation()),
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5", "input/js0"),
		dualSense("0002", "a0:ab:51:33:b7:12", "input/event6"),
		dualSense("0003", "b4:8c:9d:11:22:33", "input/event7"),
	)

	resp := plugin.prepareClaim(testClaim())
	if resp.Error != "" {
		t.Fatalf("preparing the claim returned the error %q, want none", resp.Error)
	}
	if len(resp.Devices) != 1 {
		t.Fatalf("devices = %+v, want one", resp.Devices)
	}
	device := resp.Devices[0]
	if device.DeviceName != "a0-ab-51-33-b7-12" || device.PoolName != "liken-1" {
		t.Errorf("device = %+v, want device a0-ab-51-33-b7-12 in pool liken-1", device)
	}
	wantID := "bluetooth.liken.sh/controller=" + testClaimUID + "-a0-ab-51-33-b7-12"
	if len(device.CdiDeviceIds) != 1 || device.CdiDeviceIds[0] != wantID {
		t.Errorf("cdiDeviceIds = %v, want [%s]", device.CdiDeviceIds, wantID)
	}

	// The file name starts with this driver's name, so it cannot collide
	// with the spec files of other drivers in the same directory.
	path := filepath.Join(cdiDir, "bluetooth.liken.sh-"+testClaimUID+".json")
	spec := readSpec(t, path)
	if len(spec.Devices) != 1 {
		t.Fatalf("spec devices = %+v, want one", spec.Devices)
	}
	// Both HID devices of the allocated controller give a virtual node,
	// and the other controller gives none.
	paths := specNodes(t, spec, 0)
	want := plugin.relays.virtualNodes("a0:ab:51:33:b7:12")
	if len(want) != 2 {
		t.Fatalf("the relay holds %v, want two nodes", want)
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("nodes = %v, want %v", paths, want)
	}
	for _, other := range plugin.relays.virtualNodes("b4:8c:9d:11:22:33") {
		if slices.Contains(paths, other) {
			t.Errorf("the claim received %s, which belongs to another controller", other)
		}
	}
}

// A claim on a controller that is asleep is prepared from the
// capabilities snapshot in its bond's Secret, so the pod starts before
// anyone presses a button.
func TestPrepareClaimGrantsNodesForAControllerThatIsAsleep(t *testing.T) {
	plugin := preparePlugin(t, allocatedClaim(t, controllerAllocation()))
	plugin.relays.restore("a0:ab:51:33:b7:12", storedCapabilities(t))

	resp := plugin.prepareClaim(testClaim())
	if resp.Error != "" {
		t.Fatalf("preparing the claim returned the error %q, want none", resp.Error)
	}
	paths := specNodes(t, readSpec(t, cdiSpecPath(testClaimUID)), 0)
	want := plugin.relays.virtualNodes("a0:ab:51:33:b7:12")
	if len(want) != 1 || !reflect.DeepEqual(paths, want) {
		t.Fatalf("nodes = %v, want %v", paths, want)
	}
}

// storedCapabilities returns the evdev capabilities snapshot of one
// controller, as stored in its bond's Secret.
func storedCapabilities(t *testing.T) []byte {
	t.Helper()
	stored, err := json.Marshal(evdevSnapshot{
		Version: snapshotVersion,
		Nodes:   []evdevCapabilities{testCapabilities()},
	})
	if err != nil {
		t.Fatalf("marshaling the capabilities snapshot: %v", err)
	}
	return stored
}

func busAllocation() AllocatedDevice {
	return AllocatedDevice{
		Request: "sound",
		Driver:  DriverName,
		Pool:    "liken-1",
		Device:  testBus,
	}
}

// The bus grants a mount and an environment variable, and no device
// node. It has no relay, so a prepare succeeds on a machine with no
// connected controller.
func TestPrepareClaimGrantsTheMediaBusMountAndVariable(t *testing.T) {
	plugin := preparePlugin(t, allocatedClaim(t, busAllocation()))

	resp := plugin.prepareClaim(testClaim())
	if resp.Error != "" {
		t.Fatalf("preparing the claim returned the error %q, want none", resp.Error)
	}
	wantID := "bluetooth.liken.sh/controller=" + testClaimUID + "-" + testBus
	if len(resp.Devices) != 1 || resp.Devices[0].CdiDeviceIds[0] != wantID {
		t.Fatalf("devices = %+v, want one with %s", resp.Devices, wantID)
	}

	spec := readSpec(t, cdiSpecPath(testClaimUID))
	if len(spec.Devices) != 1 {
		t.Fatalf("spec devices = %+v, want one", spec.Devices)
	}
	edits := spec.Devices[0].ContainerEdits
	wantEnv := []string{"DBUS_SYSTEM_BUS_ADDRESS=unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket"}
	if !reflect.DeepEqual(edits.Env, wantEnv) {
		t.Errorf("env = %v, want %v", edits.Env, wantEnv)
	}
	wantMounts := []cdiMount{{
		HostPath:      "/var/run/bluetooth.liken.sh/dbus",
		ContainerPath: "/var/run/bluetooth.liken.sh/dbus",
		Options:       []string{"ro", "rbind", "rprivate", "nosuid", "nodev"},
	}}
	if !reflect.DeepEqual(edits.Mounts, wantMounts) {
		t.Errorf("mounts = %+v, want %+v", edits.Mounts, wantMounts)
	}
	if len(edits.DeviceNodes) != 0 {
		t.Errorf("deviceNodes = %+v, want none", edits.DeviceNodes)
	}
}

// One claim can hold both kinds of device from this driver. The spec
// file has one entry for each, and each entry has only the edits for
// its kind of device.
func TestPrepareClaimGrantsAControllerAndTheBusInOneSpec(t *testing.T) {
	plugin := preparePlugin(t,
		allocatedClaim(t, controllerAllocation(), busAllocation()),
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
	)

	resp := plugin.prepareClaim(testClaim())
	if resp.Error != "" {
		t.Fatalf("preparing the claim returned the error %q, want none", resp.Error)
	}
	if len(resp.Devices) != 2 {
		t.Fatalf("devices = %+v, want two", resp.Devices)
	}

	spec := readSpec(t, cdiSpecPath(testClaimUID))
	edits := map[string]cdiEdits{}
	for _, device := range spec.Devices {
		edits[device.Name] = device.ContainerEdits
	}
	controller := edits[testClaimUID+"-a0-ab-51-33-b7-12"]
	relayed := plugin.relays.virtualNodes("a0:ab:51:33:b7:12")
	if len(controller.DeviceNodes) != 1 || controller.DeviceNodes[0].Path != relayed[0] {
		t.Errorf("the controller's edits = %+v, want %v", controller, relayed)
	}
	if len(controller.Env) != 0 || len(controller.Mounts) != 0 {
		t.Errorf("the controller's edits = %+v, want none of the bus's edits", controller)
	}
	bus := edits[testClaimUID+"-"+testBus]
	if len(bus.Env) != 1 || len(bus.Mounts) != 1 || len(bus.DeviceNodes) != 0 {
		t.Errorf("the bus's edits = %+v, want the bus edits", bus)
	}
}

func TestPrepareClaimIgnoresAnotherDriversDevice(t *testing.T) {
	// A claim can also hold devices of other drivers, such as a USB
	// adapter from the liken.sh driver. The plugin prepares nothing for
	// them and writes no spec file.
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
		t.Fatalf("preparing the claim returned the error %q, want none", resp.Error)
	}
	if len(resp.Devices) != 0 {
		t.Fatalf("devices = %+v, want none", resp.Devices)
	}
	if _, err := os.Stat(cdiSpecPath(testClaimUID)); !os.IsNotExist(err) {
		t.Fatal("a CDI spec file exists for a claim with no device of this driver, want none")
	}
}

func TestPrepareClaimFailsForABondThatHasNeverConnected(t *testing.T) {
	// The controller has not connected since it was paired, so there is
	// no snapshot and the relay has no virtual device.
	plugin := preparePlugin(t, allocatedClaim(t, controllerAllocation()))

	resp := plugin.prepareClaim(testClaim())
	if resp.Error == "" {
		t.Fatal("preparing a claim for a controller with no relay returned no error, want one")
	}
	// The pod's events show this error, so it must name the controller
	// to turn on.
	if !strings.Contains(resp.Error, "A0:AB:51:33:B7:12") {
		t.Errorf("the error is %q, want it to name the controller A0:AB:51:33:B7:12", resp.Error)
	}
	if _, err := os.Stat(cdiSpecPath(testClaimUID)); !os.IsNotExist(err) {
		t.Fatal("a CDI spec file exists after a failed prepare, want none")
	}
}

func TestPrepareClaimFailsWhenTheClaimUIDChanged(t *testing.T) {
	// The claim was deleted and created again under the same name.
	plugin := preparePlugin(t, allocatedClaim(t, AllocatedDevice{
		Driver: DriverName,
		Device: "a0-ab-51-33-b7-12",
	}))

	claim := testClaim()
	claim.Uid = "99999999-0000-0000-0000-000000000000"
	if resp := plugin.prepareClaim(claim); resp.Error == "" {
		t.Fatal("preparing a claim whose UID changed returned no error, want one")
	}
}

func TestUnprepareDeletesTheSpecAndSucceedsWhenRepeated(t *testing.T) {
	plugin := preparePlugin(t,
		allocatedClaim(t, controllerAllocation()),
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
	)
	if resp := plugin.prepareClaim(testClaim()); resp.Error != "" {
		t.Fatalf("preparing the claim returned the error %q, want none", resp.Error)
	}

	req := &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "arcade", Name: "player-one", Uid: testClaimUID}},
	}
	// The kubelet repeats an unprepare when it has no record that the
	// call succeeded, so the second call must succeed too.
	for range 2 {
		resp, err := plugin.NodeUnprepareResources(t.Context(), req)
		if err != nil {
			t.Fatalf("unpreparing the claim: %v", err)
		}
		if got := resp.Claims[testClaimUID]; got == nil || got.Error != "" {
			t.Fatalf("unprepare response = %+v, want an entry with no error", got)
		}
	}
	if _, err := os.Stat(cdiSpecPath(testClaimUID)); !os.IsNotExist(err) {
		t.Fatal("the CDI spec file exists after unprepare, want it deleted")
	}
}

// inputsOf returns the input classes that a spec records for one
// device.
func inputsOf(t *testing.T, spec cdiSpec, device int) []string {
	t.Helper()
	return spec.Devices[device].inputs()
}

// The input classes a claim requests are written to the spec file with
// its nodes, because the relays rebuild their settings from the spec
// files when the operator restarts.
func TestPrepareClaimRecordsTheRequestedInputClasses(t *testing.T) {
	plugin := preparePlugin(t,
		configuredClaim(t,
			[]AllocatedConfig{driverConfig("FromClaim", `{"inputs":["joystick"]}`)},
			controllerAllocation()),
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
	)

	resp := plugin.prepareClaim(testClaim())
	if resp.Error != "" {
		t.Fatalf("preparing the claim returned the error %q, want none", resp.Error)
	}
	spec := readSpec(t, cdiSpecPath(testClaimUID))
	if got := inputsOf(t, spec, 0); !reflect.DeepEqual(got, []string{"joystick"}) {
		t.Errorf("inputs = %v, want [joystick]", got)
	}
}

// A claim that receives every class records no list of classes.
func TestPrepareClaimRecordsNoClassListWhenTheClaimRequestsEveryClass(t *testing.T) {
	plugin := preparePlugin(t,
		allocatedClaim(t, controllerAllocation()),
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
	)

	if resp := plugin.prepareClaim(testClaim()); resp.Error != "" {
		t.Fatalf("preparing the claim returned the error %q, want none", resp.Error)
	}
	if got := inputsOf(t, readSpec(t, cdiSpecPath(testClaimUID)), 0); got != nil {
		t.Errorf("inputs = %v, want none", got)
	}
}

// An invalid configuration fails the claim, so the container does not
// get input the claim did not request.
func TestPrepareClaimFailsOnAnInvalidConfiguration(t *testing.T) {
	cases := []struct {
		name       string
		parameters string
		contains   string
	}{
		{name: "an empty list", parameters: `{"inputs":[]}`, contains: "empty"},
		{name: "an unknown class", parameters: `{"inputs":["buttons"]}`, contains: `"buttons"`},
		{name: "an unknown axis", parameters: `{"axes":{"ABS_NOPE":{"fuzz":4}}}`, contains: "ABS_NOPE"},
		{name: "a field an axis does not take", parameters: `{"axes":{"ABS_X":{"maximum":9}}}`, contains: "maximum"},
		{name: "a negative fuzz", parameters: `{"axes":{"ABS_X":{"fuzz":-1}}}`, contains: "negative"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plugin := preparePlugin(t,
				configuredClaim(t,
					[]AllocatedConfig{driverConfig("FromClaim", c.parameters)},
					controllerAllocation()),
				dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
			)

			resp := plugin.prepareClaim(testClaim())
			if resp.Error == "" {
				t.Fatal("preparing a claim with an invalid configuration returned no error, want one")
			}
			if !strings.Contains(resp.Error, c.contains) {
				t.Errorf("the error is %q, want it to contain %s", resp.Error, c.contains)
			}
			if _, err := os.Stat(cdiSpecPath(testClaimUID)); !os.IsNotExist(err) {
				t.Fatal("a CDI spec file exists after a failed prepare, want none")
			}
		})
	}
}

// The axis settings of a claim are recorded with its classes, so the
// relays apply the same settings after an operator restart.
func TestPrepareClaimRecordsTheAxisSettings(t *testing.T) {
	plugin := preparePlugin(t,
		configuredClaim(t,
			[]AllocatedConfig{driverConfig("FromClaim",
				`{"inputs":["joystick"],"axes":{"ABS_RX":{"fuzz":4},"ABS_RY":{"fuzz":4}}}`)},
			controllerAllocation()),
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
	)

	resp := plugin.prepareClaim(testClaim())
	if resp.Error != "" {
		t.Fatalf("preparing the claim returned the error %q, want none", resp.Error)
	}
	spec := readSpec(t, cdiSpecPath(testClaimUID))
	if got := spec.Devices[0].axes(); got != "ABS_RX=4:,ABS_RY=4:" {
		t.Errorf("axes = %q, want the two axes the claim sets", got)
	}
	if got := inputsOf(t, spec, 0); !reflect.DeepEqual(got, []string{"joystick"}) {
		t.Errorf("inputs = %v, want [joystick]", got)
	}
}
