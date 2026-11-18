package main

// This file writes, reads, and removes one CDI spec for each prepared
// claim. A CDI spec is a JSON file that lists devices and, for each
// one, the environment variables, mounts, and device nodes that the
// container runtime adds to a container that requests it.
//
// An output or draw device connects the container to the compositor.
// Its edits bind-mount the compositor's socket directory and set
// XDG_RUNTIME_DIR to that directory, WAYLAND_DISPLAY to the socket that
// the layout module opened for this claim, and DISPLAY_APP_ID to the
// output's device name. The mount exposes the sockets of every claim on
// the node. Only WAYLAND_DISPLAY points the client at its own, and the
// compositor identifies a claim by the socket that a client connects
// on. Nothing routes by DISPLAY_APP_ID. It is set for consumer images
// that pass it to their toolkit, as --class for chromium or
// --wayland-app-id for mpv.
//
// A control device adds the connector's i2c device node and
// DISPLAY_CONTROL_BUS with its path.
//
// The file is display.liken.sh-<claimUID>.json. machine-operator writes
// liken.sh-<claimUID>.json in the same directory, and each driver reads
// only the files with its own prefix. The name uses the claim's UID, so
// a stale file of a deleted claim cannot be read as the file of a new
// claim with the same name.
//
// The specs are on disk, so they outlast a restart of the operator's
// container. They are the operator's only record of which claims have a
// socket. After a compositor restart closes every socket, the operator
// reads the specs to open the sockets again.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// cdiFiles serializes every read, write, and removal of a spec file.
var cdiFiles sync.Mutex

// cdiDir is the directory where the container runtime reads CDI specs.
// It is a variable so the tests can change it.
var cdiDir = "/var/run/cdi"

// cdiKind is the kind in every CDI device ID this driver returns,
// "<kind>=<name>". Control devices use it too, although it ends in
// "output", because a spec file has one kind, and a second kind would
// need a second file for each claim.
const cdiKind = DriverName + "/output"

const cdiPrefix = DriverName + "-"

// cdiSpec is the part of the CDI spec schema that this driver writes.
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

// The runtime creates the node in the container and allows its major
// and minor numbers in the container's device cgroup. With no hostPath,
// Path is both the host path and the container path.
type cdiDeviceNode struct {
	Path        string `json:"path"`
	Permissions string `json:"permissions,omitempty"`
}

// outputEdits mounts socketDir at the same path in the container, the
// path where the operator's own pod mounts it, so the operator and the
// client use the same socket paths.
//
// When one container requests two outputs, the runtime applies both
// sets of variables and the last value of each wins, so the client
// connects to one screen only. A pod with two screens needs a container
// and a request for each.
func outputEdits(socketDir, socketName, id string) cdiEdits {
	return cdiEdits{
		Env: []string{
			"XDG_RUNTIME_DIR=" + socketDir,
			waylandDisplayVariable + "=" + socketName,
			"DISPLAY_APP_ID=" + id,
		},
		Mounts: []cdiMount{{
			HostPath:      socketDir,
			ContainerPath: socketDir,
			// Connecting to a Unix socket needs write permission.
			Options: []string{"rw", "bind"},
		}},
	}
}

// controlEdits adds the i2c device node of the connector's DDC/CI bus
// and sets DISPLAY_CONTROL_BUS to its path. The kernel numbers i2c
// adapters in the order it registers them, so a client that guessed
// the number could open another card's bus or the system management
// bus. The node is rw because a DDC/CI exchange writes a request before
// it reads the reply.
func controlEdits(node string) cdiEdits {
	return cdiEdits{
		Env:         []string{"DISPLAY_CONTROL_BUS=" + node},
		DeviceNodes: []cdiDeviceNode{{Path: node, Permissions: "rw"}},
	}
}

// writeCDISpec writes a temporary file and renames it, because the
// runtime can read the directory at any time, and a half-written spec
// fails the creation of the container that reads it.
func writeCDISpec(claimUID string, devices []cdiDevice) error {
	cdiFiles.Lock()
	defer cdiFiles.Unlock()

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

// preparedDevices returns the device names in one claim's spec, with
// the claim's UID cut off. Unprepare receives only the claim's UID, and
// the mode record is keyed by connector, so the spec is the only link
// from a claim to its connectors. It returns no devices when the spec
// does not exist, because the kubelet repeats an unprepare that already
// removed it.
func preparedDevices(claimUID string) ([]string, error) {
	spec, err := readCDISpec(claimUID)
	if err != nil {
		return nil, err
	}
	var devices []string
	for _, device := range spec.Devices {
		name, named := strings.CutPrefix(device.Name, claimUID+"-")
		if !named {
			continue
		}
		devices = append(devices, name)
	}
	return devices, nil
}

// readCDISpec returns an empty spec and no error when the file does
// not exist, because callers ask about claims that are already
// unprepared.
func readCDISpec(claimUID string) (cdiSpec, error) {
	cdiFiles.Lock()
	defer cdiFiles.Unlock()

	raw, err := os.ReadFile(cdiSpecPath(claimUID))
	if os.IsNotExist(err) {
		return cdiSpec{}, nil
	}
	if err != nil {
		return cdiSpec{}, err
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return cdiSpec{}, err
	}
	return spec, nil
}

// eachPreparedSpec calls visit with the claim UID and spec of every
// file this driver wrote. It fails on a file that does not parse,
// because the callers use the specs to find which claims have a screen
// or a socket, and a skipped file would make that claim's screen look
// free and leave its socket closed after a compositor restart.
func eachPreparedSpec(visit func(claimUID string, spec cdiSpec)) error {
	cdiFiles.Lock()
	defer cdiFiles.Unlock()

	entries, err := os.ReadDir(cdiDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
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
			return err
		}
		var spec cdiSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			return fmt.Errorf("parsing the CDI spec %s: %w", entry.Name(), err)
		}
		visit(claim, spec)
	}
	return nil
}

// removeCDISpec returns no error for a missing file, because the
// kubelet repeats unprepare until it records a success.
func removeCDISpec(claimUID string) error {
	cdiFiles.Lock()
	defer cdiFiles.Unlock()
	err := os.Remove(cdiSpecPath(claimUID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func cdiSpecPath(claimUID string) string {
	return filepath.Join(cdiDir, cdiPrefix+claimUID+".json")
}

// preparedOutputs returns the output devices that prepared claims
// hold. The Display controller does not apply a declared mode or
// restart the compositor while a claim holds the output, because both
// take the screen from the workload. Control and draw devices are left
// out, because a control claim draws nothing and many draw claims share
// one screen, so neither sets the screen's mode.
func preparedOutputs() (map[string]bool, error) {
	claimed := map[string]bool{}
	err := eachPreparedSpec(func(claimUID string, spec cdiSpec) {
		for _, device := range spec.Devices {
			name, prefixed := strings.CutPrefix(device.Name, claimUID+"-")
			if !prefixed {
				continue
			}
			if _, control := outputOfControl(name); control {
				continue
			}
			if _, draw := outputOfDraw(name); draw {
				continue
			}
			claimed[name] = true
		}
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}
