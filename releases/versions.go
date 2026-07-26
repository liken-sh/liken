package releases

// The versions document is the channel's list of every release it
// holds, in one file that a script can read.
//
// The channel document (machine/channel.go) names one version, the
// newest, and that is all a polling cluster needs. A person or a
// script often wants the other question answered: what else is here?
// Object storage will not answer it, because the bucket refuses to
// list itself, so the answer has to be published like anything else.
// This document is the machine-readable twin of the front page, and
// both come out of the same index run.
//
// It is a separate document rather than a field on the channel
// document for one reason: every cluster in the fleet fetches
// channel.yaml on a poll, and that document must stay one small,
// fixed size no matter how many releases have ever been published.
//
// The digests here are a convenience and not an authority. A digest
// that the channel served vouches for nothing on its own, which is
// why adopting a release means an operator pins the digest in their
// own Cluster document. This file makes that entry easy to find and
// easy to diff. It does not make the channel trusted.

import (
	"fmt"
	"strings"

	"github.com/liken-sh/liken/api"
)

// A Versions document lists every release a channel serves, newest
// first.
type Versions struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   api.ObjectMeta `json:"metadata"`
	Latest     string         `json:"latest"`
	Releases   []VersionEntry `json:"releases"`
}

// A VersionEntry is one release, in the shape of the catalog entry
// that adopts it. The field names match a Cluster's
// spec.releases.catalog, so an entry copies straight across.
type VersionEntry struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// versionsDocument arranges the indexed releases as the document the
// channel publishes at /versions.yaml.
//
// The text is written out rather than marshalled, the same way the
// release and channel documents are (bundle.go). A marshaller sorts
// the keys, which would put the digest above the version it belongs
// to. A person reads this file, and the entries are meant to be
// copied straight into a Cluster's spec.releases.catalog, so they
// have to appear in the order that catalog uses.
func versionsDocument(latest string, releases []*releaseInfo) []byte {
	var out strings.Builder
	fmt.Fprintf(&out, "apiVersion: %s\nkind: Versions\nmetadata:\n  name: liken\n", api.APIVersion)
	fmt.Fprintf(&out, "latest: %s\nreleases:\n", latest)
	for _, release := range releases {
		fmt.Fprintf(&out, "  - version: %s\n    digest: %s\n", release.Version, release.Digest)
	}
	return []byte(out.String())
}
