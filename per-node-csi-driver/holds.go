package main

// holds.go records which pod holds which copy on this node, so the rule
// of one pod per node per handle survives a restart of the plugin.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// hold is one pod's claim on one copy. It is written beside the copies,
// so a restarted plugin reads it back from the disk. Memory does not
// survive the restart.
type hold struct {
	PodUID       string `json:"podUid"`
	PodName      string `json:"podName,omitempty"`
	PodNamespace string `json:"podNamespace,omitempty"`
	Target       string `json:"targetPath"`
}

// holder returns the pod that holds the handle on this node, and false
// when no pod does.
func (n *node) holder(handle string) (hold, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	standing, found := n.holds[handle]
	return standing, found
}

// keepHold records the hold in memory and on the disk. A hold the driver
// cannot write is logged and nothing more, because the pod has its
// mount and a mount is worth more than the record of it.
func (n *node) keepHold(ctx context.Context, handle string, taken hold) {
	n.mu.Lock()
	n.holds[handle] = taken
	n.mu.Unlock()
	n.reportVolumes()

	path := n.store.holdPath(handle)
	content, err := json.Marshal(taken)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o755)
	}
	if err == nil {
		err = os.WriteFile(path, content, 0o600)
	}
	if err != nil {
		n.logger.WarnContext(ctx, "the hold was not written",
			"volume", handle, "error", err)
	}
}

// dropHold gives the handle up in memory and on the disk, so the next
// pod on this node can take it.
func (n *node) dropHold(ctx context.Context, handle string) {
	n.mu.Lock()
	delete(n.holds, handle)
	n.mu.Unlock()
	n.reportVolumes()

	if err := os.Remove(n.store.holdPath(handle)); err != nil && !os.IsNotExist(err) {
		n.logger.WarnContext(ctx, "the hold was not removed",
			"volume", handle, "error", err)
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
// holds right now. It runs after every change to n.holds, the one map
// that says what is mounted.
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
		taken, err := readHold(n.store.holdPath(handle))
		if err != nil {
			n.logger.WarnContext(ctx, "the hold was not read",
				"volume", handle, "error", err)
			n.dropHold(ctx, handle)
			continue
		}
		if !n.mounted(taken.Target) {
			n.logger.InfoContext(ctx, "the hold was given up while the plugin was down",
				"volume", handle, "target", taken.Target)
			n.dropHold(ctx, handle)
			continue
		}
		n.mu.Lock()
		n.holds[handle] = taken
		n.mu.Unlock()
	}
	// The two branches above already call reportVolumes through
	// dropHold. This call covers the holds resume restores by writing
	// n.holds directly, which do not.
	n.reportVolumes()
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
