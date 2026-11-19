package machine

// A SystemRelease record names the slot that init boots. It goes
// through the same staged, attempted, and proven steps as the Machine
// and Cluster manifests (staging.go), in its own store.
// machine-operator stages a record when it has verified a release on
// the inactive slot and the machine should reboot into it. Init marks
// the record attempted when it sets the firmware to boot that slot
// once and reboots. machine-operator promotes it to proven when the
// machine reports that it runs that release from that slot.
//
// The proven record names the slot that is known to work, and at boot
// init writes the firmware's BootOrder to match it. So BootOrder
// always follows the proven record, and not the other way round.
//
// The record has no artifact digests, because the release document on
// the slot has them, and they were verified when the slot was written.

import (
	"fmt"

	"github.com/liken-sh/liken/liken/api"
	"sigs.k8s.io/yaml"
)

// SystemRelease is a release installed on one slot.
type SystemRelease struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`

	// Version is the release version, as the catalog spells it.
	Version string `json:"version"`

	// Slot is the slot that holds the release's artifacts: "A" or "B".
	Slot string `json:"slot"`

	// ReleaseDigest is the catalog's digest of the release document.
	// A version that is published again with a different document
	// makes a different record, and so a new upgrade. It is empty on
	// the record that machine-operator writes for a machine that has
	// never upgraded, because its release came from the install and
	// not from a catalog.
	ReleaseDigest string `json:"releaseDigest,omitempty"`
}

// SystemReleases returns the store of the record under the
// machineState root.
func SystemReleases(root string) ManifestStore {
	return ManifestStore{dir: root + "/system"}
}

// RenderSystemRelease returns the record's canonical bytes and their
// hash. The attempted marker and a rejection name the record by this
// hash.
func RenderSystemRelease(version, slot, releaseDigest string) ([]byte, string, error) {
	return renderDocument(SystemRelease{
		APIVersion:    api.APIVersion,
		Kind:          "SystemRelease",
		Version:       version,
		Slot:          slot,
		ReleaseDigest: releaseDigest,
	})
}

// ParseSystemRelease parses a record strictly and validates it. An
// invalid record would fail every boot the same way, so a caller
// rejects it once instead of retrying it.
func ParseSystemRelease(raw []byte) (*SystemRelease, error) {
	r := &SystemRelease{}
	if err := yaml.UnmarshalStrict(raw, r); err != nil {
		return nil, err
	}
	if r.Kind != "SystemRelease" {
		return nil, fmt.Errorf("the kind is %q, and a system release record must have kind SystemRelease", r.Kind)
	}
	if r.Version == "" {
		return nil, fmt.Errorf("the record has no version")
	}
	if r.Slot != "A" && r.Slot != "B" {
		return nil, fmt.Errorf("the record has slot %q, and the slot must be A or B", r.Slot)
	}
	return r, nil
}
