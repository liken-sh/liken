package main

// The hardware report boot: a boot that changes nothing.
//
// A new machine asks a question the lab cannot answer: which drivers,
// which interface names, which disk paths does this hardware need in
// its manifest? The lab cannot answer it because the vendored kernel
// builds the paravirtual drivers in, so a lab guest never loads the
// storage or network module that every real controller needs. The
// report boot answers the question on the real machine, before the
// first install.
//
// A person picks "liken hardware report" from the installation stick's
// menu. The boot carries liken.report on its command line, and no
// liken.machine= identity, because it describes the hardware, not a
// machine in the deployment. It does the smallest amount of work that
// produces a real answer:
//
//  1. It mounts the payload's system image to reach the full module
//     tree. The install medium carries the whole OS as liken.sqfs, and
//     that image holds every driver, the alias table, and the softdep
//     information. The report reads all three from there.
//  2. It loads the drivers this hardware wants, from that tree. Loading
//     a module changes only RAM, so the report keeps its promise to
//     change nothing on any disk. The names it needs are real only
//     after the drivers bind: eth0 does not exist until r8169 loads.
//  3. It observes what appeared: every disk, and every interface with
//     its link brought up long enough to see the carrier.
//  4. It writes a proposed manifest to the stick, prints it, and
//     reboots when the person presses Enter.
//
// The report never claims, formats, or writes to any of the machine's
// own disks. It runs before storage settles for exactly this reason:
// nothing downstream of it, not the storage reconciliation and not the
// installer, must ever run on a report boot.

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/hardware"
)

// reportParam makes a boot describe the hardware and change nothing. It
// follows installParam and reinstallParam (reinstall.go), but it is its
// own word because it is the one menu entry that never touches a disk.
const reportParam = "liken.report"

// stickInstallPartition is the GPT partition name that image/stick.go
// writes on the installation stick. The report finds the stick by this
// name to write its proposal there, the same way storage roles are
// found by the names written into their partitions.
const stickInstallPartition = "liken:install"

// hardwareReportName is the proposal's file name at the root of the
// stick's filesystem. A person pulls the stick, reads this file, edits
// it, and uses it as the machine's manifest.
const hardwareReportName = "hardware-report.yaml"

// reportImageMount is where the report loop-mounts the payload's system
// image to reach its module tree. reportStickMount is where it mounts
// the stick's filesystem to write the proposal.
const (
	reportImageMount = "/liken-report-image"
	reportStickMount = "/liken-report-stick"
)

// reporting reports whether this boot's one job is to describe the
// hardware and reboot.
func reporting() bool {
	return bootParam(reportParam)
}

// runHardwareReport is the whole report boot. It gathers the hardware,
// composes the proposal, writes it to the stick, prints it, and reboots
// when the person acknowledges. Like every install-menu terminal state,
// it ends at a held console, because a person picked this entry and is
// present by construction. It never returns.
func runHardwareReport() {
	report := gatherHardwareReport()
	proposal := composeHardwareReport(report)

	writeErr := writeReportToStick(proposal)

	// The console print is the proposal's second copy, and its only copy
	// when the stick write fails. It goes out after the write attempt,
	// so the held message below can tell the person the truth about the
	// file.
	fmt.Println(proposal)

	var message string
	if writeErr == nil {
		message = fmt.Sprintf(
			"liken: this report was written to the stick as %s; press Enter to reboot.",
			hardwareReportName)
	} else {
		fmt.Fprintf(os.Stderr, "liken: report: writing to the stick: %v\n", writeErr)
		message = fmt.Sprintf(
			"liken: writing %s to the stick FAILED; the text above is the only copy; press Enter to reboot.",
			hardwareReportName)
	}
	holdInstallerConsole(message, false)
	rebootAfterReport()
}

// gatherHardwareReport does the observation: it mounts the module tree,
// loads the drivers this hardware wants, and reads back the disks and
// interfaces that appeared. It degrades rather than fails. A machine
// with no reachable module tree still reports the disks and interfaces
// the boot-path drivers already bound, which is more use to a person
// than a blank screen.
func gatherHardwareReport() hardwareReport {
	uefi := firmwareIsUEFI()

	base, pciIDs, unmount, err := mountPayloadModules()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: report: %v\n", err)
		stick, stickPath := stickDisk()
		return hardwareReport{UEFI: uefi, StickPath: stickPath, Disks: readReportDisks(stick), Interfaces: observeInterfaces()}
	}
	defer unmount()

	// Let the boot-path buses finish probing before the first walk. On
	// real hardware, storage settles in the middle of a SATA link's
	// training or a USB device's negotiation, and a disk that has not
	// appeared yet is indistinguishable from one that is not there.
	quiesceHardware()

	var recommendations []moduleRecommendation
	catalog, err := hardware.LoadCatalog(base, pciIDs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: report: no hardware catalog, so no driver recommendations: %v\n", err)
	} else {
		recommendations = recommendModules(catalog, base)
		// Load the full ordered chains from the payload's tree. The
		// declared-module loader loads each name in order and prints the
		// outcome, exactly as a from-disk boot loads spec.modules. The
		// interfaces and disks these drivers create are real only after
		// this returns and the probe settles again.
		loadDeclaredModulesFrom(base, dedupChains(recommendations))
		quiesceHardware()
	}

	// The installation stick is itself a disk, and it must not appear
	// in the proposal: it leaves the machine with the person, so a
	// role laid onto it would vanish with them. The stick is findable
	// only here, after the loads above, because its own controller
	// driver (usb-storage, or uas) is usually among them: before they
	// load, the stick has no block device to find. For the same
	// reason, the driver that exists only to reach the stick must not
	// be recommended into the manifest, though it stayed loaded so
	// the report can write its file.
	//
	// The find must also wait, not just walk. usb-storage delays its
	// scan a full second after the device attaches (its delay_use),
	// and USB enumeration keeps going past the uevent quiet window
	// above, so an immediate walk can run before the stick's disk
	// exists. A report boot expects a stick by construction: a person
	// picked this entry from one. A boot with no stick at all (a
	// hand-typed liken.report) pays the ceiling once and the report
	// stays console-only.
	stick, stickPath := awaitInstallStick(15 * time.Second)
	recommendations = withoutStickRecommendations(recommendations, stick)

	return hardwareReport{
		UEFI:            uefi,
		StickPath:       stickPath,
		Recommendations: recommendations,
		Disks:           readReportDisks(stick),
		Interfaces:      observeInterfaces(),
	}
}

// awaitInstallStick polls for the installation stick's disk until it
// appears or the ceiling passes, ending the moment it is found. The
// poll is on the partition table itself, not on uevents, so it also
// covers the moment between the disk's arrival and its partitions'.
func awaitInstallStick(ceiling time.Duration) (name, path string) {
	deadline := time.Now().Add(ceiling)
	for {
		if name, path = stickDisk(); name != "" || time.Now().After(deadline) {
			return name, path
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// stickDisk names the installation stick's disk twice over: the kernel
// node name for excluding it from the disk walk, and the device path
// for the proposal's evidence. Both are empty when no stick is
// present, which excludes nothing.
func stickDisk() (name, path string) {
	name = installStickDiskName()
	if name == "" {
		return "", ""
	}
	return name, devRoot + "/" + name
}

// mountPayloadModules loop-mounts the payload's system image and returns
// the paths into it that the report reads: the kernel's module tree and
// the PCI naming database. The install medium carries the whole OS as
// liken.sqfs beside the release document, and that image holds the full
// module tree that the boot archive deliberately does not. The mount is
// read-only, so it too changes nothing.
func mountPayloadModules() (base, pciIDs string, unmount func(), err error) {
	image := filepath.Join(releasePayloadDir, slotImageName)
	if err := loopMount(image, reportImageMount); err != nil {
		return "", "", nil, fmt.Errorf("mounting the payload's system image %s: %w", image, err)
	}
	base = filepath.Join(reportImageMount, "lib/modules", kernelRelease())
	pciIDs = filepath.Join(reportImageMount, "usr/share/hwdata/pci.ids")
	unmount = func() { _ = unix.Unmount(reportImageMount, unix.MNT_DETACH) }
	return base, pciIDs, unmount, nil
}

// recommendModules turns the machine's unclaimed devices into ordered
// driver recommendations. For each undriven device it takes the kernel
// build's preferred candidate and expands its soft dependencies, so a
// NIC that needs its PHY library first reads as [realtek, r8169]. The
// evidence it keeps beside each chain is the device in words and its
// modalias fingerprint, so the proposal can say what each driver
// claims. It also keeps each device's place in sysfs, so the report
// can later tell a recommendation that serves the machine from one
// that serves only the installation stick.
func recommendModules(catalog *hardware.Catalog, base string) []moduleRecommendation {
	devices := hardware.DiscoverDevices(sysfsRoot, catalog.PCI)
	var recommendations []moduleRecommendation
	for _, u := range catalog.Unclaimed(devices) {
		if len(u.Candidates) == 0 {
			continue
		}
		rec := moduleRecommendation{
			Device: describeUnclaimed(u) + " (modalias " + u.Modalias + ")",
			Chain:  softdepChain(base, u.Candidates[0]),
		}
		// Unclaimed reports one entry per fingerprint, so every
		// device that shares this modalias belongs to this
		// recommendation.
		for _, d := range devices {
			if d.Modalias == u.Modalias {
				rec.SysfsDirs = append(rec.SysfsDirs,
					filepath.Join(sysfsRoot, "bus", d.Bus, "devices", d.Address))
			}
		}
		recommendations = append(recommendations, rec)
	}
	return recommendations
}

// withoutStickRecommendations drops the recommendations that exist
// only to reach the installation stick. The stick's own controller
// wants a driver like any unclaimed device, and the report rightly
// loaded it, because without it there is no stick to write the
// proposal to. But the stick leaves the machine with the person, so
// its driver does not belong in the machine's manifest. A device
// serves the stick when the stick's disk sits below it in the sysfs
// device tree; a recommendation is dropped only when every device
// that named it serves the stick, so a second, permanent disk on the
// same kind of controller keeps its driver recommended.
func withoutStickRecommendations(recs []moduleRecommendation, stick string) []moduleRecommendation {
	if stick == "" {
		return recs
	}
	stickReal, err := filepath.EvalSymlinks(filepath.Join(sysBlock, stick))
	if err != nil {
		return recs
	}
	var kept []moduleRecommendation
	for _, rec := range recs {
		serves := len(rec.SysfsDirs) > 0
		for _, dir := range rec.SysfsDirs {
			real, err := filepath.EvalSymlinks(dir)
			if err != nil || !strings.HasPrefix(stickReal+"/", real+"/") {
				serves = false
				break
			}
		}
		if !serves {
			kept = append(kept, rec)
		}
	}
	return kept
}

// readReportDisks lists every disk, in the form the proposal records.
// It reuses the same sysfs walk the world report uses, and adds the
// transport, which a person reads to recognize a disk by how it
// attaches. The excluded name is the installation stick's own disk,
// which belongs to the person, not the machine.
func readReportDisks(exclude string) []reportDisk {
	var disks []reportDisk
	for _, d := range discoverBlockDevices() {
		if d.Name == exclude {
			continue
		}
		disks = append(disks, reportDisk{
			Path:      devicePath(d),
			SizeBytes: d.SizeBytes,
			Model:     d.Model,
			Transport: diskTransport(d.Name),
		})
	}
	return disks
}

// diskTransport names the bus that carries a disk, read from the disk's
// place in the sysfs device tree. The tree's path names every bus the
// device hangs off, so a SATA disk's path passes through an ata node, an
// NVMe disk's through nvme, and so on. This is the same information udev
// derives its ID_BUS from, read straight from the one file the kernel
// already maintains.
func diskTransport(name string) string {
	real, err := filepath.EvalSymlinks(filepath.Join(sysBlock, name))
	if err != nil {
		return ""
	}
	// The order matters only where a path could match more than one
	// word. A disk reached through libata shows both an ata node and the
	// SCSI layer that libata presents it through, and "sata" is the
	// answer a person wants.
	for _, bus := range []struct{ marker, transport string }{
		{"/nvme", "nvme"},
		{"/ata", "sata"},
		{"/usb", "usb"},
		{"/virtio", "virtio"},
	} {
		if strings.Contains(real, bus.marker) {
			return bus.transport
		}
	}
	return ""
}

// observeInterfaces brings every real interface admin-up and reads back
// its link state. Admin-up is required before the kernel trains the link
// and learns the carrier, and it changes only kernel state in RAM, so
// the report keeps its promise here too. The report waits a few seconds
// after raising the links, because copper autonegotiation takes that
// long, and a carrier read before the link trains would report every
// port dark.
func observeInterfaces() []reportInterface {
	links, err := netlink.LinkList()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: report: listing interfaces: %v\n", err)
		return nil
	}

	var raised []netlink.Link
	for _, link := range links {
		attrs := link.Attrs()
		// Loopback is not hardware, and a link with no MAC is a virtual
		// device the report has nothing to say about.
		if attrs.Flags&net.FlagLoopback != 0 || len(attrs.HardwareAddr) == 0 {
			continue
		}
		if err := netlink.LinkSetUp(link); err != nil {
			fmt.Fprintf(os.Stderr, "liken: report: raising %s: %v\n", attrs.Name, err)
		}
		raised = append(raised, link)
	}
	if len(raised) > 0 {
		time.Sleep(3 * time.Second)
	}

	var interfaces []reportInterface
	for _, link := range raised {
		attrs := link.Attrs()
		interfaces = append(interfaces, reportInterface{
			Name: attrs.Name,
			MAC:  attrs.HardwareAddr.String(),
			Link: linkState(attrs.Name),
		})
	}
	return interfaces
}

// linkState reads the kernel's word for an interface's link: up, down,
// or unknown. operstate is the kernel's own summary of the carrier, in
// the form a person reads most easily.
func linkState(name string) string {
	if state := sysfsString(filepath.Join("/sys/class/net", name), "operstate"); state != "" {
		return state
	}
	return "unknown"
}

// quiesceHardware waits for the bus probe to go quiet, so a walk of
// sysfs reads a settled machine rather than one mid-enumeration. It
// reuses the boot's own settle helper over the kernel's uevent socket:
// each device that arrives or binds a driver announces itself, and the
// wait ends once a full second passes with no such announcement, or a
// ceiling passes either way. Without the socket it falls back to a fixed
// pause, so a probe still has a moment to finish.
func quiesceHardware() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	uevents, err := hardware.ListenForUevents(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: report: no uevent socket, pausing instead: %v\n", err)
		time.Sleep(3 * time.Second)
		return
	}
	settle(ctx, uevents, time.Second, 10*time.Second)
}

// writeReportToStick writes the proposal to the root of the
// installation stick's filesystem. The stick is a FAT volume, found by
// the same GPT name image/stick.go writes on it. The report mounts it
// read-write only for this write, writes durably (FAT has no journal, so
// the sync before the rename is what makes the file whole), syncs, and
// unmounts, so the filesystem is clean when the person pulls the stick.
func writeReportToStick(proposal string) error {
	device, err := findInstallStick()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(reportStickMount, 0o755); err != nil {
		return err
	}
	if err := unix.Mount(device, reportStickMount, "vfat", 0, ""); err != nil {
		return fmt.Errorf("mounting the stick %s: %w", device, err)
	}

	writeErr := writeFileDurably(filepath.Join(reportStickMount, hardwareReportName), []byte(proposal))
	unix.Sync()

	// A plain unmount flushes and detaches the filesystem cleanly. If it
	// is busy, a lazy detach at least releases it, so a later boot does
	// not find a stale mount.
	if err := unix.Unmount(reportStickMount, 0); err != nil {
		_ = unix.Unmount(reportStickMount, unix.MNT_DETACH)
	}
	return writeErr
}

// installStickDiskName names the disk that carries the installation
// stick's filesystem, so the report can leave that disk out of its
// proposal. An absent or ambiguous stick returns "", which excludes
// nothing: better to list one disk too many than to hide a real one.
func installStickDiskName() string {
	disk := ""
	for _, p := range discoverPartitions() {
		if p.partName != stickInstallPartition {
			continue
		}
		if disk != "" {
			return ""
		}
		disk = p.disk
	}
	return disk
}

// findInstallStick names the device that holds the installation stick's
// filesystem. It recognizes the stick the same way liken recognizes
// everything on a disk: by the GPT partition name, not by the device
// path, which changes with enumeration. Two partitions with the name is
// an ambiguity the report refuses to guess through.
func findInstallStick() (string, error) {
	var device string
	for _, p := range discoverPartitions() {
		if p.partName != stickInstallPartition {
			continue
		}
		if device != "" {
			return "", fmt.Errorf("two partitions carry %s; refusing to guess which is the stick", stickInstallPartition)
		}
		device = devRoot + "/" + p.name
	}
	if device == "" {
		return "", fmt.Errorf("no installation stick found (no partition named %s)", stickInstallPartition)
	}
	return device, nil
}

// rebootAfterReport restarts the machine. A report boot has no k3s and
// no role mounts to tear down, so this is the plain restart syscall
// after a sync, not the supervisor's full shutdown. The report reboots
// rather than powers off, because the machine's next boot is the person
// walking the install once they have edited the manifest. Like every
// terminal path in init, it never lets PID 1 exit.
func rebootAfterReport() {
	syncLogs()
	unix.Sync()
	if err := unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART); err != nil {
		fmt.Fprintf(os.Stderr, "liken: report: reboot failed: %v\n", err)
	}
	for {
		time.Sleep(time.Hour)
	}
}
