package main

// This file writes the hardware report's proposal: a complete Machine
// manifest for a new machine, with the evidence for each line in
// comments beside it. A person renames the machine, checks the sizes,
// and installs from it, so the proposal declares only what an install
// can use:
//
//   - A storage driver is not declared in spec.modules (see
//     storageClass). The proposal warns about it instead.
//   - A role is not given a size that its disk cannot hold
//     (reportlayout.go does that arithmetic).
//   - A network port with no carrier is listed as a comment, not
//     declared.
//
// composeHardwareReport is a pure function of a hardwareReport value.
// It reads no sysfs, netlink, or disk, so a test can build the whole
// document from made-up hardware. report.go loads the drivers, reads
// the hardware, and fills in the hardwareReport.

import (
	"fmt"
	"strings"
)

// reportDisk is one disk that the report found: its kernel name, device
// path, size, model when the bus reports one, and transport (sata,
// nvme, usb, virtio). A person uses the model and transport to
// recognize the disk.
//
// StableName is the disk's /dev/disk/by-id name, when it has one. The
// proposal declares a role by this name instead of Path, because the
// kernel can assign the same Path to a different disk on a later boot.
//
// BehindModules lists the drivers that had to load before this disk
// appeared. An install from a stock image does not load them, so it
// does not find the disk. MaybeStick is true when more than one disk has
// an installation partition and this disk could be the stick.
type reportDisk struct {
	Name          string
	Path          string
	SizeBytes     uint64
	Model         string
	Transport     string
	StableName    string
	BehindModules []string
	MaybeStick    bool
}

// deviceName returns the value a proposal declares for this disk: its
// stable name when it has one, its kernel device path otherwise.
func (d reportDisk) deviceName() string {
	if d.StableName != "" {
		return d.StableName
	}
	return d.Path
}

// reportInterface is one network interface as the report found it
// after it loaded the recommended drivers and set the link up. Link is
// the kernel's operstate for the interface, which shows whether a
// cable is connected.
type reportInterface struct {
	Name string
	MAC  string
	Link string
}

// moduleRecommendation pairs one device that has no driver with the
// drivers that would bind it. Chain lists them in load order with
// their softdeps expanded, so a NIC that needs its PHY library first
// is [realtek, r8169] and not just r8169. Device describes the
// hardware in words for the proposal's comments. Class is the
// device's class, which storageClass reads. SysfsDirs lists the sysfs
// directories of the matching devices. This file does not read it.
// report.go uses it to tell which drivers bind the machine's own
// devices and which bind only the install stick, and which disks
// appeared only because a driver loaded.
type moduleRecommendation struct {
	Device    string
	Class     string
	Chain     []string
	SysfsDirs []string
}

// storageClass reports whether this recommendation is a storage driver.
// A storage driver cannot go in spec.modules, because init loads the
// declared modules after it has claimed and mounted the disks that
// the manifest names. A disk behind that driver does not exist yet
// when init looks for it, so the driver has to be in the image's boot
// modules.
func (r moduleRecommendation) storageClass() bool {
	return r.Class == "storage" || r.Class == "mass-storage"
}

// hardwareReport is all the hardware the report found, as the input of
// composeHardwareReport. report.go fills it from the machine, and a
// test fills it by hand.
type hardwareReport struct {
	// UEFI is true when UEFI firmware booted this machine. A UEFI
	// machine keeps its boot entries in firmware and needs no biosBoot
	// or bootHome role. A BIOS machine needs both, so the proposal
	// declares them.
	UEFI bool
	// StickPath is the device path of the install stick, when the
	// report identified it. The stick is not in Disks and gets no
	// role, because the person removes it after the install. The
	// proposal still lists it, so the person can match every disk in
	// the machine to a line.
	StickPath       string
	Recommendations []moduleRecommendation
	// Claimable is the hardware with no driver, other than storage and
	// network devices. The report does not load these drivers, and the
	// proposal lists them only as comments (see composeClaimable).
	Claimable []moduleRecommendation
	Disks     []reportDisk
	// Cards is every memory card that the report found in a slot. Like
	// the stick, a card is not in Disks and gets no role, and the
	// proposal still lists it.
	Cards      []reportDisk
	Interfaces []reportInterface
}

const reportHeader = `# A proposed Machine manifest, written by the liken hardware report.
#
# This machine booted the report entry from the install stick. The
# report loaded the drivers this hardware names, watched which disks and
# interfaces appeared, and wrote its findings here. It changed nothing
# on the machine.
#
# Read this file, edit the parts marked CHANGE-ME or "size to ...", and
# use it as the machine's manifest in your deployment layer. Then boot
# the install entry for this machine. The comments beside each field are
# the evidence the report gathered; delete them once you have read them.
`

// composeHardwareReport renders the proposal for r. The result is a
// valid Machine manifest whose storage roles fit the disks that r
// lists.
func composeHardwareReport(r hardwareReport) string {
	var b strings.Builder
	b.WriteString(reportHeader)
	b.WriteString("apiVersion: liken.sh/v1alpha1\n")
	b.WriteString("kind: Machine\n")
	b.WriteString("metadata:\n")
	b.WriteString("  # The name this machine has in your deployment. Boot entries\n")
	b.WriteString("  # pass it as liken.machine=, so it must match a name the\n")
	b.WriteString("  # stick's menu offers.\n")
	b.WriteString("  name: CHANGE-ME\n")
	b.WriteString("spec:\n")
	composeModules(&b, r.Recommendations, r.Claimable)
	composeNetwork(&b, r.Interfaces)
	composeStorage(&b, r)
	return b.String()
}

// composeModules writes the spec.modules section, with a comment for
// each device that needs a driver. The declared list is the chains of
// the recommendations, in order, with each module once. A storage
// chain is left out of the list and gets a warning instead, for the
// reason given at storageClass.
func composeModules(b *strings.Builder, recs, claimable []moduleRecommendation) {
	b.WriteString("  # Extra kernel modules this machine's hardware requires, beyond\n")
	b.WriteString("  # the drivers the OS already loads. Each comment names a device\n")
	b.WriteString("  # with no driver bound and the modules that would bind it, in\n")
	b.WriteString("  # the order to load them. The report looks at storage and\n")
	b.WriteString("  # network devices only: a machine needs those two kinds to\n")
	b.WriteString("  # install itself and join a cluster, and loading anything else\n")
	b.WriteString("  # would change the machine a person is standing in front of.\n")
	for _, rec := range recs {
		fmt.Fprintf(b, "  #   %s: %s\n", rec.Device, strings.Join(rec.Chain, ", then "))
	}

	var declarable []moduleRecommendation
	var bootPath []moduleRecommendation
	for _, rec := range recs {
		if rec.storageClass() {
			bootPath = append(bootPath, rec)
		} else {
			declarable = append(declarable, rec)
		}
	}
	for _, rec := range bootPath {
		writeComment(b, "  ", fmt.Sprintf(
			"WARNING: %s is a storage controller, and spec.modules cannot load its driver in time. init loads the declared modules after it claims the disks, so a disk behind %s does not exist when the disks are claimed. Build a liken image whose image/boot-modules.conf names %s, and install this machine from that image.",
			rec.Device, strings.Join(rec.Chain, " and "), strings.Join(rec.Chain, " and ")))
	}

	if len(declarable) == 0 {
		b.WriteString("  # This report found no unclaimed storage or network device\n")
		b.WriteString("  # whose driver a manifest can load. If an interface you\n")
		b.WriteString("  # expect is missing below, its controller may need a driver\n")
		b.WriteString("  # this image does not include.\n")
		b.WriteString("  modules: []\n")
		composeClaimable(b, claimable)
		return
	}
	b.WriteString("  modules:\n")
	for _, name := range dedupChains(declarable) {
		fmt.Fprintf(b, "    - %s\n", name)
	}
	composeClaimable(b, claimable)
}

// composeClaimable lists the hardware that has no driver as commented
// module lines that a person can uncomment.
//
// The report loads only storage and network drivers, because another
// driver can change the machine while the person uses it: a display
// driver changes the screen that shows the report. The proposal still
// lists the hardware, so a person learns that the machine has a GPU
// and can choose to load its driver.
func composeClaimable(b *strings.Builder, claimable []moduleRecommendation) {
	if len(claimable) == 0 {
		return
	}
	b.WriteString("  # This machine also has hardware that nothing drives. The\n")
	b.WriteString("  # install does not need it, so the report left it alone.\n")
	b.WriteString("  # Uncomment a line to load its driver, and the machine\n")
	b.WriteString("  # publishes the device for workloads to claim. A device with\n")
	b.WriteString("  # no driver is not claimable, and appears in the node's\n")
	b.WriteString("  # unclaimed hardware instead.\n")
	for _, rec := range claimable {
		writeComment(b, "    ", rec.Device)
		for _, name := range rec.Chain {
			fmt.Fprintf(b, "    #- %s\n", name)
		}
	}
}

// composeNetwork writes the spec.network section from the interfaces
// that the report found. The names are the kernel's names, because the
// report loaded the drivers before it listed the interfaces.
//
// Only ports with a carrier are declared. The others are written as
// commented entries with their link state. init configures the
// declared interfaces one after another and waits up to 30 seconds
// for a DHCP lease on each (acquireLease), so a declared port with no
// cable adds that wait to every boot. A person who connects a cable
// later uncomments one line and does not need to run the report
// again.
func composeNetwork(b *strings.Builder, ifaces []reportInterface) {
	b.WriteString("  # The network interfaces this report saw once the drivers above\n")
	b.WriteString("  # were loaded and each link was brought up. A name is real only\n")
	b.WriteString("  # after a driver binds the card, so these are the true names.\n")
	if len(ifaces) == 0 {
		b.WriteString("  # This report saw no interface. Its controller may need a\n")
		b.WriteString("  # driver this report could not name.\n")
		b.WriteString("  network: {}\n")
		return
	}

	var connected, unconnected []reportInterface
	for _, ifc := range ifaces {
		// The kernel reports "down" only when the driver tracks the
		// carrier and finds none. A driver that does not track it
		// reports "unknown", and the report declares such a port
		// because it cannot tell that the port is unused.
		if ifc.Link == "down" || ifc.Link == "lowerlayerdown" {
			unconnected = append(unconnected, ifc)
			continue
		}
		connected = append(connected, ifc)
	}

	if len(connected) == 0 {
		writeComment(b, "  ", "No port had a carrier when this report ran. This proposal declares no interface, so liken configures the first port it finds. Connect the cable you will use and run the report again, or declare the port yourself from the names below.")
		writeUnconnectedInterfaces(b, unconnected)
		b.WriteString("  network: {}\n")
		return
	}

	if len(unconnected) > 0 {
		writeComment(b, "  ", "Only the ports with a carrier are declared here. liken waits up to thirty seconds for a DHCP lease on each declared interface, one after another, so a declared port with no cable delays every boot by that much. The ports with no carrier follow the declared ones as comments. Uncomment a port after you connect its cable.")
	}
	b.WriteString("  network:\n")
	b.WriteString("    interfaces:\n")
	for i, ifc := range connected {
		fmt.Fprintf(b, "      # MAC %s, link %s\n", ifc.MAC, ifc.Link)
		if i == 0 {
			b.WriteString("      # This first interface uses DHCP. For a cluster segment\n")
			b.WriteString("      # with a fixed address, add an \"address: 10.0.0.N/24\" line.\n")
		}
		fmt.Fprintf(b, "      - name: %s\n", ifc.Name)
	}
	for _, ifc := range unconnected {
		fmt.Fprintf(b, "      # MAC %s, link %s\n", ifc.MAC, ifc.Link)
		fmt.Fprintf(b, "      #- name: %s\n", ifc.Name)
	}
}

// writeUnconnectedInterfaces lists the ports as comments, for a
// machine where no port had a carrier.
func writeUnconnectedInterfaces(b *strings.Builder, unconnected []reportInterface) {
	for _, ifc := range unconnected {
		fmt.Fprintf(b, "  #   - name: %s   # MAC %s, link %s\n", ifc.Name, ifc.MAC, ifc.Link)
	}
}

// composeStorage writes the spec.storage section: every disk as a
// comment, then the planner's notes, then the roles from
// planStorageLayout. The notes say which role holds containerd's
// images, which sizes the person should choose, and which role the
// planner left out because a disk was too small.
func composeStorage(b *strings.Builder, r hardwareReport) {
	b.WriteString("  # The disks this report saw. A role below names a disk by its\n")
	b.WriteString("  # stable name when it has one, because that name belongs to\n")
	b.WriteString("  # the disk itself and does not change when the disk moves to\n")
	b.WriteString("  # another port. A role names the kernel path only when the disk\n")
	b.WriteString("  # has no stable name, and the next boot can give that path to\n")
	b.WriteString("  # a different disk. The value matters only on the boot that\n")
	b.WriteString("  # claims the disk. After that, liken finds each role by the\n")
	b.WriteString("  # name that it writes into the GPT.\n")
	for _, d := range r.Disks {
		fmt.Fprintf(b, "  #   %s  %s  %s  (%s)%s%s\n",
			d.Path, gib(d.SizeBytes), orUnknown(d.Model), orUnknown(d.Transport), stableNameField(d), diskCaveat(d))
	}
	if r.StickPath != "" {
		fmt.Fprintf(b, "  #   (%s is the install stick. You take it away,\n", r.StickPath)
		b.WriteString("  #   so it is not listed and no role may be on it)\n")
	}
	for _, c := range r.Cards {
		fmt.Fprintf(b, "  #   (%s is a %s memory card in a card slot. You can take\n", c.Path, gib(c.SizeBytes))
		b.WriteString("  #   it away, so no role is proposed on it. Declare it yourself if\n")
		b.WriteString("  #   this card stays in this machine.)\n")
	}
	for _, warning := range storageWarnings(r) {
		writeComment(b, "  ", warning)
	}

	layout := planStorageLayout(r.Disks, r.UEFI)
	for _, note := range layout.Notes {
		writeComment(b, "  ", note)
	}
	if len(layout.Roles) == 0 {
		b.WriteString("  storage: {}\n")
		return
	}

	b.WriteString("  storage:\n")
	if r.UEFI {
		b.WriteString("    # This machine booted UEFI, so the firmware holds its boot\n")
		b.WriteString("    # entries and it needs no biosBoot or bootHome role.\n")
	} else {
		b.WriteString("    # This machine booted BIOS, so liken supplies the boot\n")
		b.WriteString("    # bookkeeping that UEFI firmware would otherwise hold.\n")
	}
	for _, role := range layout.Roles {
		writeRole(b, string(role.Name), role.Device, role.Size, role.Comment)
	}
}

// stableNameField returns the stable name to append to a disk's
// evidence line, or "" when the disk has none. It is the same name
// that a role's device field uses, so a person can match the roles to
// the disks.
func stableNameField(d reportDisk) string {
	if d.StableName == "" {
		return ""
	}
	return "  " + d.StableName
}

// diskCaveat returns the note to append to a disk's evidence line when
// the disk is a poor target for a role, or "" otherwise.
func diskCaveat(d reportDisk) string {
	switch {
	case d.MaybeStick:
		return "  <- this disk may be the install stick"
	case len(d.BehindModules) > 0:
		return "  <- needs " + strings.Join(d.BehindModules, " and ") + " in the image's boot modules"
	}
	return ""
}

// storageWarnings returns a warning for each problem that keeps the
// proposal from installing as written and that the manifest does not
// show: disks that appeared only after the report loaded a driver, and
// disks that might be the install stick.
func storageWarnings(r hardwareReport) []string {
	var warnings []string
	var behind, maybe []string
	for _, d := range r.Disks {
		if len(d.BehindModules) > 0 {
			behind = append(behind, fmt.Sprintf("%s (%s)", d.Path, strings.Join(d.BehindModules, " and ")))
		}
		if d.MaybeStick {
			maybe = append(maybe, d.Path)
		}
	}
	if len(behind) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"WARNING: these disks appeared only after this report loaded a driver: %s. An install claims its disks before it loads any module a manifest declares, so an install from a stock image never sees them. Build a liken image whose image/boot-modules.conf names those modules, and install this machine from that image.",
			strings.Join(behind, ", ")))
	}
	if len(maybe) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"WARNING: more than one disk has an installation partition, so this report cannot tell which disk is the stick you booted from. It placed no role on %s. Remove the other stick and run this report again, or name the disks yourself.",
			strings.Join(maybe, " or ")))
	}
	return warnings
}

// reportWarnings returns the same warnings as console lines, plus a
// warning for each storage driver and one when no disk can hold an
// install. A person who reads the proposal on screen can scroll past
// the comments in it, so the console repeats the problems that stop
// an install.
func reportWarnings(r hardwareReport) []string {
	var lines []string
	for _, warning := range storageWarnings(r) {
		lines = append(lines, "liken: report: "+warning)
	}
	for _, rec := range r.Recommendations {
		if rec.storageClass() {
			lines = append(lines, fmt.Sprintf(
				"liken: report: WARNING: %s needs %s, and spec.modules cannot load a storage driver before the disks are claimed. Build an image whose image/boot-modules.conf lists it.",
				rec.Device, strings.Join(rec.Chain, " and ")))
		}
	}
	if len(planStorageLayout(r.Disks, r.UEFI).Roles) == 0 {
		lines = append(lines, "liken: report: WARNING: no disk here can hold a liken install, so the proposal declares no storage.")
	}
	return lines
}

// writeRole renders one storage role. An empty size means the role
// takes the rest of its disk, which the spec allows for one role per
// disk. A non-empty comment follows the size on the same line.
func writeRole(b *strings.Builder, name, device, size, comment string) {
	fmt.Fprintf(b, "    %s:\n", name)
	fmt.Fprintf(b, "      device: %s\n", device)
	switch {
	case size != "" && comment != "":
		fmt.Fprintf(b, "      size: %s  %s\n", size, comment)
	case size != "":
		fmt.Fprintf(b, "      size: %s\n", size)
	case comment != "":
		fmt.Fprintf(b, "      %s\n", comment)
	}
}

// writeComment writes text as YAML comment lines, wrapped at 64
// characters after the indent so that they fit on the console.
func writeComment(b *strings.Builder, indent, text string) {
	const width = 64
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) <= width:
			line += " " + word
		default:
			fmt.Fprintf(b, "%s# %s\n", indent, line)
			line = word
		}
	}
	if line != "" {
		fmt.Fprintf(b, "%s# %s\n", indent, line)
	}
}

// dedupChains joins the chains of recs into one module list in order,
// keeping the first occurrence of each module name.
func dedupChains(recs []moduleRecommendation) []string {
	seen := map[string]bool{}
	var out []string
	for _, rec := range recs {
		for _, name := range rec.Chain {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// orUnknown returns value, or "unknown" when the bus reported nothing,
// so that a missing attribute does not leave a gap in the disk line.
func orUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
