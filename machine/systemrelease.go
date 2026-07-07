package machine

// The system release record: the document that carries an OS upgrade
// through its proving reboot.
//
// A verified download is bytes on a slot; this record is the *intent*
// to boot them. It rides the same staged/proven/attempted lifecycle
// as the Machine and Cluster manifests (staging.go), in its own
// store: staged when the operator has verified a release on the
// inactive slot and the machine should reboot into it, attempted when
// init arms the firmware and takes the machine down, proven when the
// operator — running *as* the new release — confirms the machine came
// up serving its cluster. The proven record is the standing answer to
// "which slot is this machine's known-good", and init re-asserts the
// firmware's BootOrder from it on every boot: the store is the
// authority, the firmware a cache of it.
//
// The record deliberately carries no artifact digests: those live in
// the release document on the slot itself, and the trust chain
// already ran when the bytes landed. What the record pins is the
// decision — this version, vouched for by this catalog digest, on
// this slot.

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

// SystemRelease names one release standing on one slot.
type SystemRelease struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`

	// Version is the release, as the catalog names it.
	Version string `json:"version"`

	// Slot is where the release's artifacts stand: "A" or "B".
	Slot string `json:"slot"`

	// ReleaseDigest is the catalog's digest over the release document,
	// carried so the record's identity changes when the catalog's
	// promise does — the same version republished under a different
	// digest is a different decision. Empty on records that describe
	// an installed system rather than a catalog download (the
	// installer's release predates any catalog).
	ReleaseDigest string `json:"releaseDigest,omitempty"`
}

// SystemReleases is the record's lifecycle store under the given
// machineState root, beside the Machine's and the Cluster's.
func SystemReleases(root string) ManifestStore {
	return ManifestStore{dir: root + "/system"}
}

// RenderSystemRelease produces the record's canonical bytes and their
// hash — its identity for staging idempotence, the attempted marker,
// and rejections (yaml marshals through JSON with sorted keys, so the
// same decision always renders the same bytes).
func RenderSystemRelease(version, slot, releaseDigest string) ([]byte, string, error) {
	record := SystemRelease{
		APIVersion:    APIVersion,
		Kind:          "SystemRelease",
		Version:       version,
		Slot:          slot,
		ReleaseDigest: releaseDigest,
	}
	raw, err := yaml.Marshal(&record)
	if err != nil {
		return nil, "", err
	}
	return raw, ManifestHash(raw), nil
}

// ParseSystemRelease reads a record strictly and vets it at the door,
// for the same reason every liken document is vetted: a record that
// would fail every future boot the same way should be rejected once,
// visibly, not retried forever.
func ParseSystemRelease(raw []byte) (*SystemRelease, error) {
	r := &SystemRelease{}
	if err := yaml.UnmarshalStrict(raw, r); err != nil {
		return nil, err
	}
	if r.Kind != "SystemRelease" {
		return nil, fmt.Errorf("expected kind SystemRelease, got %q", r.Kind)
	}
	if r.Version == "" {
		return nil, fmt.Errorf("the record names no version")
	}
	if r.Slot != "A" && r.Slot != "B" {
		return nil, fmt.Errorf("the record's slot must be A or B, got %q", r.Slot)
	}
	return r, nil
}
