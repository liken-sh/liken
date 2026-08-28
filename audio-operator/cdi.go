package main

// Writing CDI specs: how a prepared claim becomes a socket and two
// environment variables in a consumer's container.
//
// The Container Device Interface connects two things: which device to
// use, and what appears inside the container. A JSON file in a
// well-known directory names devices and the edits that grant one to
// a container. Here those edits are a mount and two environment
// variables, and no device node at all. A consumer does not open a
// PCM device. It connects to PipeWire, which holds every PCM on the
// card, and names the sink its streams must reach.
//
// The two variables are what PipeWire's own client library reads. A
// PIPEWIRE_REMOTE that starts with a slash is used as an absolute
// socket path, and the runtime directory is not consulted.
// PIPEWIRE_NODE sets target.object on every stream the client
// creates, which takes a node name.
//
// The file name starts with this driver's own prefix,
// audio.liken.sh-<claimUID>.json. liken writes
// liken.sh-<claimUID>.json in the same directory and reads back only
// the files whose names start with its own prefix, so the two drivers
// never read or overwrite each other's specs.
//
// A file also has to stay correct for the whole boot. The kubelet
// prepares a claim once and reuses the answer for every later pod
// that names it, and a sink's name can change after that: WirePlumber
// builds the name from the card's profile, so a profile change
// renames the sink. The reconcile pass rewrites every prepared
// claim's file from the same graph read that publishes the slice.

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
const cdiKind = DriverName + "/output"

// cdiPrefix is what separates this driver's spec files from liken's
// in the shared directory.
const cdiPrefix = DriverName + "-"

// The two variables a consumer receives.
const (
	remoteVariable = "PIPEWIRE_REMOTE"
	nodeVariable   = "PIPEWIRE_NODE"
)

// cdiSpec holds the part of the CDI spec schema that this operator
// writes. The delivery includes no device node, so the struct omits
// the field for them.
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

// endpointEdits builds what one allocated device grants a container:
// the directory that holds PipeWire's socket, the absolute path to
// that socket, and the name of the node the claim allocated. A
// Bluetooth speaker, an HDMI output, and a microphone deliver the
// identical three things, because PIPEWIRE_NODE sets target.object
// on every stream a client creates, and a capture stream honors it
// the way a playback stream does.
//
// The mount is read-only. Connecting to a Unix socket needs write
// permission on the socket itself and not on the file system that
// holds it, so a read-only mount still connects, and a consumer has
// no reason to create anything in this directory.
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
	uid, ok = strings.CutSuffix(uid, ".json")
	if !ok {
		return "", false
	}
	return uid, true
}

// refreshCDISpecs rewrites each prepared claim's spec with the node
// its endpoint has now. It resolves each device the same way prepare
// does, from one inventory and one graph read, so a spec written by
// a refresh and a spec written by a prepare always agree.
//
// This cannot repair a container that already runs. The runtime
// applies the edits when it creates the container, and a variable
// that changes under a running container stays wrong until the pod
// restarts. What it prevents is a stale file that every later pod
// would receive. An output whose sink came back under a different
// name would give the next pod a PIPEWIRE_NODE that names nothing,
// and its streams would play into whichever sink PipeWire chose by
// default.
func refreshCDISpecs(endpoints *endpointInventory, graph pwGraph) {
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
		if err := refreshCDISpec(claimUID, endpoints, graph); err != nil {
			fmt.Fprintf(os.Stderr, "refreshing the spec for claim %s: %v\n", claimUID, err)
		}
	}
}

// refreshCDISpec rewrites one claim's spec, and writes nothing when
// every device still delivers what the file says.
//
// An output whose sink is gone keeps the name it had. An empty
// PIPEWIRE_NODE would start the next pod against PipeWire's default
// sink with no error. The taints on the device hold that pod back
// until the output can play again.
func refreshCDISpec(claimUID string, endpoints *endpointInventory, graph pwGraph) error {
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
		// A device this pass cannot resolve keeps the name the file
		// already holds, which is the same rule a sink that is gone
		// gets.
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
