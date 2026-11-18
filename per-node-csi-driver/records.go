package main

// records.go keeps a mount record for each target path that a copy is
// published at, in memory and in files under holds/. The sweep reads
// the records so it does not delete a copy that a pod still mounts, and
// loadRecords reads the files after a restart.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// mountRecord records that a pod has a copy mounted at a target path.
type mountRecord struct {
	PodUID       string `json:"podUid"`
	PodName      string `json:"podName,omitempty"`
	PodNamespace string `json:"podNamespace,omitempty"`
	Target       string `json:"targetPath"`
}

// recordsOf returns the handle's mount records, ordered by target path.
func (n *node) recordsOf(handle string) []mountRecord {
	n.mu.Lock()
	defer n.mu.Unlock()
	records := make([]mountRecord, 0, len(n.records[handle]))
	for _, record := range n.records[handle] {
		records = append(records, record)
	}
	slices.SortFunc(records, func(one, other mountRecord) int {
		return strings.Compare(one.Target, other.Target)
	})
	return records
}

// published reports whether the handle is already published at the target.
func (n *node) published(handle, target string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, found := n.records[handle][target]
	return found
}

// addRecord adds the record in memory only.
func (n *node) addRecord(handle string, record mountRecord) {
	n.mu.Lock()
	if n.records[handle] == nil {
		n.records[handle] = map[string]mountRecord{}
	}
	n.records[handle][record.Target] = record
	n.mu.Unlock()
}

// saveRecord adds the record in memory and writes it to a file. When the
// file write fails, saveRecord only logs it and the publish still
// succeeds, because the mount is in place. The driver then loses the
// record at its next restart.
func (n *node) saveRecord(ctx context.Context, handle string, record mountRecord) {
	n.addRecord(handle, record)
	n.reportVolumes()

	path := n.store.recordPath(handle, record.Target)
	content, err := json.Marshal(record)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o755)
	}
	if err == nil {
		err = os.WriteFile(path, content, 0o600)
	}
	if err != nil {
		n.logger.WarnContext(ctx, "The mount record was not written to disk, so it exists only in memory",
			"volume", handle, "target", record.Target, "error", err)
	}
}

// deleteRecord deletes the record of the target from memory and from
// disk. It returns true when that was the handle's last record.
func (n *node) deleteRecord(ctx context.Context, handle, target string) bool {
	n.mu.Lock()
	delete(n.records[handle], target)
	last := len(n.records[handle]) == 0
	if last {
		delete(n.records, handle)
	}
	n.mu.Unlock()
	n.reportVolumes()

	if err := os.Remove(n.store.recordPath(handle, target)); err != nil && !os.IsNotExist(err) {
		n.logger.WarnContext(ctx, "The mount record file was not deleted",
			"volume", handle, "target", target, "error", err)
	}
	if last {
		n.removeRecordsDirectory(ctx, handle)
	}
	return last
}

// removeRecordsDirectory logs a failure only at debug level, because the
// directory still has a file only when that record file could not be
// deleted, and the driver already logged that.
func (n *node) removeRecordsDirectory(ctx context.Context, handle string) {
	err := os.Remove(n.store.recordsPath(handle))
	if err != nil && !os.IsNotExist(err) {
		n.logger.DebugContext(ctx, "The mount records directory of the volume was not deleted", "volume", handle, "error", err)
	}
}

// publishedHandles returns the handles that have at least one mount
// record.
func (n *node) publishedHandles() map[string]bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	published := make(map[string]bool, len(n.records))
	for handle := range n.records {
		published[handle] = true
	}
	return published
}

// reportVolumes sets per_node_csi_volumes to the number of handles with
// a mount record, however many pods mount each one.
func (n *node) reportVolumes() {
	n.mu.Lock()
	count := len(n.records)
	n.mu.Unlock()
	n.metrics.setVolumes(count)
}

// loadRecords reads the record files back into memory after a restart.
// A bind mount outlives the driver's process, so the mount table shows
// which records still have a mount. When a record's target is no longer
// a mount, the kubelet unpublished it while the driver was down, and
// loadRecords deletes the record file.
func (n *node) loadRecords(ctx context.Context) {
	entries, err := os.ReadDir(filepath.Join(n.store.root, recordsDirectory))
	if err != nil {
		return
	}
	for _, entry := range entries {
		handle := entry.Name()
		if entry.IsDir() {
			n.loadRecordsOf(ctx, handle)
			continue
		}
		n.loadSingleFileRecord(ctx, handle)
	}
	n.reportVolumes()
}

func (n *node) loadRecordsOf(ctx context.Context, handle string) {
	dir := n.store.recordsPath(handle)
	files, err := os.ReadDir(dir)
	if err != nil {
		n.logger.WarnContext(ctx, "The mount records directory of the volume was not read", "volume", handle, "error", err)
		return
	}
	for _, file := range files {
		path := filepath.Join(dir, file.Name())
		record, err := readRecord(path)
		if err != nil {
			n.logger.WarnContext(ctx, "The mount record file was not read, so the driver deleted it",
				"volume", handle, "file", path, "error", err)
			n.removeRecordFile(ctx, handle, path)
			continue
		}
		if !n.mounted(record.Target) {
			n.logger.InfoContext(ctx, "Deleted the mount record of a target that is no longer mounted",
				"volume", handle, "target", record.Target)
			n.removeRecordFile(ctx, handle, path)
			continue
		}
		n.addRecord(handle, record)
	}
	if len(n.recordsOf(handle)) == 0 {
		n.removeRecordsDirectory(ctx, handle)
	}
}

// loadSingleFileRecord reads a record stored as one file at
// holds/<handle>, the layout that earlier versions of the driver wrote.
// When the record's target is still a mount, it writes the record again
// under holds/<handle>/, so a pod that runs across the upgrade keeps its
// record. It deletes the file first, because the new directory has the
// same path.
func (n *node) loadSingleFileRecord(ctx context.Context, handle string) {
	path := n.store.recordsPath(handle)
	record, err := readRecord(path)
	if err != nil {
		n.logger.WarnContext(ctx, "The mount record file was not read, so the driver deleted it",
			"volume", handle, "file", path, "error", err)
	}
	n.removeRecordFile(ctx, handle, path)
	if err != nil {
		return
	}
	if !n.mounted(record.Target) {
		n.logger.InfoContext(ctx, "Deleted the mount record of a target that is no longer mounted",
			"volume", handle, "target", record.Target)
		return
	}
	n.saveRecord(ctx, handle, record)
}

func (n *node) removeRecordFile(ctx context.Context, handle, path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		n.logger.WarnContext(ctx, "The mount record file was not deleted",
			"volume", handle, "file", path, "error", err)
	}
}

func readRecord(path string) (mountRecord, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return mountRecord{}, err
	}
	record := mountRecord{}
	if err := json.Unmarshal(content, &record); err != nil {
		return mountRecord{}, err
	}
	return record, nil
}

// mountTable lists the mounts of this process's mount namespace.
const mountTable = "/proc/self/mountinfo"

// mountedNow reports whether the path is a mount point in the table
// file. It returns false when the table cannot be opened.
func mountedNow(table, path string) bool {
	published, err := os.Open(table)
	if err != nil {
		return false
	}
	defer published.Close()
	return mountedIn(published, path)
}

// mountedIn reports whether the path is a mount point in a mountinfo
// table. The fifth field of each line is the mount point, and the
// kernel writes a space, tab, newline, and backslash in it as octal
// escapes.
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
