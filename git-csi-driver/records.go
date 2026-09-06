package main

// records.go is what a restarted driver reads to find the volumes it
// published. The mounts belong to the kernel and survive the driver;
// its own set does not.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// recordFile is the file each volume's directory carries beside its
// tree.
const recordFile = "volume.json"

// record is one published volume as the store holds it. The credentials
// are not in it. A Secret reaches the driver through the kubelet, and
// the node's disk is not where it belongs.
//
// An inline volume and a writeable volume carry the one target they can
// have in targetPath. A read-only claim carries every target it is bound
// at in targetPaths.
type record struct {
	VolumeID    string            `json:"volumeId"`
	Attributes  map[string]string `json:"attributes"`
	Target      string            `json:"targetPath"`
	Targets     []string          `json:"targetPaths,omitempty"`
	Staging     string            `json:"stagingPath,omitempty"`
	Ephemeral   bool              `json:"ephemeral"`
	Kind        string            `json:"kind,omitempty"`
	Credentials bool              `json:"credentials"`
}

// The names a record writes for the three kinds.
const (
	inlineKind    = "inline"
	readOnlyKind  = "readOnly"
	writeableKind = "writeable"
)

// kindNames is the name each kind writes and reads.
var kindNames = map[volumeKind]string{
	inlineVolume:    inlineKind,
	readOnlyClaim:   readOnlyKind,
	writeableVolume: writeableKind,
}

// kind is what the record says this volume is.
//
// A record with no kind is read by its ephemeral flag: an inline volume
// when it is set, and a writeable volume when it is not. Those are the
// two kinds a driver that writes no kind holds.
func (r *record) kind() volumeKind {
	switch r.Kind {
	case inlineKind:
		return inlineVolume
	case readOnlyKind:
		return readOnlyClaim
	case writeableKind:
		return writeableVolume
	}
	if r.Ephemeral {
		return inlineVolume
	}
	return writeableVolume
}

// targetPaths is every target the record names.
func (r *record) targetPaths() []string {
	if r.kind() == readOnlyClaim {
		return r.Targets
	}
	return []string{r.Target}
}

// record writes the volume's own record. A record the driver cannot
// write is logged and nothing more, because a mount is worth more than
// the record of it.
func (n *node) record(ctx context.Context, published *volume) {
	written := &record{
		VolumeID:    published.id,
		Attributes:  published.context,
		Target:      published.target,
		Targets:     published.boundTargets(),
		Staging:     published.staging,
		Ephemeral:   published.kind == inlineVolume,
		Kind:        kindNames[published.kind],
		Credentials: published.credentials != nil,
	}
	content, err := json.Marshal(written)
	if err == nil {
		err = os.WriteFile(filepath.Join(published.directory, recordFile), content, 0o600)
	}
	if err != nil {
		n.logger.WarnContext(ctx, "the volume's record was not written",
			"volume", published.id, "error", err)
	}
}

// forget removes the record, so a driver that starts after this
// unpublish does not resume a mount the kubelet took away.
func (n *node) forget(published *volume) {
	if err := os.Remove(filepath.Join(published.directory, recordFile)); err != nil {
		n.logger.Warn("the volume's record was not removed", "volume", published.id, "error", err)
	}
}

// resume rebuilds the driver's set from the store. A target that is
// still a mount is a volume a pod still reads, so its loops start
// again. A target that is not is a read-only volume the store may drop,
// or a work tree that may hold work nobody has pushed.
//
// The driver mounts nothing at a staging path, so the targets are the
// whole evidence that a volume is still held. A record with no target
// that the kernel still holds is dropped.
func (n *node) resume(ctx context.Context) {
	volumes := filepath.Join(n.store.root, "volumes")
	entries, err := os.ReadDir(volumes)
	if err != nil {
		return
	}
	for _, entry := range entries {
		directory := filepath.Join(volumes, entry.Name())
		held, err := readRecord(filepath.Join(directory, recordFile))
		if err != nil {
			continue
		}
		mounted := n.mountedTargets(held)
		if len(mounted) == 0 {
			n.drop(ctx, held, directory)
			continue
		}
		n.resumeOne(ctx, held, directory, mounted)
	}
}

// mountedTargets is the targets the record names that the kernel still
// holds.
func (n *node) mountedTargets(held *record) []string {
	mounted := []string{}
	for _, target := range held.targetPaths() {
		if n.mounted(target) {
			mounted = append(mounted, target)
		}
	}
	return mounted
}

// drop removes a read-only volume whose mounts are gone, because it
// leaves nothing worth keeping. A writeable one keeps its tree until
// plan 06's sweep or a person takes it.
func (n *node) drop(ctx context.Context, held *record, directory string) {
	if held.kind() == writeableVolume {
		return
	}
	if err := os.RemoveAll(directory); err != nil {
		n.logger.WarnContext(ctx, "the volume's directory stayed",
			"volume", held.VolumeID, "error", err)
	}
}

// resumeOne rebuilds one volume and starts the loops it had. It
// holds no credential: the Secret came with a call the kubelet makes
// again only when the pod restarts, so a fetch that needs one fails and
// the volume's report says so.
func (n *node) resumeOne(ctx context.Context, held *record, directory string, mounted []string) {
	parsed, err := parseVolumeContext(held.Attributes)
	if err != nil {
		n.logger.WarnContext(ctx, "the volume's record was not read",
			"volume", held.VolumeID, "error", err)
		return
	}
	resumed := &volume{
		id:         held.VolumeID,
		attributes: parsed,
		directory:  directory,
		tree:       filepath.Join(directory, "tree"),
		target:     held.Target,
		staging:    held.Staging,
		kind:       held.kind(),
		context:    held.Attributes,
		targets:    map[string]podReference{},
		pod:        parsed.pod,
	}
	if held.Credentials {
		resumed.reportTrouble("the driver restarted and holds no credential for this volume")
	}
	if resumed.kind == readOnlyClaim {
		// The record names no pod for a target, so the resumed volume
		// reports to the claim alone until each pod publishes again.
		for _, target := range mounted {
			resumed.bind(target, podReference{})
		}
		n.findClaim(ctx, resumed)
		// The record is written again, so the targets a pod gave up
		// while the driver was down leave it.
		n.record(ctx, resumed)
	}
	n.noteHealth(ctx, resumed)

	n.mu.Lock()
	defer n.mu.Unlock()
	n.volumes[resumed.id] = resumed
	if resumed.kind == readOnlyClaim {
		n.staged[resumed.id] = resumed
		n.follow(resumed)
		return
	}
	if resumed.kind == inlineVolume {
		n.follow(resumed)
		return
	}
	resumed.work = n.store.workTree(n.store.repository(parsed.url), resumed.id)
	if commit, err := resumed.work.head(ctx); err == nil {
		resumed.commit = commit
	}
	n.staged[resumed.id] = resumed
	n.arm(resumed)
	n.watch(resumed)
}

// readRecord reads one volume's record.
func readRecord(path string) (*record, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	held := &record{}
	if err := json.Unmarshal(content, held); err != nil {
		return nil, err
	}
	return held, nil
}

// mountTable is where the kernel publishes what is mounted.
const mountTable = "/proc/self/mountinfo"

// mountedNow asks the kernel whether the path is still a mount. Only
// the mount table says what is still there.
func mountedNow(table, path string) bool {
	published, err := os.Open(table)
	if err != nil {
		return false
	}
	defer published.Close()
	return mountedIn(published, path)
}

// mountedIn reads mountinfo. The fifth field of every line is the mount
// point, with a space, a tab, a newline, and a backslash written as
// octal escapes.
func mountedIn(table io.Reader, path string) bool {
	lines := bufio.NewScanner(table)
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		if len(fields) > 4 && mountEscapes.Replace(fields[4]) == path {
			return true
		}
	}
	return false
}

var mountEscapes = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
