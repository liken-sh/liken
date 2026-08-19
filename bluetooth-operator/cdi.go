package main

// Writing CDI specs: how a prepared claim becomes evdev nodes, or the
// bus socket of this pod's bluetoothd, in a consumer's container.
//
// The Container Device Interface connects two things: which device to
// use, and what appears inside the container. A JSON file in a
// well-known directory names devices and the edits that grant one to
// a container. A controller's edits are device nodes only, because a
// program reads a gamepad by opening /dev/input/eventN, and nothing
// about the device is configuration. The media bus takes the other
// two kinds of edit and no device node: a mount of the bus directory,
// and DBUS_SYSTEM_BUS_ADDRESS, the variable a sound server reads to
// find the bus.
//
// The file name starts with this driver's own prefix,
// bluetooth.liken.sh-<claimUID>.json. liken writes
// liken.sh-<claimUID>.json in the same directory and reads back only
// the files whose names start with its own prefix, so the two drivers
// never read or overwrite each other's specs.
//
// Each claim gets one file, named by the claim's UID rather than by
// its namespace and name. A claim that is deleted and recreated under
// the same name is a different grant, and its file must not collide
// with a stale one.
//
// A file also has to stay correct for the whole boot. The kubelet
// prepares a claim once and reuses the answer for every later pod
// that names it, and an evdev node moves under it: the kernel numbers
// input devices in the order they appear, so a controller that
// reconnects usually comes back as a different eventN. The reconcile
// pass rewrites every prepared claim's file from the same sysfs walk
// that publishes the slice.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// cdiWrites serializes the writes to these files. The kubelet's
// prepare calls and the reconcile pass both write them, and both
// stage a write through the same temporary path.
var cdiWrites sync.Mutex

// cdiDir is the directory where the container runtime looks for CDI
// specs. It is a variable so the tests can change it.
var cdiDir = "/var/run/cdi"

// cdiKind identifies this driver's CDI devices, the same way the
// driver name identifies its slices. A CDI device ID has the form
// "<kind>=<name>".
//
// The media bus takes the same kind. A CDI kind namespaces one
// writer's device IDs. It is not a taxonomy of the devices, and a
// second kind would rename every ID this driver has already written.
const cdiKind = DriverName + "/controller"

// busDir is the directory that holds bluetoothd's bus socket, on the
// host and inside every container that mounts it. The one constant is
// both ends of the claim holder's mount, because the pod's own bus
// volume is a hostPath at this same path. A CDI mount names a host
// path, which is why that volume is not an emptyDir.
const busDir = "/var/run/bluetooth.liken.sh/dbus"

// The address a sound server dials, and the environment variable it
// reads the address from. DBUS_SYSTEM_BUS_ADDRESS is D-Bus's own
// standard variable, so PipeWire finds this bus with no configuration
// of its own.
const (
	busVariable = "DBUS_SYSTEM_BUS_ADDRESS"
	busAddress  = "unix:path=" + busDir + "/system_bus_socket"
)

// cdiPrefix separates this driver's spec files from liken's in the
// shared directory.
const cdiPrefix = DriverName + "-"

// cdiSpec holds the part of the CDI spec schema that this operator
// writes: device nodes for a controller, a mount and an environment
// variable for the media bus. Each field is omitted when a device
// grants nothing of its kind.
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
	Env         []string        `json:"env,omitempty"`
	DeviceNodes []cdiDeviceNode `json:"deviceNodes,omitempty"`
	Mounts      []cdiMount      `json:"mounts,omitempty"`
}

type cdiDeviceNode struct {
	Path string `json:"path"`
}

type cdiMount struct {
	HostPath      string   `json:"hostPath"`
	ContainerPath string   `json:"containerPath"`
	Options       []string `json:"options,omitempty"`
}

// busEdits builds what an allocated media bus grants a container: the
// directory that holds bluetoothd's socket, and the address of the
// socket inside it.
//
// The mount is read-only. Connecting to a Unix socket needs write
// permission on the socket itself, not on the file system that holds
// it, so a read-only mount still connects, and the holder has no
// reason to create anything in this directory.
//
// The edits are the same on every pass, because the socket's path is
// fixed. Only a controller's evdev nodes move, so only they need the
// refresh.
func busEdits() cdiEdits {
	return cdiEdits{
		Env: []string{busVariable + "=" + busAddress},
		Mounts: []cdiMount{{
			HostPath:      busDir,
			ContainerPath: busDir,
			Options:       []string{"ro", "rbind", "rprivate", "nosuid", "nodev"},
		}},
	}
}

// deviceNodes turns the paths one controller delivers into the
// container edits that grant them.
func deviceNodes(paths []string) []cdiDeviceNode {
	nodes := make([]cdiDeviceNode, 0, len(paths))
	for _, path := range paths {
		nodes = append(nodes, cdiDeviceNode{Path: path})
	}
	return nodes
}

// writeCDISpec writes one claim's devices where the runtime finds
// them.
func writeCDISpec(claimUID string, devices []cdiDevice) error {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()
	return writeSpecFile(claimUID, devices)
}

// writeSpecFile is the write itself, with the lock already held. It
// is atomic. The runtime may list the directory at any moment, and a
// half-written spec would fail every container creation that read it
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

// removeCDISpec deletes a claim's spec file. An already absent file
// counts as success, because unprepare must be idempotent: the
// kubelet repeats it whenever it has no record that the call
// succeeded.
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
	return filepath.Join(cdiDir, cdiPrefix+claimUID+".json")
}

// claimUIDFromSpecName reads a claim's UID back out of a spec file
// name. A name without this driver's prefix belongs to liken or to
// another writer, and a name that is a temporary file mid-rename ends
// in .json.tmp, so both fall out of the pattern and the refresh
// leaves them alone.
func claimUIDFromSpecName(name string) (string, bool) {
	uid, ok := strings.CutPrefix(name, cdiPrefix)
	if !ok {
		return "", false
	}
	return strings.CutSuffix(uid, ".json")
}

// refreshCDISpecs rewrites each prepared claim's spec with the nodes
// its controller registers now. It resolves each device the same way
// prepare does, from one sysfs walk, so a spec written by a refresh
// and a spec written by a prepare always agree.
//
// This cannot repair a container that already runs. The runtime
// injects the nodes when it creates the container, and a node that
// moves under a running container stays wrong until the pod restarts.
// What it prevents is a stale file that every later pod would
// receive. A controller that reconnects as event6 while the spec
// still says event5 would give the next pod a node that does not
// exist, and the runtime fails a container creation on a node it
// cannot stat.
func refreshCDISpecs(nodes map[string][]string) {
	entries, err := os.ReadDir(cdiDir)
	if err != nil {
		// No directory means no claim has been prepared on this boot.
		return
	}
	for _, entry := range entries {
		claimUID, ok := claimUIDFromSpecName(entry.Name())
		if !ok {
			continue
		}
		if err := refreshCDISpec(claimUID, nodes); err != nil {
			fmt.Fprintf(os.Stderr, "refreshing the spec for claim %s: %v\n", claimUID, err)
		}
	}
}

// refreshCDISpec rewrites one claim's spec, and writes nothing when
// every device still delivers what the file says.
//
// A controller that is disconnected registers no evdev node, and the
// spec keeps the nodes it had. An empty edit list would start the
// next pod with no device and no error. The taint on the device holds
// that pod back until the controller returns.
func refreshCDISpec(claimUID string, nodes map[string][]string) error {
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
		// prepare names each CDI device for the claim and the allocated
		// device together, so the allocated name is in the file and the
		// refresh needs no call to the API server.
		allocated, ok := strings.CutPrefix(device.Name, claimUID+"-")
		if !ok {
			continue
		}
		// A media bus entry stays as prepare wrote it: its delivery
		// names a fixed path. Its name is not a MAC either, so the
		// lookup below would derive a key that names nothing.
		if isMediaBusName(allocated) {
			continue
		}
		current, ok := nodes[macFromDeviceName(allocated)]
		if !ok || len(current) == 0 {
			continue
		}
		edits := deviceNodes(current)
		if slices.Equal(edits, device.ContainerEdits.DeviceNodes) {
			continue
		}
		spec.Devices[i].ContainerEdits.DeviceNodes = edits
		changed = true
	}
	if !changed {
		return nil
	}
	return writeSpecFile(claimUID, spec.Devices)
}
