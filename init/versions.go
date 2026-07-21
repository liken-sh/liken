package main

// The on-board components record.
//
// A release document lists every outside component, and the
// upstream version of it that shipped (see machine/release.go). This
// document lives on the channel, and a running machine should not
// have to contact the channel to know what it is made of. So the
// image build writes the same record, from the same VERSION pins,
// into the image itself. This file folds that record into the
// version facts that the Machine publishes.
//
// The fold works in one direction only, and it does not override
// values the machine already knows. For a component that the
// running machine already has an observed value for, such as the
// kernel via uname, the netfilter userspace via iptables, or liken
// via its own build stamp, this fold keeps that observed value, in
// the running software's own vocabulary. The record fills in only
// what nothing else can observe: the boot artifacts, the bundled
// images, and the data files. If the record is missing, this fold is
// silent about it, because dev boots that predate the record, and
// lab images built by hand, are still valid machines. Their version
// facts are simply sparser.

import (
	"os"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/machine"
)

// componentsPath is where the image build stages the record. It is
// under /usr/share, owned by the squashfs, and deliberately outside
// /etc/liken, where the deployment layer's files live. The record is
// a fact about the image, not about any deployment of it. This is a
// variable so tests can override it.
var componentsPath = "/usr/share/liken/components.yaml"

// applyComponentFacts folds the record into the version block. It
// fills only the fields that no runtime probe owns.
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
		case "systemd-boot":
			v.SystemdBoot = c.Version
		case "grub":
			v.Grub = c.Version
		case "hwdata":
			v.Hwdata = c.Version
		case "linux-firmware":
			v.LinuxFirmware = c.Version
		}
	}
}
