package main

// records.go writes a record file for each published volume and
// rebuilds the driver's volumes from those files at startup. A driver
// restart clears the in-memory set of volumes, and the kernel keeps the
// mounts.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const recordFile = "volume.json"

// record is the JSON written for one published volume. Credentials
// records only whether the volume had a credential, because the driver
// keeps the Secret's data in memory and never on the node's disk.
//
// An inline volume and a writeable volume have one target, in Target.
// A read-only claim can be mounted in several pods, so Targets lists
// each of its targets.
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

// The values of record.Kind.
const (
	inlineKind    = "inline"
	readOnlyKind  = "readOnly"
	writeableKind = "writeable"
)

var kindNames = map[volumeKind]string{
	inlineVolume:    inlineKind,
	readOnlyClaim:   readOnlyKind,
	writeableVolume: writeableKind,
}

// kind reads Ephemeral when Kind is empty. record writes Kind for every
// volume, so an empty Kind comes from an earlier driver version, and
// such a record is an inline volume or a writeable volume.
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

func (r *record) targetPaths() []string {
	if r.kind() == readOnlyClaim {
		return r.Targets
	}
	return []string{r.Target}
}

// record writes the volume's record file. A failed write is only
// logged, so a volume that is mounted and working does not fail because
// its record is missing.
func (n *node) record(ctx context.Context, published *volume) {
	written := &record{
		VolumeID:    published.id,
		Attributes:  published.context,
		Target:      published.target,
		Targets:     published.boundTargets(),
		Staging:     published.staging,
		Ephemeral:   published.kind == inlineVolume,
		Kind:        kindNames[published.kind],
		Credentials: published.hasOrAwaitsCredential(),
	}
	content, err := json.Marshal(written)
	if err == nil {
		err = writeFileAtomically(filepath.Join(published.directory, recordFile), content)
	}
	if err != nil {
		n.logger.WarnContext(ctx, "The record file of the volume was not written",
			"volume", published.id, "error", err)
	}
}

// forget deletes the record file, so a driver that restarts after an
// unpublish does not resume a volume that the kubelet has removed.
func (n *node) forget(published *volume) {
	if err := os.Remove(filepath.Join(published.directory, recordFile)); err != nil {
		n.logger.Warn("The record file of the volume was not removed", "volume", published.id, "error", err)
	}
}

// resume rebuilds the driver's volumes from the record files. A volume
// is still in use when one of its targets is mounted. The driver mounts
// nothing at the staging path, so resume checks only the targets. It
// passes a volume with no mounted target to drop.
func (n *node) resume(ctx context.Context) {
	volumes := filepath.Join(n.store.root, "volumes")
	ids, err := n.store.volumeIDs()
	if err != nil {
		// The pods of the volumes keep their mounts, but the driver does
		// not follow their refs or commit their changes until it restarts
		// and reads the directory.
		n.logger.WarnContext(ctx, "The volumes in the store were not resumed", "error", err)
		return
	}
	for _, id := range ids {
		directory := filepath.Join(volumes, id)
		rec, err := readRecord(filepath.Join(directory, recordFile))
		if err != nil {
			// A writeable volume that was staged and never published has
			// no record.
			if !absent(err) {
				n.logger.WarnContext(ctx, "The volume was not resumed, because its record was not read",
					"volume", id, "error", err)
			}
			continue
		}
		mounted := n.mountedTargets(rec)
		if len(mounted) == 0 {
			n.drop(ctx, rec, directory)
			continue
		}
		n.resumeOne(ctx, rec, directory, mounted)
	}
}

func (n *node) mountedTargets(rec *record) []string {
	mounted := []string{}
	for _, target := range rec.targetPaths() {
		if n.mounted(target) {
			mounted = append(mounted, target)
		}
	}
	return mounted
}

// drop deletes the directory of a read-only volume that has no mounts,
// because its tree is a copy of the remote. It keeps a writeable
// volume's directory, which can hold commits that were never pushed.
// reclaim.go deletes that directory when its PersistentVolume is
// deleted with the Delete reclaim policy.
func (n *node) drop(ctx context.Context, rec *record, directory string) {
	if rec.kind() == writeableVolume {
		return
	}
	if err := os.RemoveAll(directory); err != nil {
		n.logger.WarnContext(ctx, "The volume directory was not removed",
			"volume", rec.VolumeID, "error", err)
	}
}

// resumeOne rebuilds one volume from its record and starts its fetch
// loop or its watch. A volume that had a credential has none now,
// because the driver never writes it to disk. Such a volume starts no
// fetch here, and its health report says that it waits for a
// credential. The kubelet calls NodePublishVolume again for each mounted
// volume with the Secret, and republish.go then starts the fetch or
// push.
func (n *node) resumeOne(ctx context.Context, rec *record, directory string, mounted []string) {
	parsed, err := parseVolumeContext(rec.Attributes)
	if err != nil {
		n.logger.WarnContext(ctx, "The attributes in the record file of the volume were not parsed",
			"volume", rec.VolumeID, "error", err)
		return
	}
	resumed := &volume{
		id:         rec.VolumeID,
		attributes: parsed,
		directory:  directory,
		tree:       filepath.Join(directory, "tree"),
		target:     rec.Target,
		staging:    rec.Staging,
		kind:       rec.kind(),
		context:    rec.Attributes,
		targets:    map[string]podReference{},
		pod:        parsed.pod,
		commit:     placedCommit(directory),
	}
	if rec.Credentials {
		resumed.markCredentialLost()
	}
	if resumed.kind == readOnlyClaim {
		// The record has no pod for each target, so the volume posts its
		// Events only to the claim until each pod publishes again.
		for _, target := range mounted {
			resumed.bind(target, podReference{})
		}
		n.findClaim(ctx, resumed)
		// Writing the record again removes the targets that were
		// unmounted while the driver was down.
		n.record(ctx, resumed)
	}
	n.recordHealth(ctx, resumed)
	// A demand stamped before the restart needs no fetch of its own.
	// resumePull, or the publish that returns the credential, fetches
	// each read-only volume that follows its ref, and a writeable volume
	// ignores demands.
	resumed.satisfyDemandsBefore(time.Now())

	n.mu.Lock()
	defer n.mu.Unlock()
	n.volumes[resumed.id] = resumed
	if resumed.kind == readOnlyClaim {
		n.staged[resumed.id] = resumed
		n.joinFetchLoop(resumed)
		if !rec.Credentials {
			n.resumePull(resumed)
		}
		return
	}
	if resumed.kind == inlineVolume {
		n.joinFetchLoop(resumed)
		if !rec.Credentials {
			n.resumePull(resumed)
		}
		return
	}
	resumed.work = n.store.workTree(n.store.repository(parsed.url), resumed.id, parsed.paths)
	if commit, err := resumed.work.head(ctx); err == nil {
		resumed.commit = commit
	}
	n.staged[resumed.id] = resumed
	n.arm(resumed)
	n.watch(resumed)
}

func readRecord(path string) (*record, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rec := &record{}
	if err := json.Unmarshal(content, rec); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return rec, nil
}

// writeFileAtomically writes content to a temporary file beside path,
// syncs it, and renames it over path, with the mode 0600 of
// os.CreateTemp. A driver that stops partway leaves the earlier file
// whole, where a write in place can leave a truncated record that the
// sweep cannot read.
func writeFileAtomically(path string, content []byte) error {
	next, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".next-")
	if err != nil {
		return err
	}
	defer os.Remove(next.Name())
	_, written := next.Write(content)
	err = errors.Join(written, next.Sync(), next.Close())
	if err == nil {
		err = os.Rename(next.Name(), path)
	}
	return err
}

// mountTable is the kernel's list of the mounts that this process
// sees.
const mountTable = "/proc/self/mountinfo"

// mountedNow reports whether path is a mount point in the mount table,
// because a path can exist on disk after its mount is gone. A table that
// cannot be read reports no mount.
func mountedNow(table, path string) bool {
	published, err := os.Open(table)
	if err != nil {
		return false
	}
	defer published.Close()
	return mountedIn(published, path)
}

// mountedIn reads the mount point from the fifth field of each line. The
// kernel writes a space, tab, newline, or backslash in that field as an
// octal escape.
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
