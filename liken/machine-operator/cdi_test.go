package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

func specPaths(t *testing.T, fixture *draFixture, claimUID string) []string {
	t.Helper()
	var paths []string
	for _, node := range specNodes(t, fixture, claimUID) {
		paths = append(paths, node.Path)
	}
	return paths
}

func specNodes(t *testing.T, fixture *draFixture, claimUID string) []cdiDeviceNode {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixture.cdi, "liken.sh-"+claimUID+".json"))
	if err != nil {
		t.Fatalf("reading the CDI spec: %v", err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decoding the CDI spec: %v", err)
	}
	var nodes []cdiDeviceNode
	for _, device := range spec.Devices {
		nodes = append(nodes, device.ContainerEdits.DeviceNodes...)
	}
	return nodes
}

// prepared runs one prepare call, the way the kubelet does before it
// starts the first pod that holds the claim.
func prepared(t *testing.T, fixture *draFixture) {
	t.Helper()
	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if result := resp.Claims["claim-1"]; result == nil || result.Error != "" {
		t.Fatalf("the prepare result for claim-1 is %+v, want no error", result)
	}
}

func TestRefreshFollowsADeviceThatEnumeratedAgain(t *testing.T) {
	// The kubelet prepares a claim once, so after a replug the file
	// still lists the old usbfs node, which no longer exists. The next
	// pod that uses the claim would receive that node.
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	fixture.enumerate(t, 9)
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/002/009", "/dev/sda"}) {
		t.Errorf("the spec device nodes are %v, want the node this enumeration assigned", paths)
	}
}

func TestARefreshRecordsTheSpecsItRewrites(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)
	current := &passOutcome{}
	refreshCDISpecs(draSysfsRoot, current, machineEvents{})

	fixture.enumerate(t, 9)
	rewritten := &passOutcome{}
	refreshCDISpecs(draSysfsRoot, rewritten, machineEvents{})

	if len(current.writes) != 0 || len(rewritten.writes) != 1 {
		t.Errorf("writes: %q for a current spec and %q after a re-enumeration, want none and one", current.writes, rewritten.writes)
	}
}

// A spec that does not decode stays broken until a new prepare
// replaces it, so the pass retries it slowly.
func TestACorruptSpecIsALastingFailure(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)
	if err := os.WriteFile(cdiSpecPath("claim-1"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("writing the corrupt spec: %v", err)
	}
	out := &passOutcome{}

	refreshCDISpecs(draSysfsRoot, out, machineEvents{})

	if got := failureKinds(out); !slices.Equal(got, []failureKind{lasting}) {
		t.Errorf("the failure kinds are %v, want one lasting failure", got)
	}
}

func TestRefreshLeavesASpecThatStillMatchesAlone(t *testing.T) {
	// A device that has not changed must not cause a write on every
	// pass, because containerd reads these files at every container
	// creation.
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	path := filepath.Join(fixture.cdi, "liken.sh-claim-1.json")
	written := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatalf("setting the spec modification time: %v", err)
	}
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the spec file status: %v", err)
	}
	if !info.ModTime().Equal(written) {
		t.Errorf("the spec was rewritten at %v, want no write at all", info.ModTime())
	}
}

// The kernel gives the stick's old /dev/sda to the next disk that
// arrives, so a container that kept the old nodes would open that
// disk. The spec lists a node that does not exist instead, and the
// next container of the claim fails to start until the stick returns.
func TestRefreshNamesAnAbsentNodeForHardwareThatLeft(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	// The stick leaves port 2-1, and another stick in port 2-2 takes
	// /dev/sda.
	usb := filepath.Join(draSysfsRoot, "bus", "usb", "devices")
	if err := os.Rename(filepath.Join(usb, "2-1:1.0"), filepath.Join(usb, "2-2:1.0")); err != nil {
		t.Fatalf("unplugging the stick: %v", err)
	}
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

	if paths := specPaths(t, fixture, "claim-1"); !slices.Equal(paths, []string{deviceAbsentNode}) {
		t.Errorf("the spec device nodes are %v, want the absent node alone", paths)
	}
}

func TestRefreshRestoresTheNodesOfHardwareThatReturned(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)
	stick := filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1:1.0")
	aside := filepath.Join(t.TempDir(), "unplugged")

	if err := os.Rename(stick, aside); err != nil {
		t.Fatalf("unplugging the stick: %v", err)
	}
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})
	if err := os.Rename(aside, stick); err != nil {
		t.Fatalf("plugging the stick back in: %v", err)
	}
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/002/004", "/dev/sda"}) {
		t.Errorf("the spec device nodes are %v, want the stick's nodes back", paths)
	}
}

func TestRefreshWritesTheBusNodeAloneAfterADriverDetach(t *testing.T) {
	// A libusb program in the claim's pod detaches the kernel driver
	// from the interface, and the driver's nodes go with it. The
	// interface stays on the bus. The refresh rewrites the spec to the
	// bus node, so a restarted container of the claim gets a node that
	// exists.
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	stick := filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1:1.0")
	if err := os.Remove(filepath.Join(stick, "driver")); err != nil {
		t.Fatalf("removing the driver link of the stick: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(stick, "host0")); err != nil {
		t.Fatalf("removing the host directory of the stick: %v", err)
	}
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

	paths := specPaths(t, fixture, "claim-1")
	if !slices.Equal(paths, []string{"/dev/bus/usb/002/004"}) {
		t.Errorf("the spec device nodes are %v, want the bus node alone", paths)
	}
}

func TestRefreshAddsUHIDAfterTheModuleLoads(t *testing.T) {
	// The operator loads a declared module without a reboot, so
	// /dev/uhid can appear after the kubelet prepared the adapter's
	// claim. The next refresh adds it to the spec.
	fixture := newDRAFixture(t)
	fixture.addBluetooth(t)
	fixture.allocated = "usb-1-8-1-0"
	prepared(t, fixture)

	miscDevice(t, draSysfsRoot, "uhid", 239)
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/001/008", "/dev/uhid"}) {
		t.Errorf("the spec device nodes are %v, want the uhid node beside the usbfs node", paths)
	}
}

func TestRefreshIgnoresFilesItDidNotWrite(t *testing.T) {
	// Other CDI drivers write their specs to the same directory.
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	foreign := filepath.Join(fixture.cdi, "nvidia.com-gpu.json")
	if err := os.WriteFile(foreign, []byte("{}"), 0o644); err != nil {
		t.Fatalf("writing the other driver spec: %v", err)
	}
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

	raw, err := os.ReadFile(foreign)
	if err != nil {
		t.Fatalf("reading the other driver spec: %v", err)
	}
	if string(raw) != "{}" {
		t.Errorf("the other driver's spec is %s, want it unchanged", raw)
	}
}

func TestTheRefreshWithNoDirectoryDoesNothing(t *testing.T) {
	isolatePass(t)
	cdiDir = filepath.Join(t.TempDir(), "absent")
	out := &passOutcome{}

	refreshCDISpecs(t.TempDir(), out, machineEvents{})

	if out.failureCount() != 0 || len(out.writes) != 0 {
		t.Errorf("the refresh recorded %+v, want no failures and no writes", out)
	}
}

// A CDI directory that exists and cannot be read hides every prepared
// claim from the refresh, so an unplugged device gets no taint. The pass
// must record the failure so that it retries.
func TestTheRefreshReportsADirectoryItCannotRead(t *testing.T) {
	isolatePass(t)
	cdiDir = filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(cdiDir, nil, 0o644); err != nil {
		t.Fatalf("writing a regular file at the CDI directory: %v", err)
	}
	out := &passOutcome{}

	refreshCDISpecs(t.TempDir(), out, machineEvents{})

	if out.failureCount() != 1 {
		t.Errorf("the refresh recorded %d failures for a CDI directory that is a regular file, want 1", out.failureCount())
	}
}

// A spec path that cannot be read is a failure of the refresh. A device
// whose name does not start with its claim's UID is left unchanged.
func TestTheRefreshReportsASpecItCannotRead(t *testing.T) {
	isolatePass(t)
	if err := os.Mkdir(cdiSpecPath("claim-1"), 0o755); err != nil {
		t.Fatalf("creating a directory at the spec path: %v", err)
	}
	foreign := cdiSpec{Version: "0.6.0", Kind: cdiKind, Devices: []cdiDevice{{Name: "claim-9-usb-2-1-1-0"}}}
	raw, _ := json.Marshal(foreign)
	if err := os.WriteFile(cdiSpecPath("claim-2"), raw, 0o644); err != nil {
		t.Fatalf("writing the spec of claim-2: %v", err)
	}
	out := &passOutcome{}

	refreshCDISpecs(t.TempDir(), out, machineEvents{})

	after, _ := os.ReadFile(cdiSpecPath("claim-2"))
	if out.failureCount() != 1 || string(after) != string(raw) {
		t.Errorf("the refresh recorded %d failures and left claim-2 as %s, want 1 and unchanged", out.failureCount(), after)
	}
}

// cdiDir under a regular file cannot be created, a read-only cdiDir
// refuses the spec file, and a spec path that is a non-empty directory
// cannot be removed.
func TestTheSpecWritesReportTheirFailures(t *testing.T) {
	isolatePass(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatalf("writing a regular file: %v", err)
	}
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o755) })
	full := t.TempDir()
	if err := os.MkdirAll(filepath.Join(full, "liken.sh-claim-1.json", "child"), 0o755); err != nil {
		t.Fatalf("creating a non-empty directory at the spec path: %v", err)
	}

	cdiDir = filepath.Join(file, "cdi")
	noDirectory := writeCDISpec("claim-1", nil)
	cdiDir = readOnly
	noFile := writeCDISpec("claim-1", nil)
	cdiDir = full
	noRemove := removeCDISpec("claim-1")

	if noDirectory == nil || noFile == nil || noRemove == nil {
		t.Errorf("writeCDISpec returned %v for a missing directory and %v for a read-only one, and removeCDISpec returned %v for a non-empty directory, want an error from each", noDirectory, noFile, noRemove)
	}
}

// An evdev node has a file mode, which is a pointer. A refresh must
// find the nodes it reads back from a spec equal to the nodes it would
// write, or it rewrites a claim on an input device on every pass.
func TestAnEvdevNodeReadBackFromItsSpecIsCurrent(t *testing.T) {
	written := deviceNodes([]string{"/dev/input/event3", "/dev/hidraw0"})
	raw, err := json.Marshal(written)
	if err != nil {
		t.Fatalf("encoding the device nodes: %v", err)
	}
	var read []cdiDeviceNode
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatalf("decoding the device nodes: %v", err)
	}

	if !sameDeviceNodes(read, deviceNodes([]string{"/dev/input/event3", "/dev/hidraw0"})) {
		t.Error("the nodes read back from a spec differ from the same nodes built again")
	}
	if sameDeviceNodes(read, deviceNodes([]string{"/dev/input/event4", "/dev/hidraw0"})) {
		t.Error("nodes for another event device compare equal")
	}
}
