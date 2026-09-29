package main

import (
	"fmt"
	"strings"
)

// A RecordEntry is one component's version at a release tag.
type RecordEntry struct {
	Component string `yaml:"component"`
	Version   string `yaml:"version"`
	// Released is true when this tag published the version.
	Released bool `yaml:"released"`
	// Pinned is true for a pinned component, whose version is the tag
	// in its package.toml at the release's commit.
	Pinned bool `yaml:"pinned"`
}

// Record lists every component that publishes, with its version at
// the tag: the newest release at or before the tag. A component that
// did not change keeps the version an earlier tag gave it.
func Record(components map[string]*Component, versions VersionsFunc, tag string) ([]RecordEntry, error) {
	var entries []RecordEntry
	for _, c := range sortedComponents(components) {
		if !c.HasOutputs() {
			continue
		}
		if c.Pinned() {
			entries = append(entries, RecordEntry{Component: c.Name(), Version: c.PinnedTag(), Pinned: true})
			continue
		}
		published, err := versions(c)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Name(), err)
		}
		version := NewestReleaseAtOrBefore(published, tag)
		entries = append(entries, RecordEntry{Component: c.Name(), Version: version, Released: version == tag})
	}
	return entries, nil
}

// RecordNotes renders the record as the body of the tag's GitHub
// release. The OS's catalog entry comes first when the tag released
// the OS, because a cluster adopts a release by that entry.
func RecordNotes(tag string, entries []RecordEntry, catalogDigest string, changes []string) string {
	var b strings.Builder
	if catalogDigest != "" {
		fmt.Fprintf(&b, "liken %s is on the channel at https://releases.liken.sh/%s/release.yaml.\n\n", tag, tag)
		b.WriteString("To adopt it, add the catalog entry to your Cluster's `spec.releases.catalog` and set `spec.version`:\n\n")
		fmt.Fprintf(&b, "```yaml\n  - version: %s\n    digest: %s\n```\n\n", tag, catalogDigest)
	}
	b.WriteString("| Component | Version | |\n| --- | --- | --- |\n")
	for _, e := range entries {
		version, note := e.Version, "unchanged"
		if e.Pinned {
			note = "pinned"
		} else if version == "" {
			version, note = "none", "not published yet"
		} else if e.Released {
			note = "released by this tag"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", e.Component, version, note)
	}
	if len(changes) > 0 {
		b.WriteString("\nChanges:\n\n")
		for _, subject := range changes {
			fmt.Fprintf(&b, "- %s\n", subject)
		}
	}
	return b.String()
}
