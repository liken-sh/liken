package main

// The release document on the channel lists the version of each
// outside component in a release (machine/release.go). A running
// machine reports those versions without fetching the document,
// because the image build writes the same list, from the same VERSION
// pins, into the image as the components record. This file copies the
// record into the Machine's status.version.
//
// The record fills only the fields that the machine cannot read at
// runtime: the boot artifacts, the bundled programs, and the data
// files. The kernel, iptables, and liken versions come from the
// running machine. When the record is missing, those other fields stay
// empty.

import (
	"os"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/liken/machine"
)

// componentsPath is under /usr/share and not /etc/liken, which holds
// the deployment's files, because the record describes the image.
var componentsPath = "/usr/share/liken/components.yaml"

var cpuinfoPath = "/proc/cpuinfo"

// microcodeRevision returns the CPU microcode revision from the first
// microcode line of /proc/cpuinfo, for example "0x28", because every
// core runs the same revision after the early loader. It returns ""
// when cpuinfo has no microcode line, as on some hypervisors.
func microcodeRevision(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		key, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(key) == "microcode" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// observedAtRuntime lists the components whose versions the running
// machine reads itself, so applyComponentFacts has no case for them.
// TestEveryRecordedComponentReachesTheVersionStatus reads this list.
var observedAtRuntime = []string{"liken", "kernel", "xtables"}

// applyComponentFacts copies the record's versions into v. Its switch
// needs a case for each component that image/build.sh writes and
// observedAtRuntime does not list.
// TestEveryRecordedComponentReachesTheVersionStatus fails when one is
// missing.
func applyComponentFacts(v *machine.VersionStatus) {
	raw, err := os.ReadFile(componentsPath)
	if err != nil {
		return
	}
	var record struct {
		Components []machine.ReleaseComponent `json:"components"`
	}
	if err := yaml.UnmarshalStrict(raw, &record); err != nil {
		return
	}
	for _, c := range record.Components {
		switch c.Name {
		case "k3s":
			v.K3s = c.Version
		case "trust":
			v.Trust = c.Version
		case "e2fsprogs":
			v.E2fsprogs = c.Version
		case "open-iscsi":
			v.OpenISCSI = c.Version
		case "nfs-utils":
			v.NFSUtils = c.Version
		case "wpa-supplicant":
			v.WPASupplicant = c.Version
		case "systemd-boot":
			v.SystemdBoot = c.Version
		case "grub":
			v.Grub = c.Version
		case "hwdata":
			v.Hwdata = c.Version
		case "tzdata":
			v.Tzdata = c.Version
		case "linux-firmware":
			v.LinuxFirmware = c.Version
		case "wireless-regdb":
			v.WirelessRegdb = c.Version
		case "microcode":
			v.Microcode = c.Version
		}
	}
}
