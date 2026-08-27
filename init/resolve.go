package main

// Resolving a declared disk name to the disk it names, for the claim
// path.
//
// spec.storage.<role>.device may hold a kernel device path (/dev/vda)
// or a stable name under /dev/disk/by-id/ or /dev/disk/by-path/
// (machine/storage.go explains why a manifest may use either form).
// Claiming a blank disk is the only place that reads this field, so
// it is the only place that has to turn a stable name back into a
// disk.
//
// The resolution never reads /dev/disk. That tree is built by
// disklinks.go and its siblings, code that runs once storage has
// already settled, so the very first boot that claims a disk under a
// stable name has no tree there yet to read. Instead,
// resolveDeclaredDisk recomputes every attached disk's own names
// straight from sysfs, the same computation disklinks.go uses to
// build the tree, and compares the declared name against that.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/machine"
)

// The shape of the declared-disk wait. These are variables so a
// test can prove the timeout in milliseconds instead of waiting out
// the real 30 seconds; a real boot never points them anywhere else.
// The deadline is long enough that reaching it means the disk is
// not coming, not that it is slow.
var (
	declaredDiskPoll     = 500 * time.Millisecond
	declaredDiskDeadline = 30 * time.Second
)

// awaitDeclaredDisk waits, boundedly, for a declared device to
// attach. Every path that claims or wipes a disk hits the same probe
// race: the controller's driver has loaded, but the SATA link, the
// USB device, or the mmc card finishes attaching a moment later, and
// mmc card detection runs on a workqueue up to a second behind. A
// device the manifest names is expected to exist, so the caller
// reports its continued absence at the deadline. Only the not-found
// case waits: an ambiguity is two disks however long the code waits,
// and a disk that attaches later cannot un-name them.
func awaitDeclaredDisk(declared string, deadline time.Duration, notice string) (*machine.BlockDevice, error) {
	disk, err := resolveDeclaredDisk(declared)
	if err != nil || disk != nil {
		return disk, err
	}
	fmt.Println(notice)
	for begin := time.Now(); time.Since(begin) < deadline; {
		time.Sleep(declaredDiskPoll)
		disk, err = resolveDeclaredDisk(declared)
		if err != nil || disk != nil {
			return disk, err
		}
	}
	return nil, nil
}

// resolveDeclaredDisk turns one declared device string into the disk
// it names. A plain device path, such as /dev/vda, names whichever
// disk currently answers to that kernel name; it returns nil when no
// attached disk does. A stable name under /dev/disk/by-id/ or
// /dev/disk/by-path/ is matched against every attached disk's own
// computed names instead of read back from a link.
//
// A declared name that matches no disk is the ordinary missing-disk
// case: the disk has not attached yet, or the spec names one that
// does not exist at all. A name that matches two disks is different.
// Guessing which one the spec meant could partition the wrong disk
// and destroy whatever it holds, so resolveDeclaredDisk refuses
// instead, the same way matchRoles refuses two partitions that answer
// to one role's name.
func resolveDeclaredDisk(declared string) (*machine.BlockDevice, error) {
	suffix, ok := strings.CutPrefix(declared, "/dev/disk/")
	if !ok {
		return diskByPath(declared), nil
	}
	tree, name, _ := strings.Cut(suffix, "/")
	switch tree {
	case "by-id":
		return resolveStableName(declared, name, diskIDNames)
	case "by-path":
		return resolveStableName(declared, name, func(disk string) []string {
			if path := diskPathName(disk); path != "" {
				return []string{path}
			}
			return nil
		})
	}
	// Validate refuses every other /dev/disk tree before a spec ever
	// reaches init: by-uuid names a filesystem, and a role claims a
	// blank disk, so no other tree is legal at all
	// (machine/storage.go's Validate). The claim path checks again
	// anyway, because the code about to partition a disk must not
	// depend on an earlier gate having run.
	return nil, fmt.Errorf("declared device %s names a /dev/disk/%s tree; only by-id and by-path names resolve to a disk", declared, tree)
}

// resolveStableName matches one stable name against every attached
// disk's own computed names, calling namesFor once per disk to get
// the names that disk answers to under the tree the declared name
// came from.
func resolveStableName(declared, name string, namesFor func(disk string) []string) (*machine.BlockDevice, error) {
	var matches []machine.BlockDevice
	for _, d := range discoverBlockDevices() {
		if slices.Contains(namesFor(d.Name), name) {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return &matches[0], nil
	}
	var disks []string
	for _, d := range matches {
		disks = append(disks, d.Name)
	}
	return nil, fmt.Errorf("declared device %s matches %d disks (%s); refusing to guess",
		declared, len(matches), strings.Join(disks, ", "))
}
