package main

// Writing CDI specs: how a prepared claim becomes a Wayland
// connection, or a control channel, in a consumer's container.
//
// The Container Device Interface connects two things: which device to
// use, and what appears inside the container. A JSON file in a
// well-known directory names devices and the edits that grant one to
// a container. What the edits hold depends on whether the claim
// allocated a Wayland connection or a control channel. A Wayland
// client needs no device node at all: it needs the compositor's socket
// and the app-id that the compositor routes to the allocated output,
// so the edits are a mount and three environment variables. The output
// device and the draw device both deliver this. A control device's
// client is the opposite case: it needs exactly one device node, the
// connector's i2c wire, and the variable that names it.
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
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
//
// One kind carries every device type. A spec file states one kind, so
// a second kind would mean a second file for the same claim: a second
// write to fail halfway, and a second thing an unprepare must remove
// while staying idempotent. The claim's UID and the device's own name
// already make every device ID unique, so a control kind would buy
// nothing the name does not carry. The cost is the word "output" in a
// control device's ID, which is a name, not a claim about what it
// delivers.
const cdiKind = DriverName + "/output"

// cdiPrefix is what separates this driver's spec files from liken's in
// the shared directory.
const cdiPrefix = DriverName + "-"

// cdiSpec holds the part of the CDI spec schema that this operator
// writes: environment, mounts, and device nodes, because a control
// device's whole delivery is a node.
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
	Mounts      []cdiMount      `json:"mounts,omitempty"`
	DeviceNodes []cdiDeviceNode `json:"deviceNodes,omitempty"`
}

type cdiMount struct {
	HostPath      string   `json:"hostPath"`
	ContainerPath string   `json:"containerPath"`
	Options       []string `json:"options,omitempty"`
}

// A device node entry does two things at once: the runtime creates
// the node inside the container, and it adds the node's major and
// minor numbers to the container's device cgroup, which is what makes
// an open of it succeed. The path is the host's path, because the
// runtime reads the node there to learn its numbers, and it is the
// container's path too when the entry names no hostPath of its own.
type cdiDeviceNode struct {
	Path        string `json:"path"`
	Permissions string `json:"permissions,omitempty"`
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

// ControlEdits builds what one allocated control delivers, which is
// two things. The node is the DDC/CI wire itself, and a container
// that holds it speaks to the panel with no operator in the path. The
// environment variable is the node's path, because the kernel numbers
// i2c adapters in the order it registers them, and a consumer that
// guessed a number would read another card's bus, or a system
// management bus.
//
// The permissions are rw because every DDC/CI exchange writes a
// request before it reads the reply, so a read-only node answers
// nothing.
func controlEdits(node string) cdiEdits {
	return cdiEdits{
		Env:         []string{"DISPLAY_CONTROL_BUS=" + node},
		DeviceNodes: []cdiDeviceNode{{Path: node, Permissions: "rw"}},
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

// PreparedDevices names the outputs one claim's spec granted.
//
// Unprepare carries a claim's UID and nothing else, and the
// mode record is keyed by connector, so this file is what ties the two
// together. It is the durable record: it outlives a restart of the
// operator's container, where a map in this process would not, and it
// dies with the pod, exactly as the mode record does.
//
// A spec that is not there answers no devices. The kubelet
// repeats an unprepare whenever it has no record that the call
// succeeded, so the second call finds the file gone and has nothing
// left to release.
func preparedDevices(claimUID string) ([]string, error) {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()

	raw, err := os.ReadFile(cdiSpecPath(claimUID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, err
	}
	var devices []string
	for _, device := range spec.Devices {
		// The claim's UID prefixes every device name in the
		// file, so the device is what is left when the prefix comes
		// off.
		name, named := strings.CutPrefix(device.Name, claimUID+"-")
		if !named {
			continue
		}
		devices = append(devices, name)
	}
	return devices, nil
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

// preparedOutputs names the output devices a prepared claim
// holds right now, read from the specs this driver wrote. It is what
// tells a resting mode and a compositor restart to wait: both take the
// screen away from whatever is drawing on it, and a claim is a
// workload's hold on that screen.
//
// The control and draw companions are not in the answer. A
// control claim drives the panel's own wire and no screen, and many
// draw claims share one screen and none of them owns its mode.
//
// The read runs once per pass of the Display controller, a
// directory listing and one small file per prepared claim, on a
// tmpfs. If the pass ever runs faster than it does now, the lever is
// to hold this answer between passes and drop it on a prepare or an
// unprepare.
func preparedOutputs() (map[string]bool, error) {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()

	entries, err := os.ReadDir(cdiDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for _, entry := range entries {
		claim, mine := strings.CutPrefix(entry.Name(), cdiPrefix)
		if !mine {
			continue
		}
		claim, named := strings.CutSuffix(claim, ".json")
		if !named {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(cdiDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var spec cdiSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			return nil, fmt.Errorf("reading %s: %w", entry.Name(), err)
		}
		for _, device := range spec.Devices {
			name, prefixed := strings.CutPrefix(device.Name, claim+"-")
			if !prefixed {
				continue
			}
			if _, control := outputOfControl(name); control {
				continue
			}
			if _, draw := outputOfDraw(name); draw {
				continue
			}
			held[name] = true
		}
	}
	return held, nil
}
