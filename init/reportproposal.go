package main

// Composing the hardware report's proposal.
//
// The report boot answers the question a new machine asks before its
// first install: what does this hardware need in its manifest? It
// answers with a whole Machine manifest, ready to edit, with the
// evidence for every line written beside it as a comment. This file
// turns the facts the boot gathered into that document.
//
// The proposal makes a promise: a person renames the machine, checks
// the sizes, and installs. Everything here serves that promise. A
// driver that the install cannot load in time is not declared, but
// named loudly instead. A size that a disk cannot hold is not written
// at all (reportlayout.go does that arithmetic). A port with no cable
// is evidence, not a declaration.
//
// The composition is a pure function. It takes the enumerated hardware
// as a plain value and returns the proposal text. Nothing here touches
// sysfs, netlink, or a disk, so a test drives the whole document from
// fabricated hardware, and the boot flow in report.go owns the messy
// half: mounting the module tree, loading drivers, and observing what
// appears.

import (
	"fmt"
	"strings"
)

// reportDisk is one disk as the report observed it: its kernel name
// and device path this boot, its size, its model when the bus
// publishes one, and the transport that carries it (sata, nvme, usb,
// virtio). The transport is evidence a person reads to recognize the
// disk; the device path is only a hint that matters on the boot that
// claims the disk.
//
// The last two fields carry what the report learned about the disk's
// place in an install, rather than about the disk itself.
// BehindModules names the drivers that had to load before this disk
// existed, which means an install from a stock image never sees it.
// MaybeStick marks a disk that could be the installation stick, on a
// machine where the report could not tell which one is.
type reportDisk struct {
	Name          string
	Path          string
	SizeBytes     uint64
	Model         string
	Transport     string
	BehindModules []string
	MaybeStick    bool
}

// reportInterface is one network interface as the report observed it,
// after it loaded the recommended drivers and brought the link
// admin-up. The name is real only because a driver bound the card:
// eth0 does not exist until its driver registers it. Link is the
// kernel's own word for the carrier, which a person reads to tell a
// connected port from a dark one.
//
// Driver is the module bound to the card. The proposal uses it to
// decide whether a name can identify a port at all: two ports on one
// driver are two ports of the same model, and only the order the
// kernel probed them puts one before the other.
type reportInterface struct {
	Name   string
	MAC    string
	Link   string
	Driver string
}

// moduleRecommendation pairs one unclaimed device with the ordered
// driver chain that would bind it. Chain is the softdep-expanded list,
// in load order, so a NIC that needs its PHY library first reads as
// [realtek, r8169], not r8169 alone. Device names the hardware in
// words, so the proposal's comment can say which device each driver
// claims. Class is the device's kind, which decides whether the chain
// can be declared at all: a network driver loads in time to serve the
// machine, and a storage driver does not. SysfsDirs is where the
// devices behind this fingerprint sit in sysfs; the composition
// ignores it, and the report boot uses it to tell a driver that
// serves the machine from one that serves only the installation
// stick, and to tell which disks appeared only because of a load.
type moduleRecommendation struct {
	Device    string
	Class     string
	Chain     []string
	SysfsDirs []string
}

// storageClass reports whether this recommendation drives storage.
// Such a chain never belongs in spec.modules. The declared modules
// load after storage settles, and storage settles by claiming and
// mounting the disks the manifest names, so a disk behind one of
// these drivers does not exist yet at the only moment it matters.
// The fix is an image that carries the driver in its boot modules,
// not a line in the manifest.
func (r moduleRecommendation) storageClass() bool {
	return r.Class == "storage" || r.Class == "mass-storage"
}

// hardwareReport is everything the report enumerated, in the form the
// composition consumes. It is a plain value on purpose: the boot fills
// it from real hardware, and a test fills it by hand, and both reach
// the same document through composeHardwareReport.
type hardwareReport struct {
	// UEFI records whether UEFI firmware booted this machine. A UEFI
	// machine keeps its boot entries in firmware and needs no
	// biosBoot/bootHome roles; a BIOS machine needs liken to supply
	// those roles, so the proposal declares them.
	UEFI bool
	// StickPath is the installation stick's own device path, when the
	// report found it among the disks. The stick leaves the machine
	// with the person, so it never appears in Disks and no role may
	// land on it; the proposal names it so a person counting their
	// disks finds every one accounted for.
	StickPath       string
	Recommendations []moduleRecommendation
	// Claimable is the hardware this machine has and does not drive,
	// beyond the disks and network ports the install needs. The report
	// never loads these drivers, and the proposal never declares them:
	// it names them in comments, because whether this machine should
	// drive its GPU is the operator's decision, and they cannot make it
	// if nothing tells them the GPU is there.
	Claimable  []moduleRecommendation
	Disks      []reportDisk
	Interfaces []reportInterface
}

const reportHeader = `# A proposed Machine manifest, written by the liken hardware report.
#
# This machine booted the report entry from the installation stick. The
# report loaded the drivers this hardware wants, watched which disks and
# interfaces appeared, and wrote its findings here. It changed nothing
# on the machine.
#
# Read this file, edit the parts marked CHANGE-ME or "size to ...", and
# use it as the machine's manifest in your deployment layer. Then boot
# the install entry for this machine. The comments beside each field are
# the evidence the report gathered; delete them once you have read them.
`

// composeHardwareReport renders the whole proposal from the enumerated
// hardware. The result is a valid Machine manifest whose roles fit the
// disks this machine has, so a person can install from it after only
// renaming the machine and checking the sizes.
func composeHardwareReport(r hardwareReport) string {
	var b strings.Builder
	b.WriteString(reportHeader)
	b.WriteString("apiVersion: liken.sh/v1alpha1\n")
	b.WriteString("kind: Machine\n")
	b.WriteString("metadata:\n")
	b.WriteString("  # The name this machine has in your deployment. Boot entries\n")
	b.WriteString("  # carry it as liken.machine=, so it must match a name the\n")
	b.WriteString("  # stick's menu offers.\n")
	b.WriteString("  name: CHANGE-ME\n")
	b.WriteString("spec:\n")
	composeModules(&b, r.Recommendations, r.Claimable)
	composeNetwork(&b, r.Interfaces)
	composeStorage(&b, r)
	return b.String()
}

// composeModules writes the spec.modules section: the drivers the
// report recommends, in load order, with one comment for each device
// that named them. The declared list is the deduplicated union of the
// chains a manifest can actually load, because a module named twice
// would ask the loader to load it twice. Storage chains are held back
// from the list and stated as a warning instead, for the reason
// storageClass explains.
func composeModules(b *strings.Builder, recs, claimable []moduleRecommendation) {
	b.WriteString("  # Extra kernel modules this machine's hardware wants, beyond\n")
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
			"WARNING: %s is a storage controller, and spec.modules cannot supply its driver. The declared modules load after storage settles, so a disk behind %s does not exist on the boot that claims disks. Build a liken image whose image/boot-modules.conf names %s, and install this machine from that image.",
			rec.Device, strings.Join(rec.Chain, " and "), strings.Join(rec.Chain, " and ")))
	}

	if len(declarable) == 0 {
		b.WriteString("  # This report found no unclaimed storage or network device\n")
		b.WriteString("  # whose driver a manifest can load. If an interface you\n")
		b.WriteString("  # expect is missing below, its controller may need a driver\n")
		b.WriteString("  # this image does not carry.\n")
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

// composeClaimable writes the hardware this machine has and does not
// drive, as lines a person uncomments.
//
// The report loads storage and network drivers and no others, because
// loading a driver changes the machine while a person stands in front
// of it, and a display driver changes the very screen they are
// reading. That limit is about loading, not about knowing. This
// machine's GPU is often the reason someone bought it, and a proposal
// that says nothing about it asks a person to already know what to ask
// for.
//
// The lines stay commented, so the proposal installs exactly as it
// reads, and driving this hardware stays a decision somebody makes.
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
// the report observed. The names are the real kernel names, because the
// report loaded the drivers before it read them.
//
// Only the ports with a carrier are declared, and the dark ports are
// written as commented-out entries with the link state the report
// read. The reason is the cost of a declaration: init configures each
// declared interface in turn, and waits up to thirty seconds for a
// DHCP lease on each one, so a declared port with no cable in it
// delays every boot of the machine by that much. The evidence for
// every port is still here, so a person who moves a cable uncomments
// one line instead of running the report again.
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

	byMAC := namesAreAmbiguous(ifaces)

	var connected, dark []reportInterface
	for _, ifc := range ifaces {
		// The kernel says "down" only when it knows the carrier is
		// absent. A driver that does not track the carrier reports
		// "unknown", and a port the report cannot judge is declared
		// rather than hidden.
		if ifc.Link == "down" || ifc.Link == "lowerlayerdown" {
			dark = append(dark, ifc)
			continue
		}
		connected = append(connected, ifc)
	}

	if len(connected) == 0 {
		writeComment(b, "  ", "No port had a carrier when this report ran. This proposal declares no interface, so liken configures the first port it finds. Connect the cable you will use and run the report again, or declare the port yourself from the names below.")
		writeDarkInterfaces(b, dark, byMAC)
		b.WriteString("  network: {}\n")
		return
	}

	if byMAC {
		writeComment(b, "  ", "This machine has more than one port on the same driver, so a kernel name cannot say which port is which: eth0 and eth1 are two cards of one model, in the order the kernel probed them. These ports are declared by MAC address instead, which belongs to the card and does not move. That ties this manifest to this machine, so a replacement card needs an edit here; run the report again and it reads the new address.")
	}
	if len(dark) > 0 {
		writeComment(b, "  ", "Only the ports with a carrier are declared here. liken waits up to thirty seconds for a DHCP lease on each declared interface, one after another, so a declared port with no cable delays every boot by that much. The dark ports follow the declared ones as comments; uncomment a port after you connect its cable.")
	}
	b.WriteString("  network:\n")
	b.WriteString("    interfaces:\n")
	for i, ifc := range connected {
		writeInterfaceEvidence(b, "      ", ifc, byMAC)
		if i == 0 {
			b.WriteString("      # This first interface uses DHCP. For a cluster segment\n")
			b.WriteString("      # with a fixed address, add an \"address: 10.0.0.N/24\" line.\n")
		}
		fmt.Fprintf(b, "      - %s\n", declareInterface(ifc, byMAC))
	}
	for _, ifc := range dark {
		writeInterfaceEvidence(b, "      ", ifc, byMAC)
		fmt.Fprintf(b, "      #- %s\n", declareInterface(ifc, byMAC))
	}
}

// namesAreAmbiguous reports whether this machine's kernel names can
// identify its ports. They cannot as soon as two ports share a
// driver: the driver is the card model, so two ports on one driver
// are interchangeable to everything except their addresses, and which
// of them the kernel calls eth0 follows the bus probe. Add a card,
// move a card, or update the firmware, and the two names can trade
// places with nothing in the manifest to notice.
//
// A port whose driver the report could not read counts for nothing
// here. Two unreadable drivers are not evidence of one shared driver.
func namesAreAmbiguous(ifaces []reportInterface) bool {
	seen := map[string]bool{}
	for _, ifc := range ifaces {
		if ifc.Driver == "" || ifc.MAC == "" {
			continue
		}
		if seen[ifc.Driver] {
			return true
		}
		seen[ifc.Driver] = true
	}
	return false
}

// usesMAC decides how one entry names its port. It follows the
// machine-wide verdict, except for a port whose address the report
// could not read: no address means no entry to write, so that port
// keeps its name whatever the rest of the machine does.
func usesMAC(ifc reportInterface, byMAC bool) bool {
	return byMAC && ifc.MAC != ""
}

// declareInterface writes the one field that says which port an entry
// means. A machine whose names are unambiguous keeps the name, so its
// manifest still describes any machine built the same way. A machine
// whose names are a guess gets the address instead.
func declareInterface(ifc reportInterface, byMAC bool) string {
	if usesMAC(ifc, byMAC) {
		return "mac: " + ifc.MAC
	}
	return "name: " + ifc.Name
}

// writeInterfaceEvidence writes the comment above one entry: the
// facts about the port that the declaration itself leaves out.
func writeInterfaceEvidence(b *strings.Builder, indent string, ifc reportInterface, byMAC bool) {
	fmt.Fprintf(b, "%s# %s, link %s\n", indent, interfaceAside(ifc, byMAC), ifc.Link)
}

func driverOrUnknown(ifc reportInterface) string {
	if ifc.Driver == "" {
		return "an unnamed driver"
	}
	return ifc.Driver
}

// writeDarkInterfaces lists the ports a person could declare, as
// comments, for the machine where no port had a carrier at all.
func writeDarkInterfaces(b *strings.Builder, dark []reportInterface, byMAC bool) {
	for _, ifc := range dark {
		fmt.Fprintf(b, "  #   - %s   # %s, link %s\n", declareInterface(ifc, byMAC), interfaceAside(ifc, byMAC), ifc.Link)
	}
}

// interfaceAside names the facts the declaration left out, so that
// every port in the proposal carries both its name and its address,
// whichever one the entry itself uses.
func interfaceAside(ifc reportInterface, byMAC bool) string {
	if usesMAC(ifc, byMAC) {
		return fmt.Sprintf("%s on %s", ifc.Name, driverOrUnknown(ifc))
	}
	return "MAC " + ifc.MAC
}

// composeStorage writes the spec.storage section. It first lists every
// disk the report saw as comments, the evidence a person matches
// against the machine in front of them, then writes the layout the
// planner fitted onto those disks, with the planner's notes above it.
// Those notes carry what the sizes alone cannot say: which role the
// machine sizes for itself because containerd's images live in it,
// which role is the operator's to choose, and what the planner had to
// give up on a small disk.
func composeStorage(b *strings.Builder, r hardwareReport) {
	b.WriteString("  # The disks this report saw. The device path is only a hint\n")
	b.WriteString("  # that matters on the boot that claims the disk; after that,\n")
	b.WriteString("  # liken finds each role by the name it writes into the GPT.\n")
	for _, d := range r.Disks {
		fmt.Fprintf(b, "  #   %s  %s  %s  (%s)%s\n",
			d.Path, gib(d.SizeBytes), orUnknown(d.Model), orUnknown(d.Transport), diskCaveat(d))
	}
	if r.StickPath != "" {
		fmt.Fprintf(b, "  #   (%s is the installation stick; it leaves with you,\n", r.StickPath)
		b.WriteString("  #   so it is not listed and no role may live on it)\n")
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

// diskCaveat is the short note that rides on a disk's evidence line
// when the disk is not an ordinary target for a role.
func diskCaveat(d reportDisk) string {
	switch {
	case d.MaybeStick:
		return "  <- this disk may be the installation stick"
	case len(d.BehindModules) > 0:
		return "  <- needs " + strings.Join(d.BehindModules, " and ") + " in the image's boot modules"
	}
	return ""
}

// storageWarnings states, in the proposal itself, the two facts that
// make a proposal uninstallable as written: a disk this boot could
// see but an install cannot, and a stick the report could not
// identify. Both are the operator's to resolve, and neither is
// visible from the manifest alone.
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
			"WARNING: more than one disk carries an installation partition, so this report cannot tell which disk is the stick you booted from. It placed no role on %s. Remove the other stick and run this report again, or name the disks yourself.",
			strings.Join(maybe, " or ")))
	}
	return warnings
}

// reportWarnings is the same news for the console. A person reads the
// proposal on screen once and takes the file away, so the facts that
// make the file uninstallable have to reach them at the machine, not
// only in the text they scroll past.
func reportWarnings(r hardwareReport) []string {
	var lines []string
	for _, warning := range storageWarnings(r) {
		lines = append(lines, "liken: report: "+warning)
	}
	for _, rec := range r.Recommendations {
		if rec.storageClass() {
			lines = append(lines, fmt.Sprintf(
				"liken: report: WARNING: %s needs %s, and spec.modules cannot supply a storage driver; build an image whose image/boot-modules.conf names it.",
				rec.Device, strings.Join(rec.Chain, " and ")))
		}
	}
	if len(planStorageLayout(r.Disks, r.UEFI).Roles) == 0 {
		lines = append(lines, "liken: report: WARNING: no disk here can hold a liken install; the proposal declares no storage.")
	}
	return lines
}

// writeRole renders one storage role. An empty size means the role
// takes the rest of its disk, which the spec allows for one role per
// disk. A comment, when present, rides on the value line so a person
// reads the reason beside the number.
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

// writeComment renders one sentence of prose as YAML comment lines,
// wrapped so the file reads on a console as well as in an editor.
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

// dedupChains flattens the recommendations into one ordered module
// list, each name once, in first-seen order. This is the list a person
// writes into spec.modules: the loader loads it in order, and a
// duplicate would only ask it to load the same file twice.
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

// orUnknown returns the value, or "unknown" when the bus published
// nothing. A blank in the evidence would read as a mistake in the
// report rather than as an absent attribute.
func orUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
