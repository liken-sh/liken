package main

// Composing the hardware report's proposal.
//
// The report boot answers the question a new machine asks before its
// first install: what does this hardware need in its manifest? It
// answers with a whole Machine manifest, ready to edit, with the
// evidence for every line written beside it as a comment. This file
// turns the facts the boot gathered into that document.
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

// reportDisk is one disk as the report observed it: its device path
// this boot, its size, its model when the bus publishes one, and the
// transport that carries it (sata, nvme, usb, virtio). The transport
// is evidence a person reads to recognize the disk; the device path is
// only a hint that matters on the boot that claims the disk.
type reportDisk struct {
	Path      string
	SizeBytes uint64
	Model     string
	Transport string
}

// reportInterface is one network interface as the report observed it,
// after it loaded the recommended drivers and brought the link
// admin-up. The name is real only because a driver bound the card:
// eth0 does not exist until its driver registers it. Link is the
// kernel's own word for the carrier, which a person reads to tell a
// connected port from a dark one.
type reportInterface struct {
	Name string
	MAC  string
	Link string
}

// moduleRecommendation pairs one unclaimed device with the ordered
// driver chain that would bind it. Chain is the softdep-expanded list,
// in load order, so a NIC that needs its PHY library first reads as
// [realtek, r8169], not r8169 alone. Device names the hardware in
// words, so the proposal's comment can say which device each driver
// claims. SysfsDirs is where the devices behind this fingerprint sit
// in sysfs; the composition ignores it, and the report boot uses it
// to drop the recommendation that serves only the installation stick.
type moduleRecommendation struct {
	Device    string
	Chain     []string
	SysfsDirs []string
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
	Disks           []reportDisk
	Interfaces      []reportInterface
}

// These are the conventional role sizes the proposal starts from. They
// match the sizes a real single-disk machine uses (see the founding
// leader's manifest): 1Gi slots with headroom, a small machineState,
// and a modest /tmp. They are a starting point, not a measurement, so
// every one carries a comment that tells the person to adjust it.
const (
	biosBootSize         = "1Mi"
	bootHomeSize         = "64Mi"
	systemSlotSize       = "1Gi"
	machineStateSize     = "64Mi"
	machineEphemeralSize = "512Mi"
	clusterStateSize     = "4Gi"
	podStorageSize       = "4Gi"
)

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
// hardware. The result is a valid Machine manifest: it parses as a
// Machine, and its storage roles satisfy the spec's own validation, so
// a person can install from it after only renaming the machine and
// sizing the data roles.
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
	composeModules(&b, r.Recommendations)
	composeNetwork(&b, r.Interfaces)
	composeStorage(&b, r)
	return b.String()
}

// composeModules writes the spec.modules section: the drivers the
// report recommends, in load order, with one comment for each device
// that named them. The declared list is the deduplicated union of every
// chain, because a module named twice would ask the loader to load it
// twice. The comments carry the reasoning, so the list itself stays
// exactly what a person declares.
func composeModules(b *strings.Builder, recs []moduleRecommendation) {
	b.WriteString("  # Extra kernel modules this machine's hardware wants, beyond\n")
	b.WriteString("  # the drivers the OS already loads. Each comment names a device\n")
	b.WriteString("  # with no driver bound and the modules that would bind it, in\n")
	b.WriteString("  # the order to load them.\n")
	if len(recs) == 0 {
		b.WriteString("  # This report found no unclaimed hardware. If a disk or an\n")
		b.WriteString("  # interface you expect is missing below, its controller may\n")
		b.WriteString("  # need a driver this image does not carry.\n")
		b.WriteString("  modules: []\n")
		return
	}
	for _, rec := range recs {
		fmt.Fprintf(b, "  #   %s: %s\n", rec.Device, strings.Join(rec.Chain, ", then "))
	}
	b.WriteString("  modules:\n")
	for _, name := range dedupChains(recs) {
		fmt.Fprintf(b, "    - %s\n", name)
	}
}

// composeNetwork writes the spec.network section from the interfaces
// the report observed. The names are the real kernel names, because the
// report loaded the drivers before it read them. The first interface is
// left on DHCP, the zero-configuration default, and a comment shows how
// to pin a static address for a cluster segment.
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
	b.WriteString("  network:\n")
	b.WriteString("    interfaces:\n")
	for i, ifc := range ifaces {
		fmt.Fprintf(b, "      # MAC %s, link %s\n", ifc.MAC, ifc.Link)
		if i == 0 {
			b.WriteString("      # This first interface uses DHCP. For a cluster segment\n")
			b.WriteString("      # with a fixed address, add an \"address: 10.0.0.N/24\" line.\n")
		}
		fmt.Fprintf(b, "      - name: %s\n", ifc.Name)
	}
}

// composeStorage writes the spec.storage section. It first lists every
// disk the report saw as comments, the evidence a person matches
// against the machine in front of them, then lays down a conventional
// role layout they can install from at once. A machine with two or more
// disks gets the durable roles on its second disk, so cluster state
// survives a reinstall that replaces the system disk. The layout is a
// starting point; every size carries a comment.
func composeStorage(b *strings.Builder, r hardwareReport) {
	b.WriteString("  # The disks this report saw. The device path is only a hint\n")
	b.WriteString("  # that matters on the boot that claims the disk; after that,\n")
	b.WriteString("  # liken finds each role by the name it writes into the GPT.\n")
	for _, d := range r.Disks {
		fmt.Fprintf(b, "  #   %s  %s  %s  (%s)\n",
			d.Path, gib(d.SizeBytes), orUnknown(d.Model), orUnknown(d.Transport))
	}
	if r.StickPath != "" {
		fmt.Fprintf(b, "  #   (%s is the installation stick; it leaves with you,\n", r.StickPath)
		b.WriteString("  #   so it is not listed and no role may live on it)\n")
	}
	if len(r.Disks) == 0 {
		b.WriteString("  # This report saw no disk. Its controller may need a driver\n")
		b.WriteString("  # that the boot path does not carry.\n")
		b.WriteString("  storage: {}\n")
		return
	}

	system := r.Disks[0].Path
	durable := system
	if len(r.Disks) > 1 {
		durable = r.Disks[1].Path
	}

	b.WriteString("  storage:\n")
	if r.UEFI {
		b.WriteString("    # This machine booted UEFI, so the firmware holds its boot\n")
		b.WriteString("    # entries and it needs no biosBoot or bootHome role.\n")
	} else {
		b.WriteString("    # This machine booted BIOS, so liken supplies the boot\n")
		b.WriteString("    # bookkeeping that UEFI firmware would otherwise hold.\n")
		writeRole(b, "biosBoot", system, biosBootSize, "# GRUB core image; a tiny raw partition")
		writeRole(b, "bootHome", system, bootHomeSize, "# GRUB config and environment block")
	}
	writeRole(b, "systemA", system, systemSlotSize, "# one OS slot; the blue-green pair")
	writeRole(b, "systemB", system, systemSlotSize, "")
	writeRole(b, "machineState", system, machineStateSize, "# staged and proven manifests")
	writeRole(b, "machineEphemeral", system, machineEphemeralSize, "# the OS's /tmp")
	writeRole(b, "clusterState", durable, clusterStateSize, "# size to your cluster's state")
	writeRole(b, "podStorage", durable, podStorageSize, "# size to your workloads' volumes")
	writeRole(b, "podEphemeral", durable, "", "# takes the rest of this disk")
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
