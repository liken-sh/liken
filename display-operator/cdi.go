package main

// Writing CDI specs: how a prepared claim becomes a Wayland
// connection in a consumer's container.
//
// The Container Device Interface connects two things: which device to
// use, and what appears inside the container. A JSON file in a
// well-known directory names devices and the edits that grant one to a
// container. liken's own driver writes device nodes there and nothing
// else, and that is a decision about what an operating system
// delivers, not a limit in CDI. A Wayland client needs no device node
// at all. It needs the compositor's socket and the app-id that the
// compositor routes to the output the claim allocated, so this
// driver's edits are a mount and three environment variables.
//
//   - The mount grants the socket directory, at the same path inside
//     the container as on the host.
//   - XDG_RUNTIME_DIR names the directory where a Wayland client
//     looks for its socket.
//   - WAYLAND_DISPLAY names the socket inside that directory.
//   - DISPLAY_APP_ID is the app-id of the allocated output.
//
// The client still has to pass the app-id to its own toolkit, because
// no Wayland client reads a variable that chromium and mpv do not
// define. The pod spec supplies the flag and the variable is what the
// flag reads: --class=$(DISPLAY_APP_ID) for chromium,
// --wayland-app-id=$(DISPLAY_APP_ID) for mpv.
//
// The file name starts with this driver's own prefix,
// display.liken.sh-<claimUID>.json. liken writes
// liken.sh-<claimUID>.json in the same directory and reads back only
// the files whose names start with its own prefix, so the two drivers
// never read or overwrite each other's specs.
//
// Each claim gets one file, named by the claim's UID rather than by
// its namespace and name. A claim that is deleted and recreated under
// the same name is a different grant, and its file must not collide
// with a stale one.
//
// Nothing refreshes these files. What they hold is the socket
// directory, which the pod mounts at a fixed path, and the app-id,
// which version 0 derives from the connector name. Neither changes
// while the operator runs, so a spec written at prepare time stays
// correct until the claim ends.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// cdiWrites serializes the writes to these files. The kubelet may
// prepare two claims at once, and both stage a write through the same
// temporary path.
var cdiWrites sync.Mutex

// cdiDir is the directory where the container runtime looks for CDI
// specs. It is a variable so the tests can change it.
var cdiDir = "/var/run/cdi"

// cdiKind identifies this driver's CDI devices, the same way the
// driver name identifies its slices. A CDI device ID has the form
// "<kind>=<name>".
const cdiKind = DriverName + "/output"

// cdiPrefix is what separates this driver's spec files from liken's in
// the shared directory.
const cdiPrefix = DriverName + "-"

// cdiSpec holds the part of the CDI spec schema that this operator
// writes. The delivery is a mount and environment variables, so the
// struct omits the field for device nodes.
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
	Env    []string   `json:"env,omitempty"`
	Mounts []cdiMount `json:"mounts,omitempty"`
}

type cdiMount struct {
	HostPath      string   `json:"hostPath"`
	ContainerPath string   `json:"containerPath"`
	Options       []string `json:"options,omitempty"`
}

// outputEdits builds what one allocated output delivers.
//
// The host path and the container path are the same string. The
// operator's own pod mounts the socket directory from the host at the
// path it names here, so what the operator sees and what the host has
// are one path, and a consumer that mounts the same path reads the
// same socket. A path that differed between the two would make the
// operator read its own mount table.
//
// A claim that allocates two outputs into one container delivers two
// app-ids, and only one DISPLAY_APP_ID survives: CDI applies the edits
// in order and the last value wins. One container drives one screen.
// A pod that drives two screens runs two containers, each naming its
// own request.
func outputEdits(socketDir, socketName, id string) cdiEdits {
	return cdiEdits{
		Env: []string{
			"XDG_RUNTIME_DIR=" + socketDir,
			"WAYLAND_DISPLAY=" + socketName,
			"DISPLAY_APP_ID=" + id,
		},
		Mounts: []cdiMount{{
			HostPath:      socketDir,
			ContainerPath: socketDir,
			// rw, because connecting to a Unix socket needs write
			// permission on the socket. bind, because this is a
			// directory on the host and not a filesystem to mount.
			Options: []string{"rw", "bind"},
		}},
	}
}

// writeCDISpec writes one claim's devices where the runtime finds
// them. The write is atomic: the runtime may list the directory at any
// moment, and a half-written spec would fail every container creation
// that read it at that moment.
func writeCDISpec(claimUID string, devices []cdiDevice) error {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()

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
// counts as success, because unprepare must be idempotent: the kubelet
// repeats it whenever it has no record that the call succeeded.
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
