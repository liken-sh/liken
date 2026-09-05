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
type record struct {
	VolumeID    string            `json:"volumeId"`
	Attributes  map[string]string `json:"attributes"`
	Target      string            `json:"targetPath"`
	Staging     string            `json:"stagingPath,omitempty"`
	Ephemeral   bool              `json:"ephemeral"`
	Credentials bool              `json:"credentials"`
}

// record writes the volume's own record. A record the driver cannot
// write is logged and nothing more, because a mount is worth more than
// the record of it.
func (n *node) record(ctx context.Context, published *volume, attributes map[string]string) {
	written := &record{
		VolumeID:    published.id,
		Attributes:  attributes,
		Target:      published.target,
		Staging:     published.staging,
		Ephemeral:   !published.writeable,
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
		if !n.mounted(held.Target) {
			n.drop(ctx, held, directory)
			continue
		}
		n.resumeOne(ctx, held, directory)
	}
}

// drop removes a read-only volume whose mount is gone, because it
// leaves nothing worth keeping. A writeable one keeps its tree until
// plan 06's sweep or a person takes it.
func (n *node) drop(ctx context.Context, held *record, directory string) {
	if !held.Ephemeral {
		return
	}
	if err := os.RemoveAll(directory); err != nil {
		n.logger.WarnContext(ctx, "the volume's directory stayed",
			"volume", held.VolumeID, "error", err)
	}
}

// resumeOne rebuilds one volume and starts the loops it had. It holds
// no credential: the Secret came with a call the kubelet makes again
// only when the pod restarts, so a fetch that needs one fails and the
// condition says so.
func (n *node) resumeOne(ctx context.Context, held *record, directory string) {
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
		writeable:  !held.Ephemeral,
		pod:        parsed.pod,
	}
	if held.Credentials {
		resumed.reportTrouble("the driver restarted and holds no credential for this volume")
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	n.volumes[resumed.id] = resumed
	if held.Ephemeral {
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

// mountedNow asks the kernel whether the path is still a mount. Only
// the mount table says what is still there.
func mountedNow(path string) bool {
	table, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer table.Close()
	return mountedIn(table, path)
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
