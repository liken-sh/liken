package main

// A hardware report boot describes the machine's hardware and changes
// nothing on its disks.
//
// A new machine needs a manifest that names its drivers, interface
// names, and disk paths. The lab guests cannot show them, because the
// kernel builds the virtio drivers in, and real controllers and NICs
// need modules. So before the first install, a person picks "liken
// hardware report" from the install stick's menu. That entry boots with
// liken.report and no liken.machine=, and the report does these steps:
//
//  1. It mounts the payload's system image (liken.sqfs on the stick),
//     because boot.cpio has only the boot modules, and the report needs
//     the full module tree, its alias table, and its softdeps.
//  2. It loads drivers for the storage, network, and USB mass-storage
//     devices that have none. Names such as eth0 exist only after a
//     driver binds.
//  3. It reads the disks and the interfaces.
//  4. It writes a proposed manifest to the stick, prints it and its
//     warnings, and reboots. On an attended boot it waits for Enter
//     first.
//
// The proposal is meant to install after the person renames the
// machine and checks the sizes. It sizes the storage roles to the
// measured disks. It leaves storage drivers out of spec.modules,
// because declared modules load after the boot claims disks, and warns
// that the image's boot modules must include them. It declares only the
// ports with a carrier, because each declared port adds a DHCP wait to
// every boot.
//
// main runs the report before settleStorage, so the report never
// claims, formats, or writes a disk of the machine.

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

	"github.com/liken-sh/liken/liken/hardware"
)

// stickCeiling is how long the report waits for the install stick's
// disk to appear. The report writes the proposal to the stick, and no
// role may use the stick's disk.
const stickCeiling = 15 * time.Second

// reportParam is the kernel command-line word that selects the report
// boot. Unlike installParam and reinstallParam (reinstall.go), it never
// writes to a disk of the machine.
const reportParam = "liken.report"

// stickInstallPartition is the GPT partition name that image/stick.go
// writes on the install stick. The report finds the stick by this name.
const stickInstallPartition = "liken:install"

// hardwareReportName is the proposal's file name at the root of the
// stick's filesystem. A person edits this file and uses it as the
// machine's manifest.
const hardwareReportName = "hardware-report.yaml"

// reportImageMount is where the report loop-mounts the payload's system
// image, and reportStickMount is where it mounts the stick's
// filesystem. Tests set them to temporary directories.
var (
	reportImageMount = "/liken-report-image"
	reportStickMount = "/liken-report-stick"
)

func reporting() bool {
	return bootParam(reportParam)
}

// runHardwareReport gathers the hardware, writes the proposal to the
// stick, prints it, and reboots. It never returns.
func runHardwareReport() {
	report, stick := gatherHardwareReport()
	proposal := composeHardwareReport(report)

	writeErr := writeReportToStick(stick, proposal)

	// The printed proposal is the only copy when the stick write fails.
	fmt.Println(proposal)

	// The proposal is long, so the warnings print after it, next to the
	// prompt, where the person sees them.
	for _, line := range reportWarnings(report) {
		fmt.Println(line)
	}

	if writeErr != nil {
		fmt.Fprintf(os.Stderr, "liken: report: writing to the stick: %v\n", writeErr)
	}
	holdInstallerConsole(reportPrompt(writeErr), false)
	endReport()
}

// endReport ends the report boot. Tests replace it because the real
// one reboots the machine.
var endReport = rebootAfterReport

// reportPrompt returns the last console line, which says whether the
// proposal reached the stick.
func reportPrompt(writeErr error) string {
	if writeErr != nil {
		return fmt.Sprintf(
			"liken: writing %s to the stick failed, so the text above is the only copy. Press Enter to reboot.",
			hardwareReportName)
	}
	return fmt.Sprintf(
		"liken: wrote this report to the stick as %s. Press Enter to reboot.",
		hardwareReportName)
}

// gatherHardwareReport loads the drivers the hardware needs and reads
// the disks and interfaces that appear. It also returns the stick, so
// the caller writes the proposal to it. When the payload image does not
// mount, the report has only the disks and interfaces that the boot
// modules' drivers bound.
func gatherHardwareReport() (hardwareReport, installStick) {
	uefi := firmwareIsUEFI()

	// A SATA link can still be training, or a USB device still
	// enumerating, when the boot starts, and a disk that has not
	// appeared yet looks absent.
	quiesceHardware()

	var recommendations, claimable []moduleRecommendation
	base, pciIDs, unmount, err := mountPayloadModules()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: report: %v\n", err)
	} else {
		defer unmount()
		catalog, err := hardware.LoadCatalog(base, pciIDs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: report: the proposal has no driver recommendations because the hardware catalog did not load: %v\n", err)
		} else {
			recommendations = recommendModules(catalog, base)
			// Read before the loads below, so the list has the devices
			// that had no driver at boot.
			claimable = recommendClaimable(catalog, base)
			// The disks and interfaces of these drivers appear only after
			// the loads finish and probing settles again.
			loadDeclaredModulesFrom(base, dedupChains(recommendations), nil)
			quiesceHardware()
		}
	}

	// The proposal must not give the stick a role, because the person
	// removes the stick after the install, and a reinstall would wipe
	// it. The stick has no block device until usb-storage or uas loads,
	// so it can be found only here. The report keeps that driver loaded
	// to write the file, but leaves it out of the recommendations.
	stick := awaitInstallStick()
	recommendations = withoutStickRecommendations(recommendations, stick.Disk)

	// A disk that appeared only because the report loaded its driver is
	// invisible to an install unless the image's boot modules include
	// that driver. The proposal warns about each such disk.
	measured, cards := readReportDisks(stick)
	disks := markDisksBehindDrivers(measured, recommendations)

	return hardwareReport{
		UEFI:            uefi,
		StickPath:       stick.Path,
		Recommendations: recommendations,
		Claimable:       claimable,
		Disks:           disks,
		Cards:           cards,
		Interfaces:      observeInterfaces(),
	}, stick
}

// mountPayloadModules loop-mounts the payload's system image and
// returns the paths of its kernel module tree and its PCI names
// database.
func mountPayloadModules() (base, pciIDs string, unmount func(), err error) {
	image := filepath.Join(releasePayloadDir, slotImageName)
	if err := mountReportImage(image, reportImageMount); err != nil {
		return "", "", nil, fmt.Errorf("mounting the payload's system image %s: %w", image, err)
	}
	base = filepath.Join(reportImageMount, "lib/modules", kernelRelease())
	pciIDs = filepath.Join(reportImageMount, "usr/share/hwdata/pci.ids")
	unmount = func() { _ = unix.Unmount(reportImageMount, unix.MNT_DETACH) }
	return base, pciIDs, unmount, nil
}

// mountReportImage loop-mounts the payload's system image. Tests
// replace it because a loop mount needs privileges.
var mountReportImage = loopMount

// recommendModules returns a driver recommendation for each storage,
// network, and USB mass-storage device that has no driver.
func recommendModules(catalog *hardware.Catalog, base string) []moduleRecommendation {
	return recommendFor(catalog, base, reportableClass)
}

// recommendClaimable returns a recommendation for each device with no
// driver that a workload could claim once a driver binds, such as a
// GPU. The report does not load these drivers. The proposal lists them
// so the person learns what the machine has.
func recommendClaimable(catalog *hardware.Catalog, base string) []moduleRecommendation {
	return recommendFor(catalog, base, claimableClass)
}

// recommendFor returns one recommendation for each modalias of a
// device with no driver whose class want accepts. Chain is the first
// candidate in modules.alias order with its soft dependencies, so a NIC
// that needs its PHY library first gets [realtek, r8169]. SysfsDirs
// lists every device with the modalias, so two identical NICs give one
// recommendation with both paths. Later steps read SysfsDirs to find
// the recommendations that serve only the stick and the disks that
// exist only because of a load.
func recommendFor(catalog *hardware.Catalog, base string, want func(string) bool) []moduleRecommendation {
	devices := hardware.DiscoverDevices(sysfsRoot, catalog.PCI)
	seen := map[string]bool{}
	var recommendations []moduleRecommendation
	for _, u := range catalog.Unclaimed(devices, nil) {
		if len(u.Candidates) == 0 || seen[u.Modalias] || !want(u.Class) {
			continue
		}
		seen[u.Modalias] = true
		rec := moduleRecommendation{
			Device: describeUnclaimed(u) + " (modalias " + u.Modalias + ")",
			Class:  u.Class,
			Chain:  driverChain(base, u.Candidates[0]),
		}
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

// reportableClass reports whether the report loads a driver for a
// device of this class. A machine needs storage and network to install
// and join a cluster, and the report needs USB mass storage to reach
// the stick. Storage includes PCI subclass 0805, the SD and eMMC host
// controllers.
//
// The report loads no other driver, because a driver can change the
// running machine. A display driver takes over the framebuffer console
// and switches its mode while the person reads the proposal. An
// installed machine lists these devices in status.hardware.unclaimed.
func reportableClass(class string) bool {
	switch class {
	case "storage", "network", "mass-storage":
		return true
	}
	return false
}

// claimableClass reports whether the report lists a device of this
// class as claimable. It accepts every class except the ones that
// reportableClass accepts and the ones whose drivers give a pod no
// device: bridges, memory controllers, and system devices. The person
// decides which of the listed drivers to load.
func claimableClass(class string) bool {
	switch class {
	case "", "storage", "network", "mass-storage", "bridge", "memory", "system":
		return false
	}
	return true
}

// observeInterfaces sets every physical interface up and returns its
// name, MAC address, and link state. The kernel detects the carrier
// only on an interface that is up. It waits 3 seconds for copper
// autonegotiation to finish, because an earlier read reports every
// port as down.
func observeInterfaces() []reportInterface {
	links, err := listLinks()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: report: listing interfaces: %v\n", err)
		return nil
	}

	var raised []netlink.Link
	for _, link := range links {
		attrs := link.Attrs()
		// Skip loopback and virtual devices, which have no MAC address.
		if attrs.Flags&net.FlagLoopback != 0 || len(attrs.HardwareAddr) == 0 {
			continue
		}
		if err := raiseLink(link); err != nil {
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

// listLinks and raiseLink list the machine's interfaces and set one
// up. Tests replace them because netlink calls need privileges.
var (
	listLinks = netlink.LinkList
	raiseLink = netlink.LinkSetUp
)

// linkState returns the interface's operstate from sysfs, or "unknown"
// when sysfs has no value.
func linkState(name string) string {
	if state := sysfsString(filepath.Join(sysfsRoot, "class/net", name), "operstate"); state != "" {
		return state
	}
	return "unknown"
}

// quiesceHardware waits until device probing is done: 1 second with no
// uevent, or 10 seconds in total. When the uevent socket does not open,
// or its listener stops during the wait, which makes Settle return at
// once, it sleeps for quiescePause instead.
func quiesceHardware() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	uevents, err := listenForUevents(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: report: the uevent socket did not open, so the report pauses instead of waiting for uevents: %v\n", err)
		time.Sleep(quiescePause)
		return
	}
	hardware.Settle(ctx, uevents, time.Second, 10*time.Second)
	if listenerStopped(uevents) {
		fmt.Fprintln(os.Stderr, "liken: report: the uevent listener stopped, so the report pauses instead of waiting for uevents")
		time.Sleep(quiescePause)
	}
}

const quiescePause = 3 * time.Second

// listenerStopped reports whether the uevent channel is closed. On an
// open channel it discards a pending uevent, which the caller no longer
// needs.
func listenerStopped(uevents <-chan struct{}) bool {
	select {
	case _, ok := <-uevents:
		return !ok
	default:
		return false
	}
}

// writeReportToStick writes the proposal to the root of the stick's FAT
// volume. FAT has no journal, so the file is synced before its rename,
// and the volume is synced and unmounted, so the filesystem is clean
// when the person removes the stick.
func writeReportToStick(stick installStick, proposal string) error {
	device := stick.Partition
	if device == "" {
		if stick.ambiguous() {
			return fmt.Errorf("more than one disk has a partition named %s (%s), so the report cannot tell which disk is the stick",
				stickInstallPartition, strings.Join(stick.Candidates, ", "))
		}
		return fmt.Errorf("no disk has a partition named %s, so the report found no install stick", stickInstallPartition)
	}

	if err := os.MkdirAll(reportStickMount, 0o755); err != nil {
		return err
	}
	if err := mountStick(device, reportStickMount); err != nil {
		return fmt.Errorf("mounting the stick %s: %w", device, err)
	}

	writeErr := writeFileDurably(filepath.Join(reportStickMount, hardwareReportName), []byte(proposal))
	unix.Sync()

	// A lazy detach releases a mount that is still busy.
	if err := unmountStick(reportStickMount, 0); err != nil {
		_ = unmountStick(reportStickMount, unix.MNT_DETACH)
	}
	return writeErr
}

// mountStick and unmountStick mount and unmount the stick's FAT
// volume. Tests replace them because mounting needs privileges.
var (
	mountStick = func(device, target string) error {
		return unix.Mount(device, target, "vfat", 0, "")
	}
	unmountStick = unix.Unmount
)

// rebootAfterReport restarts the machine with the reboot syscall. A
// report boot runs no k3s and mounts no roles, so it skips the
// supervisor's shutdown. It reboots instead of powering off because the
// next boot is the install. If the syscall fails it sleeps forever,
// because PID 1 must not exit.
func rebootAfterReport() {
	syncLogs()
	unix.Sync()
	if err := unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART); err != nil {
		fmt.Fprintf(os.Stderr, "liken: report: the reboot call returned an error: %v\n", err)
	}
	for {
		time.Sleep(time.Hour)
	}
}
