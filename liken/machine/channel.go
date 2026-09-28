package machine

// The channel document shows the newest release in a release channel.
//
// liken has one linear channel. Releases only move forward. So the
// document needs only one fact: the latest version. The document is
// at the root of the channel, one URL above the releases. It names
// the newest published version.
//
// The document is advisory. It stays outside the trust chain. A
// cluster can poll the document to find a newer release, and show
// that release next to the version it runs. Adopting a release still
// needs the digest-pinned catalog entry in the Cluster document. A
// tampered channel document can show wrong information. It cannot
// change what a machine installs.

import (
	"fmt"

	"github.com/liken-sh/liken/api"
	"sigs.k8s.io/yaml"
)

type Channel struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   api.ObjectMeta `json:"metadata"`
	Latest     string         `json:"latest"`
}

// ParseChannel validates a channel document when it reads the
// document. ParseRelease validates a release document the same way.
// If a document is not exactly what it claims to be, ParseChannel
// rejects it with the reason. It never accepts a document partially.
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
	if err := api.ValidVersion(c.Latest); err != nil {
		return nil, fmt.Errorf("channel %s: latest: %w", c.Metadata.Name, err)
	}
	return c, nil
}
