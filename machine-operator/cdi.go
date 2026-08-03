package main

// Writing CDI specs: how a prepared claim becomes device nodes in a
// container.
//
// The Container Device Interface connects two things: which device to
// use, and what appears inside the container. A JSON file in a
// well-known directory describes named devices and the edits that
// grant one device to a container. Here, those edits are device
// nodes only; the CDI spec format also allows mounts and environment
// variables for drivers that need them, but liken does not use those.
// The DRA driver answers the kubelet's prepare call with CDI device
// IDs. Each ID has the form kind=name. The kubelet passes the ID
// through the CRI, and containerd resolves it against these files
// when it creates the container. No privilege is involved anywhere:
// the pod gets exactly the nodes the spec names, with the default
// cgroup device rules to match.
//
// Each claim gets one spec file, named by the claim's UID, not by its
// namespace and name. This is deliberate. When a claim is deleted and
// recreated under the same name, it is a different grant, and its
// file must not collide with a stale one. The specs live under
// /var/run, which is the machine's runtime tmpfs at /run under its
// older name (the image build explains the symlink). The kubelet
// re-prepares every claim after a reboot, so each file only needs to
// last one boot, and a tmpfs directory removes the files
// automatically at that point.
//
// A file also has to stay correct for the whole boot. The kubelet
// prepares a claim once and reuses the answer for every later pod
// that names the same claim, so nothing re-prepares a claim while one
// of its pods runs. Meanwhile the nodes a device delivers can move
// under it: a USB device that is unplugged and plugged back in
// enumerates again with a new device number, and its usbfs node moves
// with it. The reconcile pass rewrites every prepared claim's file
// from the same sysfs walk that publishes the inventory, on the same
// cadence.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/liken-sh/liken/hardware"
)

// cdiWrites serializes the writes to these files. The kubelet's
// prepare calls and the reconcile pass both write them, and both
// stage a write through the same temporary path.
var cdiWrites sync.Mutex

// cdiDir is the directory where containerd looks for the CDI specs
// that liken writes while the system runs. It is a variable so the
// tests can change it.
var cdiDir = "/var/run/cdi"

// cdiSpec holds the part of the CDI spec schema that liken writes.
// liken delivers device nodes only, so the struct omits the fields
// for mounts and environment variables.
type cdiSpec struct {
	Version string      `json:"cdiVersion"`
	Kind    string      `json:"kind"`
	Devices []cdiDevice `json:"devices"`
}

type cdiDevice struct {
	Name           string   `json:"name"`
	ContainerEdits cdiEdits `json:"containerEdits"`
}

type cdiEdits struct {
	DeviceNodes []cdiDeviceNode `json:"deviceNodes"`
}

type cdiDeviceNode struct {
	Path string `json:"path"`
}

// deviceNodes turns the paths one published device delivers into the
// container edits that grant them.
func deviceNodes(paths []string) []cdiDeviceNode {
	nodes := make([]cdiDeviceNode, 0, len(paths))
	for _, path := range paths {
		nodes = append(nodes, cdiDeviceNode{Path: path})
	}
	return nodes
}

// cdiKind identifies liken's CDI devices, the same way the driver
// name identifies liken's slices. A CDI device ID has the form
// "<kind>=<name>".
const cdiKind = "liken.sh/device"

// writeCDISpec writes one claim's devices to a file where the
// runtime can find them.
func writeCDISpec(claimUID string, devices []cdiDevice) error {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()
	return writeSpecFile(claimUID, devices)
}

// writeSpecFile is the write itself, with the lock already held. It
// is atomic. containerd may list the directory at any moment, and a
// half-written spec would fail every container creation that reads it
// at that moment.
func writeSpecFile(claimUID string, devices []cdiDevice) error {
	if err := os.MkdirAll(cdiDir, 0o755); err != nil {
		return err
	}
	spec := cdiSpec{Version: "0.6.0", Kind: cdiKind, Devices: devices}
	raw, err := json.Marshal(&spec)
	if err != nil {
		return err
	}
	path := cdiSpecPath(claimUID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// removeCDISpec deletes a claim's spec file. If the spec is already
// gone, this counts as success, because unprepare must be
// idempotent: the kubelet retries it whenever it is not sure the
// call succeeded.
func removeCDISpec(claimUID string) error {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()
	err := os.Remove(cdiSpecPath(claimUID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func cdiSpecPath(claimUID string) string {
	return filepath.Join(cdiDir, "liken.sh-"+claimUID+".json")
}

// refreshCDISpecs rewrites each prepared claim's spec with the nodes
// its devices deliver now. It resolves each device the same way
// prepare does, from one walk of sysfs, so a spec written by a
// refresh and a spec written by a prepare always agree.
//
// This cannot repair a container that is already running. The runtime
// injects the nodes at container creation, and a node that moves
// under a running container stays wrong until the pod restarts. What
// it prevents is a stale file that every later pod would receive.
func refreshCDISpecs(sysRoot string) {
	entries, err := os.ReadDir(cdiDir)
	if err != nil {
		// No directory means no claim has been prepared on this boot.
		return
	}
	var byName map[string]hardware.Device
	for _, entry := range entries {
		claimUID, ok := claimUIDFromSpecName(entry.Name())
		if !ok {
			continue
		}
		if byName == nil {
			byName = map[string]hardware.Device{}
			for _, d := range hardware.DiscoverDevices(sysRoot, draNaming()) {
				byName[deviceName(d)] = d
			}
		}
		if err := refreshCDISpec(sysRoot, claimUID, byName); err != nil {
			fmt.Fprintf(os.Stderr, "device inventory: refreshing claim %s: %v\n", claimUID, err)
		}
	}
}

// refreshCDISpec rewrites one claim's spec, and writes nothing when
// every device still delivers what the file says.
//
// A device that this machine no longer publishes keeps the nodes it
// had. The claim names hardware that left, and unprepare ends the
// claim when its pods do. An empty edit list would start the next
// pod with no device and no error.
//
// A device that is present with no driver is a different case: the
// program under this claim detached the kernel driver, and the
// kernel driver's nodes went with it. The publish policy resolves
// that shape to the bus node alone, so the refresh rewrites the spec
// to the one node the program uses. Without this rewrite the spec
// keeps a node the program deleted, and the claim's container can
// never restart: the runtime injects the spec's nodes at every
// container creation, and a stat on the deleted node fails it.
func refreshCDISpec(sysRoot, claimUID string, byName map[string]hardware.Device) error {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()

	raw, err := os.ReadFile(cdiSpecPath(claimUID))
	if os.IsNotExist(err) {
		// Unprepare removed the claim between the directory listing
		// and this read.
		return nil
	}
	if err != nil {
		return err
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return err
	}
	changed := false
	for i, device := range spec.Devices {
		// prepare names each CDI device for the claim and the
		// allocated device together, so the allocated name is in the
		// file, and the refresh needs no call to the API server.
		allocated, ok := strings.CutPrefix(device.Name, claimUID+"-")
		if !ok {
			continue
		}
		published, ok := resolveAllocated(allocated, sysRoot, byName)
		if !ok {
			continue
		}
		nodes := deviceNodes(published.Nodes)
		if slices.Equal(nodes, device.ContainerEdits.DeviceNodes) {
			continue
		}
		spec.Devices[i].ContainerEdits.DeviceNodes = nodes
		changed = true
	}
	if !changed {
		return nil
	}
	return writeSpecFile(claimUID, spec.Devices)
}

// claimUIDFromSpecName reads a claim's UID back out of its spec file
// name. A name that does not fit the pattern belongs to another
// writer, or is a temporary file mid-rename, and the refresh leaves
// it alone.
func claimUIDFromSpecName(name string) (string, bool) {
	uid, ok := strings.CutPrefix(name, "liken.sh-")
	if !ok {
		return "", false
	}
	return strings.CutSuffix(uid, ".json")
}
