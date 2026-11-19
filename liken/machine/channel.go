package machine

// The channel document is channel.yaml at the root of the release
// source, and its latest field is the newest published release. liken
// has one channel and releases only move forward, so the document
// needs no other field.
//
// The document is outside the trust chain. cluster-operator polls it
// to show a newer release in the Cluster's status, and the release
// fetcher reads it to choose which version to fetch. A machine installs
// only a release from the Cluster's catalog, whose entries pin each
// release's digest, so a tampered channel document can show a wrong
// version but cannot change what a machine installs.

import (
	"fmt"

	"github.com/liken-sh/liken/liken/api"
	"sigs.k8s.io/yaml"
)

type Channel struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   api.ObjectMeta `json:"metadata"`
	Latest     string         `json:"latest"`
}

// ParseChannel returns an error, and no Channel, when the document has
// an unknown field, the wrong kind, no name, or a malformed latest.
func ParseChannel(raw []byte) (*Channel, error) {
	c := &Channel{}
	if err := yaml.UnmarshalStrict(raw, c); err != nil {
		return nil, err
	}
	if c.Kind != "Channel" {
		return nil, fmt.Errorf("the document has kind %q, want Channel", c.Kind)
	}
	if c.Metadata.Name == "" {
		return nil, fmt.Errorf("the channel has no metadata.name")
	}
	if err := api.ValidVersion(c.Latest); err != nil {
		return nil, fmt.Errorf("checking latest of channel %s: %w", c.Metadata.Name, err)
	}
	return c, nil
}
