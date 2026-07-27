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
// This file is that computation plus the directory that holds its
// results. The component owns /dev/disk/by-path outright: nothing
// else on a liken machine writes there, so an entry that this walk
// does not produce is stale by definition, and reconcileLinks removes
// it. This is why a logout needs no handling of its own: the session
// leaves sysfs, the next walk stops naming its disks, and the next
// reconcile takes the names away.
//
// The tree is meant to grow. A name here is a pair, a link name and
// the device it points at, and iscsiPaths is one builder of such
// pairs. Local disks (PCI, ATA, NVMe, USB) have by-path names too,
// and by-id and by-uuid are sibling directories of exactly the same
// shape. Adding either is a second builder whose pairs merge into the
// same map, or a second reconcileLinks call against a second
// directory. Nothing here is specific to iSCSI except iscsiPaths.

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
)

// diskLinksQuiet is how long the walk waits for a burst of uevents to
// stop. It is shorter than the hardware watch's interval, because a
// CSI driver is blocked on the name this walk publishes, while the
// unclaimed-device report is something a person reads later.
const diskLinksQuiet = 250 * time.Millisecond

// watchDiskLinks is the machine-plane component that keeps
// /dev/disk/by-path correct. It reconciles once at start, so a
// machine that already holds sessions is right without waiting for an
// event, and then again after every settled burst of uevents.
//
// A login and a logout both announce themselves: the kernel sends add
// events as the SCSI target, the SCSI device, the block device, and
// the partitions appear, and remove events as they go. The existing
// uevent filter passes both. As everywhere else in liken, the event
// is only a signal that something changed, and the walk re-reads the
// whole truth, so a coalesced or missed event costs nothing.
func watchDiskLinks(ctx context.Context) error {
	uevents, err := hardware.ListenForUevents(ctx)
	if err != nil {
		return err
	}
	for {
		if err := reconcileLinks(diskLinksDir, iscsiPaths()); err != nil {
			fmt.Fprintf(os.Stderr, "liken: disk links: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-uevents:
		}
		// One LUN arriving produces a burst: the SCSI target, the
		// SCSI device, the block device, and one event per partition.
		// A walk into a half-populated tree would name the disk
		// wrong, so this waits for the burst to finish.
		settle(ctx, uevents, diskLinksQuiet, 5*time.Second)
		if ctx.Err() != nil {
			return nil
		}
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
