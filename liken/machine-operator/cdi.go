package main

// Writes the CDI specs that turn a prepared DRA claim into device nodes
// in a container.
//
// The DRA driver's response to the kubelet's prepare call lists CDI
// device IDs of the form kind=name. The kubelet passes them through the
// CRI, and containerd looks each one up in the spec files in cdiDir
// when it creates the container. liken's specs list device nodes and
// nothing else, so the container gets those nodes and the default
// cgroup rules for them, and no privilege.
//
// Each claim has its own spec file, named by the claim's UID, so a
// claim that is deleted and created again with the same name does not
// reuse the old file. The files are on a tmpfs (/var/run links to
// /run). That is enough, because the kubelet prepares every claim again
// after a reboot.
//
// The kubelet prepares a claim once and reuses the result for every
// later pod of the claim, but the device nodes can change in that
// time. A USB device that is unplugged and plugged in again gets a new
// device number, and its usbfs node moves. So every reconcile pass
// rewrites the spec of each prepared claim (refreshCDISpecs), and the
// uevent of the replug starts a pass.

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

// cdiWrites serializes writes to the spec files. The kubelet's prepare
// calls and the reconcile pass both write a claim's file through the
// same temporary path.
var cdiWrites sync.Mutex

// cdiDir is the directory where containerd reads CDI specs.
var cdiDir = "/var/run/cdi"

// cdiSpec has only the fields of the CDI spec schema that liken writes.
type cdiSpec struct {
	Version string      `json:"cdiVersion"`
	Kind    string      `json:"kind"`
	Devices []cdiDevice `json:"devices"`
}

type cdiDevice struct {
	Name           string            `json:"name"`
	Annotations    map[string]string `json:"annotations,omitempty"`
	ContainerEdits cdiEdits          `json:"containerEdits"`
}

type cdiEdits struct {
	DeviceNodes []cdiDeviceNode `json:"deviceNodes"`
}

// cdiDeviceNode is one device node the runtime creates in the container.
// With only Path set, the runtime stats the host node to learn its type
// and numbers. With Type, Major, and Minor set, the runtime creates the
// node itself, and no host node has to exist.
type cdiDeviceNode struct {
	Path     string `json:"path"`
	Type     string `json:"type,omitempty"`
	Major    int    `json:"major,omitempty"`
	Minor    int    `json:"minor,omitempty"`
	FileMode *int   `json:"fileMode,omitempty"`
}

// deviceNodes turns the paths of one published device into CDI device
// nodes. The kernel fixes the major and minor numbers of evdev and
// virtualization nodes, so deviceNodes sets them, with a type and a
// file mode, because the host node can be absent when the kubelet
// prepares the claim. The runtime then creates the node with mknod and
// adds its cgroup rule, and the container can open the device as soon
// as the kernel registers it.
func deviceNodes(paths []string) []cdiDeviceNode {
	nodes := make([]cdiDeviceNode, 0, len(paths))
	for _, path := range paths {
		node := cdiDeviceNode{Path: path}
		if major, minor, ok := hardware.EvdevNumbers(path); ok {
			mode := evdevFileMode
			node.Type, node.Major, node.Minor, node.FileMode = "c", major, minor, &mode
		}
		if minor, ok := virtualizationMinor(path); ok {
			mode := virtualizationFileMode
			node.Type, node.Major, node.Minor, node.FileMode = "c", miscMajor, minor, &mode
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// sameDeviceNodes reports whether two lists describe the same nodes.
// It compares the modes that FileMode points to, because nodes read
// back from a spec file and nodes built again for the same devices
// have equal modes behind different pointers.
func sameDeviceNodes(a, b []cdiDeviceNode) bool {
	return slices.EqualFunc(a, b, func(x, y cdiDeviceNode) bool {
		sameMode := (x.FileMode == nil) == (y.FileMode == nil) && (x.FileMode == nil || *x.FileMode == *y.FileMode)
		return x.Path == y.Path && x.Type == y.Type && x.Major == y.Major && x.Minor == y.Minor && sameMode
	})
}

// evdevFileMode is the mode of an evdev node that the runtime creates
// with mknod. The runtime copies the mode of a host node that it can
// stat, but an evdev node can be absent when the container starts, and
// a node created with no mode can be opened only by a process with
// CAP_DAC_OVERRIDE. The container's user owns the node, so it needs
// only read and write.
const evdevFileMode = 0o600

// cdiKind is the kind part of liken's CDI device IDs, "<kind>=<name>".
const cdiKind = "liken.sh/device"

func writeCDISpec(claimUID string, devices []cdiDevice) error {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()
	return writeSpecFile(claimUID, devices)
}

// writeSpecFile writes a claim's spec file. The caller holds cdiWrites.
// It writes a temporary file and renames it, because containerd can
// read the directory at any time, and a container creation that reads
// a half-written spec fails.
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

// removeCDISpec deletes a claim's spec file. A file that is already
// gone is not an error, because the kubelet retries unprepare when it
// is not sure the call succeeded.
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

// refreshCDISpecs rewrites the spec of each prepared claim with the
// device nodes that its devices have now. It resolves the devices with
// resolveAllocated, as prepare does, so a refresh and a prepare write
// the same spec.
//
// A running container keeps its nodes, because the runtime creates
// them only when it creates the container. The refresh corrects the
// nodes of later containers.
func refreshCDISpecs(sysRoot string, out *passOutcome, notes machineEvents) {
	entries, err := os.ReadDir(cdiDir)
	if err != nil {
		// The directory is missing until the first prepare of this boot.
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
		if err := refreshCDISpec(sysRoot, claimUID, byName, out, notes); err != nil {
			fmt.Fprintf(os.Stderr, "device inventory: refreshing claim %s: %v\n", claimUID, err)
			out.fail("refreshing the CDI specification of claim "+claimUID, err)
		}
	}
}

// refreshCDISpec rewrites one claim's spec when a device's nodes differ
// from the ones the file lists.
//
// When a device cannot be given to the container, its spec lists a node
// that does not exist, so the next container of the claim fails to
// start:
//
//   - deviceAbsentNode when the device is no longer published. The
//     kernel can give the old node names to a different device, and an
//     empty node list would start the container with no device and no
//     error.
//   - serioAbsentNode for the devices of a CEC adapter, and for a claim
//     that was allocated to the adapter's tty or USB interface before
//     spec.serio declared it. A container that opens the old tty or
//     usbfs node can end the serio attachment (serioFailsClosed).
//   - withheldNode for a disk that disk protection withholds, because
//     the disk can back the machine's own filesystems (protection.go).
//
// When the hardware returns at the same address, or the disk backs no
// role, the refresh writes the real nodes back. The claim itself stays
// prepared until its pods end and the kubelet unprepares it.
//
// A program in the claim's pod can detach the kernel driver from a
// device, and the driver's nodes go with it. resolveAllocated then
// returns the device's bus node, and the refresh writes that node. A
// spec that kept the deleted node would stop the container from
// restarting, because the runtime creates the spec's nodes at every
// container creation and fails on a missing one.
//
// When a device's USB unit leaves its address, markUnplugged marks the
// device in the spec once, and the refresh posts a DeviceUnplugged
// Event (unplugged.go).
//
// The error for a spec that does not decode wraps fs.ErrInvalid, so the
// pass outcome counts it as lasting and retries it slowly. Only a new
// prepare replaces the file.
func refreshCDISpec(sysRoot, claimUID string, byName map[string]hardware.Device, out *passOutcome, notes machineEvents) error {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()

	raw, err := os.ReadFile(cdiSpecPath(claimUID))
	if os.IsNotExist(err) {
		// Unprepare removed the claim after the directory listing.
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
		// Prepare names each CDI device "<claim UID>-<allocated
		// device>", so the refresh reads the allocated device from the
		// file and makes no call to the API server.
		allocated, ok := strings.CutPrefix(device.Name, claimUID+"-")
		if !ok {
			continue
		}
		if markUnplugged(&spec.Devices[i], sysRoot, byName) {
			notes.warning(reasonDeviceUnplugged, unpluggedMessage(allocated, spec.Devices[i]))
			changed = true
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

// deviceAbsentNode is the node a claim's spec lists while its hardware
// is absent. The node does not exist, so the runtime fails to create
// the next container of the claim, and the kubelet retries it with its
// restart backoff.
const deviceAbsentNode = "/dev/liken.sh/device-absent"

// claimUIDFromSpecName returns the claim UID in a spec file name. It
// returns false for another driver's file and for a temporary file
// that writeSpecFile has not renamed yet.
func claimUIDFromSpecName(name string) (string, bool) {
	uid, ok := strings.CutPrefix(name, "liken.sh-")
	if !ok {
		return "", false
	}
	return strings.CutSuffix(uid, ".json")
}
