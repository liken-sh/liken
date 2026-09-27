package main

// storemount.go finds the filesystem that holds the store, so a node
// plugin whose store the next reboot deletes says so at start. The
// hostPath volume creates the store's directory wherever the path
// leads. On a liken node without the pod-storage partition, that is the
// root overlay, whose writes are in memory. The directory is writable
// and the driver serves, and every repository and every unpushed write
// is lost at the next reboot.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// memoryFilesystems are the filesystem types whose files the kernel
// keeps in memory alone. rootfs is the initramfs root, a ramfs or a
// tmpfs by another name.
var memoryFilesystems = map[string]bool{"tmpfs": true, "ramfs": true, "rootfs": true}

// mountEntry is one line of mountinfo: the mount point, the
// filesystem type, and the options the filesystem itself took.
type mountEntry struct {
	point   string
	fstype  string
	options string
}

// readMountTable parses mountinfo. A line has a variable count of
// optional fields, and a lone "-" ends them. The type, the source, and
// the filesystem's own options follow it. A line that does not have
// this shape is skipped.
func readMountTable(path string) ([]mountEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries := []mountEntry{}
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		for i := 5; i+3 < len(fields); i++ {
			if fields[i] == "-" {
				entries = append(entries, mountEntry{
					point:   mountEscapes.Replace(fields[4]),
					fstype:  fields[i+1],
					options: fields[i+3],
				})
				break
			}
		}
	}
	return entries, lines.Err()
}

// holding is the mount that holds the path: the one with the longest
// mount point above it. Two mounts on the same point are a stack, and
// the later line is the one on top.
func holding(table []mountEntry, path string) (mountEntry, bool) {
	var found mountEntry
	held := false
	for _, entry := range table {
		if !under(path, entry.point) || (held && len(entry.point) < len(found.point)) {
			continue
		}
		found, held = entry, true
	}
	return found, held
}

// under reports whether the path is the mount point or below it.
func under(path, point string) bool {
	return point == "/" || path == point || strings.HasPrefix(path, point+"/")
}

// optionEscapes decodes a value in the filesystem's own options. The
// kernel writes a comma, an equals sign, and white space in a value as
// octal escapes, so a comma in the value does not end the option.
var optionEscapes = strings.NewReplacer(`\054`, ",", `\075`, "=",
	`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)

// upperDirectory is the overlay's upperdir option, the directory that
// takes every write, or empty for an overlay that takes none.
func upperDirectory(options string) string {
	for _, option := range strings.Split(options, ",") {
		if value, found := strings.CutPrefix(option, "upperdir="); found {
			return optionEscapes.Replace(value)
		}
	}
	return ""
}

// storeRefusal reads the mount table at tablePath and gives the reason
// a store at storePath does not survive a reboot, or an empty string
// when it does.
//
// An overlay keeps its writes in its upper directory, so the driver
// reads the mount under that directory. It reads one level only. The
// upperdir option is a path as the process that mounted the overlay
// saw it. A node plugin in a pod has a mount table of its own, and the
// path does not name a mount in it. The mount that holds the path in
// the pod's table is then the container's own root, an overlay again,
// and that is not evidence about the node's disk. An overlay whose upper
// directory is not on a disk this table names is refused, because
// that is how the root overlay of a liken node reads from inside a
// pod.
func storeRefusal(tablePath, storePath string) string {
	table, err := readMountTable(tablePath)
	if err != nil {
		return fmt.Sprintf("the mount table %s was not read: %v", tablePath, err)
	}
	if resolved, err := filepath.EvalSymlinks(storePath); err == nil {
		storePath = resolved
	}
	store, found := holding(table, filepath.Clean(storePath))
	if !found {
		return fmt.Sprintf("no mount in %s holds the store %s", tablePath, storePath)
	}
	if memoryFilesystems[store.fstype] {
		return fmt.Sprintf("the store %s is on %s at %s, which the kernel keeps in memory",
			storePath, store.fstype, store.point)
	}
	if store.fstype != "overlay" {
		return ""
	}
	upper := upperDirectory(store.options)
	if upper == "" {
		return fmt.Sprintf("the store %s is on an overlay at %s with no upper directory, "+
			"so it takes no writes", storePath, store.point)
	}
	below, found := holding(table, upper)
	switch {
	case found && memoryFilesystems[below.fstype]:
		return fmt.Sprintf("the store %s is on an overlay at %s whose upper directory %s is on %s at %s, "+
			"which the kernel keeps in memory", storePath, store.point, upper, below.fstype, below.point)
	case !found || below.fstype == "overlay":
		return fmt.Sprintf("the store %s is on an overlay at %s whose upper directory %s is on no disk "+
			"in this mount table, so nothing shows that it survives a reboot", storePath, store.point, upper)
	}
	return ""
}

// checkStore reads the mount table once, at start. The hostPath mount
// of the store is fixed for the life of the pod, so its filesystem
// cannot change while this process runs.
//
// A store that fails the check still serves read-only volumes: their
// trees are copies of the remote, and a reboot costs one clone again.
// A writeable volume holds work that exists nowhere else until it
// pushes, so the node refuses to stage a new one.
func (n *node) checkStore(ctx context.Context) {
	n.storeRefusal = storeRefusal(n.mountinfo, n.store.root)
	n.readings.storeRefuses.Set(gauge(n.storeRefusal != ""))
	if n.storeRefusal != "" {
		n.logger.ErrorContext(ctx, "the node refuses writeable volumes, because the store does not survive a reboot",
			"reason", n.storeRefusal)
	}
}

// refuseWriteable is the error a stage of a new writeable volume
// answers on a node whose store does not survive a reboot. The kubelet
// writes it into the pod's events, where a person reads it.
func (n *node) refuseWriteable() error {
	return status.Errorf(codes.FailedPrecondition,
		"this node refuses writeable volumes, because the store does not survive a reboot: %s",
		n.storeRefusal)
}
