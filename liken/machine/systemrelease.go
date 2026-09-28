package machine

// The system release record is the document that carries an OS
// upgrade through its proving reboot.
//
// A verified download is bytes on a slot. This record states the
// intent to boot those bytes. The record goes through the same
// staged, attempted, and proven lifecycle as the Machine and Cluster
// manifests (staging.go), in its own store. The record is staged
// when the operator has verified a release on the inactive slot and
// the machine should reboot into it. The record is attempted when
// init arms the firmware and takes the machine down. The record is
// proven when the operator, now running as the new release, confirms
// that the machine came up and serves its cluster. The proven record
// is the standing answer to the question "which slot is this
// machine's known-good slot?" init sets the firmware's BootOrder
// from the proven record on every boot. The store holds the
// authoritative answer. The firmware holds a cached copy of it.
//
// The record carries no artifact digests, by design. Those digests
// live in the release document on the slot itself, and the trust
// chain already ran when the bytes landed. The record pins the
// decision itself: this version, under this catalog digest, on this
// slot.

import (
	"fmt"

	"github.com/liken-sh/liken/api"
	"sigs.k8s.io/yaml"
)

// SystemRelease names one release installed on one slot.
type SystemRelease struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`

	// Version is the release, exactly as the catalog names it.
	Version string `json:"version"`

	// Slot names where the release's artifacts are: "A" or "B".
	Slot string `json:"slot"`

	// ReleaseDigest is the catalog's digest over the release
	// document. The record carries this digest so the record's
	// identity changes when the catalog's entry changes. The same
	// version, republished under a different digest, counts as a
	// different decision. ReleaseDigest is empty on records that
	// describe an installed system instead of a catalog download.
	// The installer's release predates any catalog.
	ReleaseDigest string `json:"releaseDigest,omitempty"`
}

// SystemReleases returns the record's lifecycle store under the
// given machineState root, beside the Machine's store and the
// Cluster's store.
func SystemReleases(root string) ManifestStore {
	return ManifestStore{dir: root + "/system"}
}

// RenderSystemRelease produces the record's canonical bytes and their
// hash. The hash serves as the record's identity for staging
// idempotence, for the attempted marker, and for rejections.
func RenderSystemRelease(version, slot, releaseDigest string) ([]byte, string, error) {
	return renderDocument(SystemRelease{
		APIVersion:    api.APIVersion,
		Kind:          "SystemRelease",
		Version:       version,
		Slot:          slot,
		ReleaseDigest: releaseDigest,
	})
}

// ParseSystemRelease reads a record with strict checks and validates
// the record right away. liken validates every document for the same
// reason: if a record would fail every future boot in the same way,
// the system must reject the record once, in a visible way. The
// system must not retry the record forever.
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
