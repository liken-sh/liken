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
// /var/run. The kubelet re-prepares every claim after a reboot, so
// each file only needs to last one boot, and a tmpfs directory
// removes the files automatically at that point.

import (
	"encoding/json"
	"os"
	"path/filepath"
)

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

// cdiKind identifies liken's CDI devices, the same way the driver
// name identifies liken's slices. A CDI device ID has the form
// "<kind>=<name>".
const cdiKind = "liken.sh/device"

// writeCDISpec writes one claim's devices to a file where the
// runtime can find them. The write is atomic. containerd may list
// the directory at any moment, and a half-written spec would fail
// every container creation that reads it at that moment.
func writeCDISpec(claimUID string, devices []cdiDevice) error {
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
	err := os.Remove(cdiSpecPath(claimUID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func cdiSpecPath(claimUID string) string {
	return filepath.Join(cdiDir, "liken.sh-"+claimUID+".json")
}
