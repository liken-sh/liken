package main

import (
	"fmt"
	"io"
	"net/http"

	"gopkg.in/yaml.v3"
)

// Published reads the versions of a component that are already
// published. It reads the release channel for the OS and ghcr.io for
// every other component.
type Published struct {
	Registry Registry
	// Channel is the release channel's address, https://releases.liken.sh.
	Channel string
	Client  *http.Client
}

// Versions lists the published versions of the component, releases
// and development builds together.
//
// A component with a deploy directory counts as published when its
// deploy artifact is, because the publish pushes the artifact after
// every image. Until the artifact has a release, the tags of the
// component's first image are used instead, because earlier releases
// of the component have only images. A component that builds no image
// has only the artifact.
func (p Published) Versions(c *Component) ([]string, error) {
	if c.Outputs.Channel {
		releases, err := p.channelReleases()
		if err != nil {
			return nil, err
		}
		versions := make([]string, len(releases))
		for i, r := range releases {
			versions[i] = r.Version
		}
		return versions, nil
	}
	if c.Outputs.Deploy != "" {
		tags, err := p.Registry.Tags(c.DeployPackage())
		if err != nil {
			return nil, err
		}
		if NewestRelease(tags) != "" || len(c.Outputs.Images) == 0 {
			return tags, nil
		}
	}
	if len(c.Outputs.Images) == 0 {
		return nil, nil
	}
	return p.Registry.Tags(c.Outputs.Images[0].Name)
}

// A ChannelRelease is one entry of the channel's versions.yaml.
type ChannelRelease struct {
	Version string `yaml:"version"`
	Digest  string `yaml:"digest"`
}

// channelReleases reads versions.yaml from the channel, which lists
// every release with the digest of its release.yaml.
func (p Published) channelReleases() ([]ChannelRelease, error) {
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Get(p.Channel + "/versions.yaml")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reading %s/versions.yaml: the server returned %s", p.Channel, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading the body of %s/versions.yaml: %w", p.Channel, err)
	}
	var doc struct {
		Releases []ChannelRelease `yaml:"releases"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s/versions.yaml: %w", p.Channel, err)
	}
	return doc.Releases, nil
}
