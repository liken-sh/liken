package main

// Stable names for the disks a machine reaches over the network.
//
// The kernel names a SCSI disk in probe order: sda, sdb, sdc. That
// name is useless as an identity, because it depends on which disk
// answered first, and an iSCSI LUN that logs in one boot later gets a
// different letter. A full distribution solves this with udev, which
// runs a rules engine over every device event and publishes a tree of
// symlinks under /dev/disk. One of those trees is by-path, whose
// names describe how to reach a device rather than which letter it
// won.
//
// A CSI driver has no other option. It asks the target for a LUN, and
// the answer it gets back is an address, a target name, and a LUN
// number, never a kernel letter. So every iSCSI CSI driver waits for
// the by-path name and mounts nothing until that name exists.
//
// liken does not run udev, for the reason disks.go states: every
// value udev publishes comes from reading sysfs, and liken can read
// sysfs itself. That holds here too. udev builds an iSCSI by-path
// name in its path_id builtin out of four values, and all four are
// plain sysfs attributes that exist the moment the LUN appears:
//
//	ip-<persistent_address>:<persistent_port>-iscsi-<targetname>-lun-<lun>
//
// This file is that computation, and the directories under /dev/disk
// that hold its results. The component owns every one of them
// outright: nothing else on a liken machine writes into any of them,
// so an entry a walk did not produce is stale by definition, and
// reconcileLinks removes it. This is why a disk's departure, whether
// an iSCSI session logs out or a local drive is unplugged, needs no
// handling of its own: the disk leaves sysfs, the next walk stops
// naming it, and the next reconcile takes its names away.
//
// The tree grew as designed. iscsiPaths and localPaths both answer
// one question, which port does a disk answer on, and their pairs
// share one directory, /dev/disk/by-path, because that is the one
// question a CSI driver and a person tracing a cable both ask.
// idPaths answers a different question, which disk is this, from the
// identity values a disk's own firmware reports, and publishes them
// under /dev/disk/by-id. uuidPaths answers a third question, what
// filesystem does this partition hold, from the bytes mke2fs or
// liken's own FAT formatter wrote there, and publishes them under
// /dev/disk/by-uuid. Every link in every one of these trees is
// relative (../../sdb), for the reason given above: a relative link
// resolves the same on both sides of the bind mount that carries
// /dev into a CSI node plugin's container.
//
// A name two disks produce in the same tree is a collision.
// mergeDiskLinks, where every builder's pairs land, publishes such a
// name for neither disk, and prints one line to stderr naming both,
// rather than pointing the name at whichever disk it read last.
//
// Nothing here is specific to iSCSI except iscsiPaths itself.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liken-sh/liken/hardware"
)

// These are the roots this file reads and writes. They are variables
// rather than constants so that tests can build a fake machine in a
// tempdir. On a real boot, they never hold anything else.
var (
	iscsiSessionClass    = "/sys/class/iscsi_session"
	iscsiConnectionClass = "/sys/class/iscsi_connection"
	diskLinksDir         = "/dev/disk/by-path"
	diskIDsDir           = "/dev/disk/by-id"
	diskUUIDsDir         = "/dev/disk/by-uuid"
)

// diskLinksQuiet is how long the walk waits for a burst of uevents to
// stop. It is shorter than the hardware watch's interval, because a
// CSI driver is blocked on the name this walk publishes, while the
// unclaimed-device report is something a person reads later.
const diskLinksQuiet = 250 * time.Millisecond

// watchDiskLinks is the machine-plane component that keeps
// /dev/disk/by-path, /dev/disk/by-id, and /dev/disk/by-uuid correct.
// It reconciles all three once at start, so a machine that already
// holds disks and sessions is right without waiting for an event, and
// then again after every settled burst of uevents.
//
// A login and a logout both announce themselves: the kernel sends add
// events as the SCSI target, the SCSI device, the block device, and
// the partitions appear, and remove events as they go. Attaching or
// detaching a local disk announces itself the same way. The existing
// uevent filter passes all of it. As everywhere else in liken, the
// event is only a signal that something changed, and the walk
// re-reads the whole truth, so a coalesced or missed event costs
// nothing.
func watchDiskLinks(ctx context.Context) error {
	uevents, err := hardware.ListenForUevents(ctx)
	if err != nil {
		return err
	}
	for {
		reconcileDiskLinks(diskLinksDir, localPaths())
		reconcileDiskLinks(diskIDsDir, idPaths())
		reconcileDiskLinks(diskUUIDsDir, uuidPaths())
		select {
		case <-ctx.Done():
			return nil
		case <-uevents:
		}
		// One LUN or local disk arriving produces a burst: the SCSI
		// target, the SCSI device, the block device, and one event
		// per partition. A walk into a half-populated tree would name
		// the disk wrong, so this waits for the burst to finish.
		settle(ctx, uevents, diskLinksQuiet, 5*time.Second)
		if ctx.Err() != nil {
			return nil
		}
	}
}

// reconcileDiskLinks reconciles one tree and reports a failure to
// stderr rather than stopping the boot: a CSI driver blocked on a
// by-path name cannot wait for a person to notice a log line, but a
// fault reconciling one tree, for example a full /dev, must not cost
// the other two trees their own reconcile.
func reconcileDiskLinks(dir string, want map[string]string) {
	if err := reconcileLinks(dir, want); err != nil {
		fmt.Fprintf(os.Stderr, "liken: disk links: %v\n", err)
	}
}

// iscsiPaths returns the by-path links this machine's iSCSI sessions
// imply, as link name to the relative target the link points at.
//
// The links are relative (../../sdb), as udev writes them. Every CSI
// node plugin sees the host's /dev through a bind mount, and a
// relative link resolves the same on both sides of that mount, while
// an absolute one would only resolve where /dev sits at /dev.
func iscsiPaths() map[string]string {
	sessions, err := os.ReadDir(iscsiSessionClass)
	if err != nil {
		// scsi_transport_iscsi creates this class, and that module
		// loads only when the cluster declares the iscsi feature. Its
		// absence is the ordinary case on most machines, not a fault.
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

		// The address belongs to the connection, not the session, and
		// a session numbers its connections from zero. iSCSI allows
		// several connections in one session, and udev reads the
		// first one, so a multi-connection session still yields one
		// name for the LUN rather than one name per path to it.
		//
		// These are the persistent values, the ones the login was
		// configured with, rather than address and port, which hold
		// whatever the current connection negotiated. A target that
		// redirects a login would otherwise change the disk's name
		// out from under whoever mounted it.
		connection := filepath.Join(iscsiConnectionClass, "connection"+number+":0")
		address := sysfsString(connection, "persistent_address")
		port := sysfsString(connection, "persistent_port")

		// A session mid-login, or one being torn down while this walk
		// reads it, publishes some of these files and not others. A
		// partial name would be wrong, so this session contributes
		// nothing and the next uevent brings another walk.
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

// sessionLUNs finds the block devices one session carries, keyed by
// LUN number. The values are sysfs directories, because the caller
// needs both the disk's name, which is the directory's base name, and
// its partitions, which are its subdirectories.
//
// deviceDir is the session's own device directory, which holds one
// entry per SCSI target the session reached. Under each target sits
// one directory per SCSI device, named with the four-part address
// host:bus:target:lun. That last field is the LUN the target
// advertised, and it is the number the CSI driver asked for.
func sessionLUNs(deviceDir string) map[string]string {
	luns := map[string]string{}
	targets, err := os.ReadDir(deviceDir)
	if err != nil {
		return luns
	}
	for _, scsiTarget := range targets {
		// The session directory also holds its connections, its
		// class entry, and the usual power and uevent files. Only the
		// target directories lead to disks.
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
			// A SCSI device that is not a disk, such as the
			// controller most targets expose at LUN 0, has no block
			// directory, and this read passes over it.
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

// addDiskLinks records the names one block device answers to: the
// disk itself, and name-part<N> for each partition it carries. The
// suffix is udev's, from the by-path rule in
// 60-persistent-storage.rules, and the number is the kernel's own
// partition number rather than a count, so a disk whose second
// partition was deleted still names the third one part3.
func addDiskLinks(links map[string]string, name, deviceDir string) {
	device := filepath.Base(deviceDir)
	links[name] = "../../" + device
	entries, err := os.ReadDir(deviceDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		// A partition's directory sits inside its disk's, is named
		// for the partition, and holds a partition file that no other
		// entry there has.
		number := sysfsString(filepath.Join(deviceDir, entry.Name()), "partition")
		if number == "" {
			continue
		}
		links[name+"-part"+number] = "../../" + entry.Name()
	}
}

// localPaths returns the by-path links this machine's local disks
// imply: the port each one answers on, from diskPathName, plus its
// partitions. It merges iscsiPaths in as well, so the whole by-path
// tree comes from one map and one reconcileLinks call: a local disk's
// port and an iSCSI LUN's address answer the same question, and
// belong in the same directory.
func localPaths() map[string]string {
	links := map[string]string{}
	blocked := map[string]bool{}
	for _, disk := range discoverBlockDevices() {
		path := diskPathName(disk.Name)
		if path == "" {
			continue
		}
		one := map[string]string{}
		addDiskLinks(one, path, filepath.Join(sysBlock, disk.Name))
		mergeDiskLinks(links, blocked, one)
	}
	mergeDiskLinks(links, blocked, iscsiPaths())
	return links
}

// idPaths returns the by-id links this machine's local disks imply:
// every name diskIDNames computes for a disk, plus that disk's
// partitions under each of those names. A disk with no by-id name,
// for example a SATA disk with no WWN, contributes nothing.
func idPaths() map[string]string {
	links := map[string]string{}
	blocked := map[string]bool{}
	for _, disk := range discoverBlockDevices() {
		dir := filepath.Join(sysBlock, disk.Name)
		one := map[string]string{}
		for _, id := range diskIDNames(disk.Name) {
			addDiskLinks(one, id, dir)
		}
		mergeDiskLinks(links, blocked, one)
	}
	return links
}

// uuidPaths returns the by-uuid links this machine's partitions
// imply: one link per partition whose filesystem carries a UUID, from
// filesystemUUID. There is no -part<N> suffix here, unlike the other
// two trees: a UUID already names one filesystem, and a suffix naming
// the disk it happens to sit on this boot would say nothing the UUID
// does not already say.
//
// Reading a UUID means reading a partition's own bytes, the
// superblock or the FAT boot sector, through its device node under
// devRoot. This is the one builder in this file that touches a
// device's contents; iscsiPaths, localPaths, and idPaths read only
// sysfs, which is why they run safely against a disk whose partitions
// carry no filesystem yet.
func uuidPaths() map[string]string {
	links := map[string]string{}
	blocked := map[string]bool{}
	for _, p := range discoverPartitions() {
		uuid := filesystemUUID(filepath.Join(devRoot, p.name))
		if uuid == "" {
			continue
		}
		mergeDiskLinks(links, blocked, map[string]string{uuid: "../../" + p.name})
	}
	return links
}

// mergeDiskLinks folds one claimant's pairs into a tree's link map,
// enforcing the rule every tree in this file shares: a name two
// claimants produce is published for neither, and the reason goes to
// stderr rather than into a guess about which claimant is right. A
// silent overwrite would point the name at whichever claimant this
// pass happened to read last.
//
// links and blocked carry the state across every call for one tree,
// so the rule holds however many claimants contribute to it and
// whichever builder they came from: a local disk and an iSCSI LUN
// competing for the same by-path name lose it exactly as two local
// disks competing for the same by-id name do.
func mergeDiskLinks(links map[string]string, blocked map[string]bool, claim map[string]string) {
	for name, target := range claim {
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
		fmt.Fprintf(os.Stderr, "liken: disk links: %s names both %s and %s; publishing neither\n",
			name, strings.TrimPrefix(current, "../../"), strings.TrimPrefix(target, "../../"))
		delete(links, name)
		blocked[name] = true
	}
}

// reconcileLinks makes dir hold exactly the links in want, and
// nothing else. It returns the first error it meets, but it keeps
// going past that error, so one link that cannot be written does not
// cost every other disk its name.
func reconcileLinks(dir string, want map[string]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Nothing to prune and nothing to publish means there is no
		// reason for the directory to exist. This is the state of
		// every machine that runs no network storage, and an empty
		// by-path tree there would claim more than liken does.
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
		// A link whose disk moved letters has to be replaced, and a
		// replacement that unlinks first leaves a window where the
		// name does not exist. A CSI driver polling for that name
		// would read the window as a missing device. So the new link
		// is built beside the old one and renamed over it, which the
		// kernel does in one step. The temporary name starts with a
		// dot, so it can never collide with a name this file
		// publishes, and the prune above clears any that a crash left
		// behind.
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
