package main

// The CDI (Container Device Interface) spec files that give a
// consumer's container what a prepared claim grants: the evdev nodes
// of a controller, or a mount of bluetoothd's bus socket for the media
// bus.
//
// The container runtime reads the files in cdiDir and applies a file's
// edits to a container that requests one of its devices. Each claim has
// one file, bluetooth.liken.sh-<claimUID>.json. liken writes its own
// liken.sh-<claimUID>.json files in the same directory, and each driver
// reads and deletes only the files with its own prefix. The name uses
// the claim's UID, not its namespace and name, so a claim that is
// deleted and created again under the same name does not get the old
// claim's file.
//
// The operator writes a file once, at prepare, and never rewrites it,
// because the kubelet prepares a claim once and reuses the result for
// every pod that references it. A controller's claim grants the relay's
// virtual input node. That node keeps its number while the operator
// holds the uinput fd, so a controller that reconnects as a different
// eventN does not make the file wrong.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// cdiWrites serializes writes to the spec files. The DRA prepare and
// unprepare calls and the reconcile pass can run at the same time, and
// writes of one file share one temporary path.
var cdiWrites sync.Mutex

// cdiDir is the directory where the container runtime reads CDI
// specs. Tests change it.
var cdiDir = "/var/run/cdi"

// cdiKind is the CDI kind of every device this driver writes. A CDI
// device ID has the form "<kind>=<name>". The kind is only a namespace
// for one writer's device names, so the media bus uses the same kind
// as a controller.
const cdiKind = DriverName + "/controller"

// busDir is the directory of bluetoothd's bus socket, at the same path
// on the host and in every container that mounts it. The pod mounts it
// as a hostPath volume, not an emptyDir, because a CDI mount needs a
// fixed host path and the kubelet chooses the host path of an emptyDir.
const busDir = "/var/run/bluetooth.liken.sh/dbus"

// A consumer's container gets busAddress in DBUS_SYSTEM_BUS_ADDRESS, so
// PipeWire connects to this bluetoothd with no other configuration.
const (
	busVariable = "DBUS_SYSTEM_BUS_ADDRESS"
	busAddress  = "unix:path=" + busDir + "/system_bus_socket"
)

// cdiPrefix starts the name of each spec file this driver writes.
const cdiPrefix = DriverName + "-"

// cdiSpec is the part of the CDI spec schema that this driver writes.
type cdiSpec struct {
	Version string      `json:"cdiVersion"`
	Kind    string      `json:"kind"`
	Devices []cdiDevice `json:"devices"`
}

// cdiDevice is the grant for one allocated device. Its annotations
// record the input classes and axis values that the claim asked for,
// so after a restart eachDeliveredDevice reads them back from the same
// file that lists the consumer's nodes. The container runtime rejects
// a spec with a field outside the CDI schema, so this data cannot go
// in fields of its own. A claim that asks for the defaults writes no
// annotations.
type cdiDevice struct {
	Name           string            `json:"name"`
	ContainerEdits cdiEdits          `json:"containerEdits"`
	Annotations    map[string]string `json:"annotations,omitempty"`
}

// inputsAnnotation holds the requested input classes as a
// comma-separated list, and axesAnnotation holds the applied axis
// values in the format that axes.go writes.
const (
	inputsAnnotation = DriverName + "/inputs"
	axesAnnotation   = DriverName + "/axes"
)

// inputs returns the recorded input classes, or nil when the device
// has no annotation.
func (d cdiDevice) inputs() []string {
	recorded, found := d.Annotations[inputsAnnotation]
	if !found {
		return nil
	}
	return strings.Split(recorded, ",")
}

// axes returns the recorded axis values, or "" when the device has no
// annotation.
func (d cdiDevice) axes() string {
	return d.Annotations[axesAnnotation]
}

// cdiAnnotations returns nil for a claim that wants every input
// class and sets no axis.
func cdiAnnotations(inputs []string, axes string) map[string]string {
	annotations := map[string]string{}
	if len(inputs) > 0 {
		annotations[inputsAnnotation] = strings.Join(inputs, ",")
	}
	if axes != "" {
		annotations[axesAnnotation] = axes
	}
	if len(annotations) == 0 {
		return nil
	}
	return annotations
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

// busEdits mounts busDir read-only. Connecting to a Unix socket needs
// write permission on the socket file, which a read-only mount does
// not remove, and the container has no reason to create files in the
// directory.
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

func deviceNodes(paths []string) []cdiDeviceNode {
	nodes := make([]cdiDeviceNode, 0, len(paths))
	for _, path := range paths {
		nodes = append(nodes, cdiDeviceNode{Path: path})
	}
	return nodes
}

func writeCDISpec(claimUID string, devices []cdiDevice) error {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()
	return writeSpecFile(claimUID, devices)
}

// writeSpecFile needs the caller to hold cdiWrites. It writes a
// temporary file and renames it, because the runtime can read the
// directory at any time, and a partly written spec fails the creation
// of the container that reads it.
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
// gone is not an error, because the kubelet repeats unprepare when it
// has no record that an earlier call succeeded.
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

// claimUIDFromSpecName returns false for a name without cdiPrefix,
// which another driver wrote, and for a name without the .json suffix,
// such as the temporary file of a write in progress.
func claimUIDFromSpecName(name string) (string, bool) {
	uid, ok := strings.CutPrefix(name, cdiPrefix)
	if !ok {
		return "", false
	}
	return strings.CutSuffix(uid, ".json")
}
