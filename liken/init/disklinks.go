package main

// Init creates stable names for disks under /dev/disk/by-path, by-id,
// and by-uuid. It computes them from sysfs and the partitions' own
// bytes because it does not run udev.
//
// The kernel names a SCSI disk in probe order (sda, sdb, sdc), so the
// name changes when an iSCSI LUN logs in later in one boot than in
// the last. A CSI driver gets an address, a target name, and a LUN
// number from the target, never a kernel letter, so it waits for the
// by-path name and mounts nothing until that name exists. udev builds
// that name in its path_id builtin from four sysfs attributes, which
// exist as soon as the LUN appears:
//
//	ip-<persistent_address>:<persistent_port>-iscsi-<targetname>-lun-<lun>
//
// iscsiPaths and localPaths build /dev/disk/by-path, which names a
// disk by the port it is reached on. idPaths builds /dev/disk/by-id
// from the identity values that the disk's firmware reports.
// uuidPaths builds /dev/disk/by-uuid from the filesystem UUID that
// mke2fs or liken's FAT formatter wrote into each partition.
//
// Nothing else on a machine writes into these directories, so
// reconcileLinks removes every entry that a walk did not produce. When
// an iSCSI session logs out or a local drive is unplugged, the disk
// leaves sysfs and the next walk removes its names.
//
// Every link is relative (../../sdb), because a CSI node plugin reads
// the host's /dev through a bind mount at another path, and an
// absolute link would not resolve there.
//
// When two disks produce the same name in one tree, mergeDiskLinks
// publishes the name for neither, because pointing it at whichever
// disk was read last would be a guess.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liken-sh/liken/liken/hardware"
)

// The roots this file reads and writes. Tests replace them to build a
// fake machine in a temporary directory.
var (
	iscsiSessionClass    = "/sys/class/iscsi_session"
	iscsiConnectionClass = "/sys/class/iscsi_connection"
	diskLinksDir         = "/dev/disk/by-path"
	diskIDsDir           = "/dev/disk/by-id"
	diskUUIDsDir         = "/dev/disk/by-uuid"
)

// diskLinksQuiet is how long watchDiskLinks waits for a burst of
// uevents to stop before it walks. It is shorter than the hardware
// watch's interval because a CSI driver is blocked until the name
// appears, while the unclaimed-device report is read later by a person.
const diskLinksQuiet = 250 * time.Millisecond

// watchDiskLinks keeps the three link trees matching the disks
// present. It walks once at start and again after each burst of
// uevents settles. Each walk reads all of sysfs again, so an event
// that Settle coalesced loses nothing.
func watchDiskLinks(ctx context.Context) error {
	uevents, err := listenForUevents(ctx)
	if err != nil {
		return err
	}
	var seen diskLinkSeen
	for {
		seen = walkDiskLinks(seen)
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-uevents:
			if !ok {
				return errUeventsStopped
			}
		}
		// A new disk sends uevents for its SCSI target, its SCSI
		// device, its block device, and each partition. Wait for them
		// to stop, so the walk reads sysfs after the disk is complete.
		hardware.Settle(ctx, uevents, diskLinksQuiet, 5*time.Second)
		if ctx.Err() != nil {
			return nil
		}
	}
}

// diskLinkSeen holds the collisions each tree reported on the previous
// walk, so reportNewCollisions prints only new ones. The zero value
// means nothing was reported yet.
type diskLinkSeen struct {
	path, id, uuid map[diskLinkCollision]bool
}

// walkDiskLinks reconciles all three trees and returns this walk's
// collisions for the next call. Tests call it directly, without a
// uevent socket.
func walkDiskLinks(seen diskLinkSeen) diskLinkSeen {
	pathLinks, pathCollisions := localPaths()
	idLinks, idCollisions := idPaths()
	uuidLinks, uuidCollisions := uuidPaths()
	reconcileDiskLinks(diskLinksDir, pathLinks)
	reconcileDiskLinks(diskIDsDir, idLinks)
	reconcileDiskLinks(diskUUIDsDir, uuidLinks)
	return diskLinkSeen{
		path: reportNewCollisions(pathCollisions, seen.path),
		id:   reportNewCollisions(idCollisions, seen.id),
		uuid: reportNewCollisions(uuidCollisions, seen.uuid),
	}
}

// reconcileDiskLinks prints a failure to stderr instead of returning
// it, so a failure in one tree, such as a full /dev, does not stop the
// other trees from reconciling.
func reconcileDiskLinks(dir string, want map[string]string) {
	if err := reconcileLinks(dir, want); err != nil {
		fmt.Fprintf(os.Stderr, "liken: disk links: %v\n", err)
	}
}

// diskLinkCollision is a link name that two devices produced in one
// walk. first and second are the devices' kernel names, such as sdb.
type diskLinkCollision struct {
	name          string
	first, second string
}

// reportNewCollisions prints the collisions in current that are not in
// seen, and returns current as seen for the next walk. Every uevent
// starts a walk, so without seen a collision that lasts would print on
// every walk. A collision that clears and later returns prints again.
func reportNewCollisions(current []diskLinkCollision, seen map[diskLinkCollision]bool) map[diskLinkCollision]bool {
	next := make(map[diskLinkCollision]bool, len(current))
	for _, c := range current {
		next[c] = true
		if seen[c] {
			continue
		}
		fmt.Fprintf(os.Stderr, "liken: disk links: %s would point at both %s and %s, so it is published for neither\n",
			c.name, c.first, c.second)
	}
	return next
}

// iscsiPaths returns the by-path links for the machine's iSCSI
// sessions, as link name to relative target, such as "../../sdb".
func iscsiPaths() map[string]string {
	sessions, err := os.ReadDir(iscsiSessionClass)
	if err != nil {
		// The scsi_transport_iscsi module creates this class, and it
		// loads only when the cluster declares the iscsi feature.
		return nil
	}
	links := map[string]string{}
	for _, session := range sessions {
		number, isSession := strings.CutPrefix(session.Name(), "session")
		if !isSession {
			continue
		}
		dir := filepath.Join(iscsiSessionClass, session.Name())
		target := sysfsString(dir, "targetname")

		// The address is an attribute of the connection, not the
		// session. Like udev, this reads only connection 0, so a LUN
		// has one name however many connections reach it.
		//
		// persistent_address and persistent_port hold the address and
		// port that the login was configured with. address and port
		// change when the target redirects the login, and the name of
		// a mounted disk must not change.
		connection := filepath.Join(iscsiConnectionClass, "connection"+number+":0")
		address := sysfsString(connection, "persistent_address")
		port := sysfsString(connection, "persistent_port")

		// A session that is logging in or logging out can lack some
		// of these files. Skip it. The uevent of the next change
		// starts another walk.
		if target == "" || address == "" || port == "" {
			continue
		}
		prefix := fmt.Sprintf("ip-%s:%s-iscsi-%s-lun-", address, port, target)
		for lun, device := range sessionLUNs(filepath.Join(dir, "device")) {
			addDiskLinks(links, prefix+lun, device)
		}
	}
	return links
}

// sessionLUNs returns the block devices of one session, as LUN number
// to the device's sysfs directory.
//
// deviceDir has one directory per SCSI target, and each target has
// one directory per SCSI device, named host:bus:target:lun.
func sessionLUNs(deviceDir string) map[string]string {
	luns := map[string]string{}
	targets, err := os.ReadDir(deviceDir)
	if err != nil {
		return luns
	}
	for _, scsiTarget := range targets {
		// The directory also has connection, class, power, and uevent
		// entries.
		if !strings.HasPrefix(scsiTarget.Name(), "target") {
			continue
		}
		targetDir := filepath.Join(deviceDir, scsiTarget.Name())
		addresses, err := os.ReadDir(targetDir)
		if err != nil {
			continue
		}
		for _, address := range addresses {
			fields := strings.Split(address.Name(), ":")
			if len(fields) != 4 {
				continue
			}
			// A SCSI device that is not a disk, such as the controller
			// that most targets expose at LUN 0, has no block directory.
			blockDir := filepath.Join(targetDir, address.Name(), "block")
			devices, err := os.ReadDir(blockDir)
			if err != nil {
				continue
			}
			for _, device := range devices {
				luns[fields[3]] = filepath.Join(blockDir, device.Name())
			}
		}
	}
	return luns
}

// addDiskLinks adds to links the name for the disk at deviceDir, and
// name-part<N> for each of its partitions, as udev's
// 60-persistent-storage.rules does. N is the kernel's partition
// number, so the third partition stays part3 after the second is
// deleted.
func addDiskLinks(links map[string]string, name, deviceDir string) {
	device := filepath.Base(deviceDir)
	links[name] = "../../" + device
	entries, err := os.ReadDir(deviceDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		number := sysfsString(filepath.Join(deviceDir, entry.Name()), "partition")
		if number == "" {
			continue
		}
		links[name+"-part"+number] = "../../" + entry.Name()
	}
}

// localPaths returns the whole by-path tree: the local disks, named by
// diskPathName, and the iSCSI LUNs from iscsiPaths, merged into one map
// so that a local disk and a LUN with the same name collide.
func localPaths() (map[string]string, []diskLinkCollision) {
	links := map[string]string{}
	blocked := map[string]bool{}
	var collisions []diskLinkCollision
	for _, disk := range discoverBlockDevices() {
		path := diskPathName(disk.Name)
		if path == "" {
			continue
		}
		one := map[string]string{}
		addDiskLinks(one, path, filepath.Join(sysBlock, disk.Name))
		collisions = mergeDiskLinks(links, blocked, collisions, one)
	}
	collisions = mergeDiskLinks(links, blocked, collisions, iscsiPaths())
	return links, collisions
}

// idPaths returns the by-id links for the machine's local disks: each
// name that diskIDNames returns for a disk, with the disk's partitions
// under each name.
func idPaths() (map[string]string, []diskLinkCollision) {
	links := map[string]string{}
	blocked := map[string]bool{}
	var collisions []diskLinkCollision
	for _, disk := range discoverBlockDevices() {
		dir := filepath.Join(sysBlock, disk.Name)
		one := map[string]string{}
		for _, id := range diskIDNames(disk.Name) {
			addDiskLinks(one, id, dir)
		}
		collisions = mergeDiskLinks(links, blocked, collisions, one)
	}
	return links, collisions
}

// uuidPaths returns one by-uuid link for each partition whose
// filesystem has a UUID. filesystemUUID reads the UUID from the
// superblock or the FAT boot sector through the partition's device
// node under devRoot. The other builders read only sysfs.
func uuidPaths() (map[string]string, []diskLinkCollision) {
	links := map[string]string{}
	blocked := map[string]bool{}
	var collisions []diskLinkCollision
	for _, p := range discoverPartitions() {
		uuid := filesystemUUID(filepath.Join(devRoot, p.name))
		if uuid == "" {
			continue
		}
		collisions = mergeDiskLinks(links, blocked, collisions, map[string]string{uuid: "../../" + p.name})
	}
	return links, collisions
}

// mergeDiskLinks adds one device's links to a tree's links. When a
// name is already in links with a different target, it deletes the
// name, adds it to blocked so that no later device publishes it, and
// appends the collision. The caller passes the same links, blocked,
// and collisions for every device of one tree.
func mergeDiskLinks(links map[string]string, blocked map[string]bool, collisions []diskLinkCollision, deviceLinks map[string]string) []diskLinkCollision {
	for name, target := range deviceLinks {
		if blocked[name] {
			continue
		}
		current, exists := links[name]
		if !exists {
			links[name] = target
			continue
		}
		if current == target {
			continue
		}
		collisions = append(collisions, diskLinkCollision{
			name:   name,
			first:  strings.TrimPrefix(current, "../../"),
			second: strings.TrimPrefix(target, "../../"),
		})
		delete(links, name)
		blocked[name] = true
	}
	return collisions
}

// reconcileLinks makes dir contain exactly the links in want. It
// continues past an error and returns the first one, so one link that
// cannot be written does not stop the others.
func reconcileLinks(dir string, want map[string]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Create the directory only when there is a link to put in
		// it.
		if len(want) == 0 {
			return nil
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	var failure error
	for _, entry := range entries {
		if _, wanted := want[entry.Name()]; wanted {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil && failure == nil {
			failure = err
		}
	}

	for name, target := range want {
		path := filepath.Join(dir, name)
		if current, err := os.Readlink(path); err == nil && current == target {
			continue
		}
		// Removing the old link before creating the new one would
		// leave a moment with no link, and a CSI driver that polls for
		// the name would find the disk missing. So create the link
		// under a temporary name and rename it over the old one, which
		// replaces it atomically. No published name starts with a dot,
		// and the removal loop above deletes a temporary link that a
		// crash left behind.
		staging := filepath.Join(dir, "."+name+".new")
		os.Remove(staging)
		if err := os.Symlink(target, staging); err != nil {
			if failure == nil {
				failure = err
			}
			continue
		}
		if err := os.Rename(staging, path); err != nil {
			os.Remove(staging)
			if failure == nil {
				failure = err
			}
		}
	}
	return failure
}
