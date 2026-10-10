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
// from the same sysfs walk that publishes the inventory, and the
// uevent of the replug wakes that pass.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/liken-sh/liken/liken/hardware"
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

// cdiDeviceNode is one node the runtime injects. A node named by
// path alone is one the runtime reads from the host: it stats the
// path to learn the node's kind and numbers. Type, Major, and Minor
// state those facts instead, and a node that states them needs no
// host node at all.
type cdiDeviceNode struct {
	Path     string `json:"path"`
	Type     string `json:"type,omitempty"`
	Major    int    `json:"major,omitempty"`
	Minor    int    `json:"minor,omitempty"`
	FileMode *int   `json:"fileMode,omitempty"`
}

// deviceNodes turns the paths one published device delivers into the
// container edits that grant them. A path whose numbers the kernel
// fixes carries those numbers, because the node it names can be
// absent when the kubelet prepares the claim. The runtime then
// creates the node with mknod and writes the matching cgroup rule,
// and the container can open the device the moment the kernel
// registers it.
func deviceNodes(paths []string) []cdiDeviceNode {
	nodes := make([]cdiDeviceNode, 0, len(paths))
	for _, path := range paths {
		node := cdiDeviceNode{Path: path}
		if major, minor, ok := hardware.EvdevNumbers(path); ok {
			mode := evdevFileMode
			node.Type, node.Major, node.Minor, node.FileMode = "c", major, minor, &mode
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// sameDeviceNodes answers whether two lists grant the same nodes. A
// node's FileMode is a pointer, so the comparison reads the mode, not
// the pointer: a spec read back from its file and the nodes built again
// for the same devices hold equal modes behind different pointers.
func sameDeviceNodes(a, b []cdiDeviceNode) bool {
	return slices.EqualFunc(a, b, func(x, y cdiDeviceNode) bool {
		sameMode := (x.FileMode == nil) == (y.FileMode == nil) && (x.FileMode == nil || *x.FileMode == *y.FileMode)
		return x.Path == y.Path && x.Type == y.Type && x.Major == y.Major && x.Minor == y.Minor && sameMode
	})
}

// evdevFileMode is the mode the runtime gives a node it creates with
// mknod. The runtime copies the mode of a host node it can stat, but a
// node in the evdev range can be absent when the container starts, and
// a node created with no mode is openable only by a process that holds
// CAP_DAC_OVERRIDE. The owner is the container's own user, so owner
// read and write is what the program needs.
const evdevFileMode = 0o600

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
func refreshCDISpecs(sysRoot string, out *passOutcome) {
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
			for _, d := range hardware.DiscoverInventory(sysRoot, draNaming()) {
				byName[deviceName(d)] = d
			}
		}
		if err := refreshCDISpec(sysRoot, claimUID, byName, out); err != nil {
			fmt.Fprintf(os.Stderr, "device inventory: refreshing claim %s: %v\n", claimUID, err)
			out.fail("refreshing the CDI specification of claim "+claimUID, err)
		}
	}
}

// refreshCDISpec rewrites one claim's spec, and writes nothing when
// every device still delivers what the file says.
//
// A device that this machine no longer publishes names
// deviceAbsentNode. The claim names hardware that left, and unprepare
// ends the claim when its pods do. Its old nodes would hand the next
// container whatever device the kernel gave those names since, and an
// empty edit list would start that container with no device and no
// error. When the hardware returns at the same address, the refresh
// writes its nodes back.
//
// A CEC adapter's devices name their own absent node, serioAbsentNode, so
// a container fails to start rather than open a node whose number
// another device may hold. So does a claim allocated to the adapter's
// interface before spec.serio declared it: its old nodes are the tty
// and the usbfs node, which end the attachment (serioFailsClosed).
//
// A disk that the protection withholds names withheldNode, for the
// same reason: the next container fails to start rather than open a
// disk that may back the machine's own filesystems (protection.go).
// When the facts read again and the disk backs no role, the refresh
// writes its nodes back.
//
// A device that is present with no driver is a different case: the
// program under this claim detached the kernel driver, and the
// kernel driver's nodes went with it. The publish policy resolves
// that shape to the bus node alone, so the refresh rewrites the spec
// to the one node the program uses. Without this rewrite the spec
// keeps a node the program deleted, and the claim's container can
// never restart: the runtime injects the spec's nodes at every
// container creation, and a stat on the deleted node fails it.
//
// A spec that does not decode is wrapped in fs.ErrInvalid: nothing but
// a new prepare replaces it, so the pass's outcome retries it slowly.
func refreshCDISpec(sysRoot, claimUID string, byName map[string]hardware.Device, out *passOutcome) error {
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
		return fmt.Errorf("decoding %s: %w: %w", cdiSpecPath(claimUID), err, fs.ErrInvalid)
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
		serio := declaredSerio()
		published, err := resolveAllocated(allocated, sysRoot, byName, serio, currentProtection())
		switch {
		case errors.Is(err, errWithheld):
			published = publishedDevice{Nodes: []string{withheldNode}}
		case err != nil && serioFailsClosed(allocated, byName, serio):
			published = publishedDevice{Nodes: []string{serioAbsentNode}}
		case err != nil:
			published = publishedDevice{Nodes: []string{deviceAbsentNode}}
		}
		nodes := deviceNodes(published.Nodes)
		if sameDeviceNodes(nodes, device.ContainerEdits.DeviceNodes) {
			continue
		}
		spec.Devices[i].ContainerEdits.DeviceNodes = nodes
		changed = true
	}
	if !changed {
		return nil
	}
	if err := writeSpecFile(claimUID, spec.Devices); err != nil {
		return err
	}
	out.wrote("refreshing the CDI specification of claim " + claimUID)
	return nil
}

// deviceAbsentNode is the node a prepared claim's spec names while its
// hardware is absent. No such node exists, so the runtime fails to
// create the next container that holds the claim, and the kubelet
// retries it under its restart backoff. A container that started
// before the hardware left keeps the nodes it received.
const deviceAbsentNode = "/dev/liken.sh/device-absent"

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
