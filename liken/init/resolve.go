package main

// spec.storage.<role>.device is a kernel device path (/dev/vda) or a
// stable name under /dev/disk/by-id/ or /dev/disk/by-path/
// (machine/storage.go explains both forms). The claim and the reinstall
// must find the disk that the name refers to.
//
// The lookup does not read /dev/disk, because init creates those links
// (disklinks.go) only after the boot steps finish, so they do not exist
// yet when init claims a disk. resolveDeclaredDisk computes each attached
// disk's names from sysfs with diskIDNames and diskPathName, the
// functions that disklinks.go uses, and compares the declared name with
// them.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

const (
	declaredDiskPoll     = 500 * time.Millisecond
	declaredDiskDeadline = 30 * time.Second
)

// awaitDeclaredDisk resolves a declared device, and while no disk
// matches, retries every declaredDiskPoll until declaredDiskDeadline.
// A disk can attach after its controller's driver has loaded: a SATA
// link or a USB device takes time to come up, and the kernel detects an
// mmc card from a workqueue that can run up to a second later. At the
// deadline it returns nil with no error, and the caller reports the
// missing disk. It does not retry an error, because a name that matches
// two disks still matches them after a wait. It prints notice once
// before the first wait.
func awaitDeclaredDisk(declared string, notice string) (*machine.BlockDevice, error) {
	disk, err := resolveDeclaredDisk(declared)
	if err != nil || disk != nil {
		return disk, err
	}
	fmt.Println(notice)
	for begin := time.Now(); time.Since(begin) < declaredDiskDeadline; {
		time.Sleep(declaredDiskPoll)
		disk, err = resolveDeclaredDisk(declared)
		if err != nil || disk != nil {
			return disk, err
		}
	}
	return nil, nil
}

// resolveDeclaredDisk returns the attached disk that a declared device
// refers to, or nil with no error when no disk matches. It returns an
// error when a name matches more than one disk, because a guess could
// partition the wrong disk and destroy its data.
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
	// Validate (machine/storage.go) already rejects every other tree.
	// The code that is about to partition a disk checks again, so it
	// does not depend on validation having run.
	return nil, fmt.Errorf("declared device %s is in the /dev/disk/%s tree, and only by-id and by-path names resolve to a disk", declared, tree)
}

// resolveStableName returns the attached disk for which namesFor
// returns name. namesFor computes one disk's names in the by-id or
// by-path tree.
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
	return nil, fmt.Errorf("declared device %s matches %d disks (%s), refusing to guess",
		declared, len(matches), strings.Join(disks, ", "))
}
