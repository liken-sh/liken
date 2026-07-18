package main

// The on-board components record.
//
// A release document lists every outside component and the upstream
// version of it that shipped (machine/release.go), but the document
// lives on the channel, and a running machine shouldn't have to
// phone anywhere to know what it is made of. So the image build
// writes the same record — from the same VERSION pins — into the
// image itself, and this file folds it into the version facts the
// Machine publishes.
//
// The fold is deliberately one-way and deferential: a component the
// running machine already answered for (the kernel via uname, the
// netfilter userspace via iptables, liken via its build stamp) keeps
// its observed value, in the running software's own vocabulary, and
// the record fills only what nothing can observe — the boot
// artifacts, the bundled images, the data files. A missing record is
// tolerated quietly because dev boots predating the record (or lab
// images built by hand) are still valid machines; their versions are
// simply sparser.

import (
	"os"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/machine"
)

// componentsPath is where the image build stages the record: under
// /usr/share, owned by the squashfs, deliberately outside /etc/liken
// where the deployment layer's files live — this is a fact about the
// image, not about any deployment of it. A variable for the tests'
// sake.
var componentsPath = "/usr/share/liken/components.yaml"

// applyComponentFacts folds the record into the version block,
// filling only the fields no runtime probe owns.
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
		}
	}
}
