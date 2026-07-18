package main

// Writing CDI specs: how a prepared claim becomes device nodes in a
// container.
//
// The Container Device Interface is the seam between "which device"
// and "what appears in the container": a JSON file in a well-known
// directory describes named devices and the edits granting one to a
// container (device nodes here; mounts and env vars exist in the
// spec for drivers that need them). The DRA driver answers the
// kubelet's prepare call with CDI device IDs — kind=name strings —
// the kubelet passes them through the CRI, and containerd resolves
// them against these files when it creates the container. No
// privilege is involved anywhere: the pod gets exactly the nodes the
// spec names, with the default cgroup device rules to match.
//
// One spec file per claim, named by the claim's UID. The UID rather
// than namespace/name, deliberately: a claim deleted and recreated
// under the same name is a different grant, and its file must not
// collide with a stale one. The specs live under /var/run — kubelet
// re-prepares every claim after a reboot, so the files' natural
// lifetime is the boot, which is exactly what a tmpfs directory
// enforces for free.

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// cdiDir is where containerd looks for dynamically-written CDI
// specs. A variable for the tests' sake.
var cdiDir = "/var/run/cdi"

// The honest subset of the CDI spec schema: liken's deliveries are
// device nodes, nothing else.
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

// cdiKind qualifies liken's CDI devices, the same way the driver
// name qualifies its slices. A CDI device ID is "<kind>=<name>".
const cdiKind = "liken.sh/device"

// writeCDISpec publishes one claim's devices for the runtime to
// find, atomically: containerd may list the directory at any moment,
// and a half-written spec would fail every container creation that
// raced it.
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

// removeCDISpec retires a claim's spec; a spec already gone is
// success, because unprepare must be idempotent (the kubelet retries
// it on any doubt).
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
