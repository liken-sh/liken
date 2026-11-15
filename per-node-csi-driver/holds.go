package main

// holds.go records which pods publish which copy on this node, so a
// copy that a pod still mounts survives a restart of the plugin and the
// sweep. Any number of pods on one node can hold one copy. The access
// mode of the volume decides how many pods mount it, and the scheduler
// and the kubelet enforce the mode.

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

// hold is one pod's mount of one copy at one target. It is written
// beside the copies, so a restarted plugin reads it back from the disk.
// Memory does not survive the restart.
type hold struct {
	PodUID       string `json:"podUid"`
	PodName      string `json:"podName,omitempty"`
	PodNamespace string `json:"podNamespace,omitempty"`
	Target       string `json:"targetPath"`
}

// holdsOf returns every hold on the handle on this node, ordered by
// target, and none when no pod holds it.
func (n *node) holdsOf(handle string) []hold {
	n.mu.Lock()
	defer n.mu.Unlock()
	held := make([]hold, 0, len(n.holds[handle]))
	for _, taken := range n.holds[handle] {
		held = append(held, taken)
	}
	slices.SortFunc(held, func(one, other hold) int {
		return strings.Compare(one.Target, other.Target)
	})
	return held
}

// holding reports whether a pod already published the handle at the
// target.
func (n *node) holding(handle, target string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, held := n.holds[handle][target]
	return held
}

// remember records the hold in memory alone.
func (n *node) remember(handle string, taken hold) {
	n.mu.Lock()
	if n.holds[handle] == nil {
		n.holds[handle] = map[string]hold{}
	}
	n.holds[handle][taken.Target] = taken
	n.mu.Unlock()
}

// keepHold records the hold in memory and on the disk. A hold the driver
// cannot write is logged and nothing more, because the pod has its
// mount and a mount is worth more than the record of it.
func (n *node) keepHold(ctx context.Context, handle string, taken hold) {
	n.remember(handle, taken)
	n.reportVolumes()

	path := n.store.holdPath(handle, taken.Target)
	content, err := json.Marshal(taken)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o755)
	}
	if err == nil {
		err = os.WriteFile(path, content, 0o600)
	}
	if err != nil {
		n.logger.WarnContext(ctx, "the hold was not written",
			"volume", handle, "target", taken.Target, "error", err)
	}
}

// dropHold gives up the hold at the target, in memory and on the disk.
// It reports whether that was the handle's last hold on this node. The
// handle's directory under holds/ goes with its last hold.
func (n *node) dropHold(ctx context.Context, handle, target string) bool {
	n.mu.Lock()
	delete(n.holds[handle], target)
	last := len(n.holds[handle]) == 0
	if last {
		delete(n.holds, handle)
	}
	n.mu.Unlock()
	n.reportVolumes()

	if err := os.Remove(n.store.holdPath(handle, target)); err != nil && !os.IsNotExist(err) {
		n.logger.WarnContext(ctx, "the hold was not removed",
			"volume", handle, "target", target, "error", err)
	}
	if last {
		n.removeHoldsDirectory(ctx, handle)
	}
	return last
}

// removeHoldsDirectory removes the handle's empty directory under
// holds/. A directory that still holds a file is left, because the file
// is a hold the driver could not remove, and the log already says so.
func (n *node) removeHoldsDirectory(ctx context.Context, handle string) {
	err := os.Remove(n.store.holdsPath(handle))
	if err != nil && !os.IsNotExist(err) {
		n.logger.DebugContext(ctx, "the holds directory stayed", "volume", handle, "error", err)
	}
}

// heldHandles returns every handle a pod holds on this node. The sweep
// never removes the copy of a held handle.
func (n *node) heldHandles() map[string]bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	held := make(map[string]bool, len(n.holds))
	for handle := range n.holds {
		held[handle] = true
	}
	return held
}

// reportVolumes sets the volumes gauge to how many handles this node
// holds right now, each counted once however many pods hold it. It runs
// after every change to n.holds, the one map that says what is mounted.
func (n *node) reportVolumes() {
	n.mu.Lock()
	count := len(n.holds)
	n.mu.Unlock()
	n.readings.setVolumes(count)
}

// resume rebuilds the holds from the disk after a restart. The kernel
// keeps a bind mount when the plugin stops, so the mount table says
// which holds are still real. A hold whose target is not a mount was
// unpublished while the plugin was down, and resume drops it.
func (n *node) resume(ctx context.Context) {
	entries, err := os.ReadDir(filepath.Join(n.store.root, holdsDirectory))
	if err != nil {
		return
	}
	for _, entry := range entries {
		handle := entry.Name()
		if entry.IsDir() {
			n.resumeHandle(ctx, handle)
			continue
		}
		n.resumeSingleFile(ctx, handle)
	}
	n.reportVolumes()
}

// resumeHandle reads the holds in one handle's directory.
func (n *node) resumeHandle(ctx context.Context, handle string) {
	dir := n.store.holdsPath(handle)
	files, err := os.ReadDir(dir)
	if err != nil {
		n.logger.WarnContext(ctx, "the holds were not read", "volume", handle, "error", err)
		return
	}
	for _, file := range files {
		path := filepath.Join(dir, file.Name())
		taken, err := readHold(path)
		if err != nil {
			n.logger.WarnContext(ctx, "the hold was not read",
				"volume", handle, "file", path, "error", err)
			n.removeHoldFile(ctx, handle, path)
			continue
		}
		if !n.mounted(taken.Target) {
			n.logger.InfoContext(ctx, "the hold was given up while the plugin was down",
				"volume", handle, "target", taken.Target)
			n.removeHoldFile(ctx, handle, path)
			continue
		}
		n.remember(handle, taken)
	}
	if len(n.holdsOf(handle)) == 0 {
		n.removeHoldsDirectory(ctx, handle)
	}
}

// resumeSingleFile reads a hold written as one file at holds/<handle>,
// the layout of a driver that allowed one pod per node per handle. A
// hold whose target is still a mount is written again in the directory
// layout, so the pod that an upgrade of the driver finds running keeps
// its copy.
func (n *node) resumeSingleFile(ctx context.Context, handle string) {
	path := n.store.holdsPath(handle)
	taken, err := readHold(path)
	if err != nil {
		n.logger.WarnContext(ctx, "the hold was not read",
			"volume", handle, "file", path, "error", err)
	}
	n.removeHoldFile(ctx, handle, path)
	if err != nil {
		return
	}
	if !n.mounted(taken.Target) {
		n.logger.InfoContext(ctx, "the hold was given up while the plugin was down",
			"volume", handle, "target", taken.Target)
		return
	}
	n.keepHold(ctx, handle, taken)
}

// removeHoldFile removes one hold's file that resume gave up or moved.
func (n *node) removeHoldFile(ctx context.Context, handle, path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		n.logger.WarnContext(ctx, "the hold was not removed",
			"volume", handle, "file", path, "error", err)
	}
}

// readHold reads one pod's claim on a copy from its file.
func readHold(path string) (hold, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return hold{}, err
	}
	taken := hold{}
	if err := json.Unmarshal(content, &taken); err != nil {
		return hold{}, err
	}
	return taken, nil
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
