package machine

// The channel document announces what a release channel currently
// offers.
//
// liken has one linear channel: releases only move forward, so the
// only question a channel needs to answer is "what is the latest?"
// This document lives at the channel's root — one URL up from the
// releases themselves — and names the newest published version.
//
// The document is advisory, deliberately outside the trust chain. A
// cluster may poll it to learn that a newer release exists and to
// surface that next to its running version, but *adopting* a release
// still requires the digest-pinned catalog entry in the Cluster
// document. A tampered channel document can misstate what exists; it
// can never change what a machine installs.

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

type Channel struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   ObjectMeta `json:"metadata"`
	Latest     string     `json:"latest"`
}

// ParseChannel validates a channel document as it is read, the way
// ParseRelease does for its document: a document that isn't exactly
// what it claims to be is rejected with the reason, never partially
// accepted.
func ParseChannel(raw []byte) (*Channel, error) {
	c := &Channel{}
	if err := yaml.UnmarshalStrict(raw, c); err != nil {
		return nil, err
	}
	if c.Kind != "Channel" {
		return nil, fmt.Errorf("expected kind Channel, got %q", c.Kind)
	}
	if c.Metadata.Name == "" {
		return nil, fmt.Errorf("a channel must have a name")
	}
	if err := ValidVersion(c.Latest); err != nil {
		return nil, fmt.Errorf("channel %s: latest: %w", c.Metadata.Name, err)
	}
	return c, nil
}
