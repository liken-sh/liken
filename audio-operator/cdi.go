package main

// Writes the CDI specs that give a consumer's container the PipeWire
// socket and the name of the PipeWire node to play to or record from.
//
// A CDI spec is a JSON file that names devices and the edits that grant
// each one to a container. Here the edits are a mount and two
// environment variables, and no device node, because a consumer opens no
// PCM device. It connects to PipeWire, and PipeWire routes its streams
// to the named node.
//
// PipeWire's client library reads both variables. A PIPEWIRE_REMOTE that
// starts with a slash is an absolute socket path, so the library does
// not look in the runtime directory. PIPEWIRE_NODE sets target.object on
// every stream that the client creates.
//
// The file name is audio.liken.sh-<claimUID>.json. liken writes
// liken.sh-<claimUID>.json in the same directory. Each driver reads back
// only the files that start with its own prefix, so neither driver reads
// or overwrites the other's specs.
//
// The kubelet prepares a claim once and reuses the result for every
// later pod that names it, but the node that a device maps to can change
// after that. sinkNodeName builds an ALSA node's name from the card and
// PCM numbers, so a card that registers under a different number has a
// new node name. So the reconcile pass rewrites the spec of every
// prepared claim, from the same graph read that publishes the
// ResourceSlice.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// cdiWrites serializes the writes to the spec files.
// NodePrepareResources and the reconcile pass both write a spec, through
// the same temporary path.
var cdiWrites sync.Mutex

// cdiDir is the directory where the container runtime reads CDI specs.
var cdiDir = "/var/run/cdi"

// cdiKind is the kind of this driver's CDI devices. A CDI device ID is
// "<kind>=<name>".
const cdiKind = DriverName + "/output"

// cdiPrefix keeps this driver's spec files apart from liken's in the
// shared directory.
const cdiPrefix = DriverName + "-"

const (
	remoteVariable = "PIPEWIRE_REMOTE"
	nodeVariable   = "PIPEWIRE_NODE"
)

// cdiSpec is the part of the CDI spec schema that this operator writes.
// It has no field for device nodes because the specs grant none.
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

// endpointEdits returns the edits for one allocated device: a mount of
// the directory that holds PipeWire's socket, PIPEWIRE_REMOTE with the
// socket's path, and PIPEWIRE_NODE with the node name. A Bluetooth
// speaker, an HDMI output, and a microphone get the same edits, because a
// capture stream follows target.object as a playback stream does.
//
// The mount is read-only, because connecting to a Unix socket needs
// write permission on the socket and not on the file system that
// contains it.
func endpointEdits(node string) cdiEdits {
	return cdiEdits{
		Env: []string{
			remoteVariable + "=" + socketPath,
			nodeVariable + "=" + node,
		},
		Mounts: []cdiMount{{
			HostPath:      runtimeDir,
			ContainerPath: runtimeDir,
			Options:       []string{"ro", "rbind", "rprivate", "nosuid", "nodev"},
		}},
	}
}

func writeCDISpec(claimUID string, devices []cdiDevice) error {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()
	return writeSpecFile(claimUID, devices)
}

// writeSpecFile writes a claim's spec while the caller holds cdiWrites.
// It writes a temporary file and renames it over the spec. The runtime
// can list the directory at any moment, and a half-written spec would
// fail every container creation that read it.
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

// removeCDISpec deletes a claim's spec file. A missing file is not an
// error, because unprepare must be idempotent: the kubelet repeats it
// when it has no record that the call succeeded.
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

// claimUIDFromSpecName returns the claim UID in a spec file name. It
// rejects a name without this driver's prefix, which belongs to liken or
// another writer, and a name that ends in .json.tmp, which is a spec
// that is still being written. The refresh leaves both alone.
func claimUIDFromSpecName(name string) (string, bool) {
	uid, ok := strings.CutPrefix(name, cdiPrefix)
	if !ok {
		return "", false
	}
	uid, ok = strings.CutSuffix(uid, ".json")
	if !ok {
		return "", false
	}
	return uid, true
}

// refreshCDISpecs rewrites the spec of each prepared claim with the
// current node name of its device. It resolves each device as prepare
// does, through endpointNode, so a refreshed spec and a prepared spec
// agree.
//
// A refresh cannot repair a container that already runs, because the
// runtime applies the edits only when it creates the container. It keeps
// a stale spec from reaching later pods. Without it, when a sink comes
// back under a different node name, the next pod gets a PIPEWIRE_NODE
// that names nothing, and its streams play to PipeWire's default sink.
func refreshCDISpecs(endpoints *endpointInventory, graph pwGraph) {
	entries, err := os.ReadDir(cdiDir)
	if err != nil {
		// No directory means that no claim has been prepared on this
		// boot.
		return
	}
	for _, entry := range entries {
		claimUID, ok := claimUIDFromSpecName(entry.Name())
		if !ok {
			continue
		}
		if err := refreshCDISpec(claimUID, endpoints, graph); err != nil {
			fmt.Fprintf(os.Stderr, "refreshing the spec for claim %s: %v\n", claimUID, err)
		}
	}
}

// refreshCDISpec writes nothing when no device's environment changed.
//
// A device whose node is gone keeps its old node name, because an empty
// PIPEWIRE_NODE would start the next pod against PipeWire's default sink
// with no error. The taints on the device keep that pod from scheduling
// until the device can play again.
func refreshCDISpec(claimUID string, endpoints *endpointInventory, graph pwGraph) error {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()

	raw, err := os.ReadFile(cdiSpecPath(claimUID))
	if os.IsNotExist(err) {
		// Unprepare removed the claim between the directory listing and
		// this read.
		return nil
	}
	if err != nil {
		return err
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("parsing the spec: %w", err)
	}
	changed := false
	for i, device := range spec.Devices {
		// prepare names each CDI device <claimUID>-<allocated device>,
		// so the refresh gets the device name from the file and makes
		// no call to the API server.
		allocated, ok := strings.CutPrefix(device.Name, claimUID+"-")
		if !ok {
			continue
		}
		// A device that endpointNode cannot resolve keeps the node
		// name in the file.
		node, err := endpointNode(endpoints, allocated, graph)
		if err != nil {
			continue
		}
		edits := endpointEdits(node)
		if slices.Equal(edits.Env, device.ContainerEdits.Env) {
			continue
		}
		spec.Devices[i].ContainerEdits = edits
		changed = true
	}
	if !changed {
		return nil
	}
	return writeSpecFile(claimUID, spec.Devices)
}
