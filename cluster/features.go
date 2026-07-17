package cluster

// The feature vocabulary: the curated set of optional capabilities a
// cluster may opt into through spec.features (see ClusterSpec).
//
// liken is a minimum viable highly-available cluster, and capabilities
// people may not need should not run on every deployment. But an OS
// must be able to offer more than its minimum, so the Cluster document
// carries a vocabulary of optional features, and this table is that
// vocabulary. Deployments name features; what a feature is made of is
// liken's concern, recorded here. Everything that must agree on the
// vocabulary agrees by consulting this table: init validates the
// cluster document against it and renders the k3s disable list from
// it, the operator judges each machine's standing against it, and a
// parity test holds the hand-written CRD (cluster/manifests/clusters-crd.yaml)
// to exactly these slugs.
//
// A feature is a slug plus any subset of these contributions:
//
//   - k3s configuration rendered at boot: today, membership in the
//     disable list init computes into the leader's boot drop-in
//     (init/k3s.go).
//   - Vendored userspace binaries: a top-level domain in this
//     repository (a pinned VERSION and a fetch.sh producing
//     sha256-verified static binaries), shipped in every image and
//     inert until declared.
//   - Kernel modules: the domain's modules.conf, staged into the
//     image at /etc/liken/features/<slug>/modules.conf and loaded by
//     init only when the feature is declared. That file's presence is
//     also how init knows the booted image carries the payload, so no
//     feature-to-modules mapping lives in Go.
//   - Workload manifests and OCI images: riding the image, seeded
//     into k3s's auto-deploy directory by init only when declared.
//   - An init boot hook: per-slug code gated on declaration, for the
//     few features with a boot-time contribution (an iSCSI initiator
//     name, for example).
//   - Parameters: the feature's configuration object, empty for
//     every feature today (see FeatureConfig).
//
// Payloads ship in every image because they are small. Enabling a
// feature is then purely a runtime act: one Cluster edit that rolls
// through the fleet as staged changes and granted reboots, never an
// image rebuild. A feature too large to ship unconditionally (a GPU
// toolkit, say) would be the moment to introduce build-time
// conditioning, and not before.

import (
	"fmt"
	"maps"
	"slices"
)

// FeatureKind is how a feature is delivered. Deployments never see
// the distinction; the machinery needs it to know what enabling a
// feature actually does.
type FeatureKind string

const (
	// FeatureBundled is a component the k3s binary already carries
	// (Traefik, the service load balancer, metrics-server). The
	// image ships it whether or not anyone wants it, so opting in
	// costs nothing at build time; the whole actuation is init
	// leaving the component out of the disable list it renders into
	// the k3s boot drop-in.
	FeatureBundled FeatureKind = "Bundled"

	// FeatureEmbedded is a controller compiled into the k3s server
	// process itself, rather than a component k3s deploys into the
	// cluster: the Helm controller (which watches HelmChart
	// resources and renders them into workloads) and the network
	// policy controller (which turns NetworkPolicy resources into
	// packet filtering, something the flannel CNI cannot do alone).
	// Each costs memory in the k3s process whether or not any of its
	// resources exist. An embedded feature can never appear on the
	// disable list (that list names deployable components); its
	// actuation is a dedicated disable key init renders into the
	// leader's boot drop-in (init/k3s.go), omitted only when the
	// feature is enabled.
	FeatureEmbedded FeatureKind = "Embedded"

	// FeatureVendored is a payload this repository vendors as a
	// top-level domain: static binaries, kernel modules, sometimes a
	// workload manifest. The payload rides every image, and
	// /etc/liken/features/<slug>/modules.conf existing in the booted
	// image is how init knows this image carries it. That check
	// matters when an older image boots against a cluster document
	// declaring a feature the image predates: the machine reports
	// the gap instead of silently lacking the capability.
	FeatureVendored FeatureKind = "Vendored"
)

// FeatureDefinition names one feature: its slug, which is the key
// deployments write in spec.features, and its kind. Requires names
// the features this one cannot work without; enabling a feature
// enables everything it requires, so a deployment declares what it
// wants and never has to know the dependency exists.
type FeatureDefinition struct {
	Slug     string
	Kind     FeatureKind
	Requires []string
}

// Features is the vocabulary, in the order the story is told. The
// bundled components' slugs are exactly k3s's names for them, the
// same words the disable list uses; a vendored feature's slug names
// the capability (iscsi), never the project that implements it
// (open-iscsi), because implementations can change and an API should
// not have to.
var Features = []FeatureDefinition{
	{Slug: "traefik", Kind: FeatureBundled, Requires: []string{"helm"}},
	{Slug: "servicelb", Kind: FeatureBundled},
	{Slug: "metrics-server", Kind: FeatureBundled},
	{Slug: "helm", Kind: FeatureEmbedded},
	{Slug: "network-policy", Kind: FeatureEmbedded},
	{Slug: "iscsi", Kind: FeatureVendored},
	{Slug: "nfs", Kind: FeatureVendored},
}

// FeatureConfig is one feature's configuration. Every feature today
// has zero configuration, so the struct is empty and {} is how a
// feature is enabled. A feature's first parameter lands here as a
// field, together with a matching property in the CRD; until then,
// the strict parse rejects any key inside the object. (When that
// first parameter arrives, validation must also grow a per-slug
// check: a shared struct would otherwise accept one feature's
// parameter under another feature's slug, which the CRD's named
// properties already refuse.)
type FeatureConfig struct{}

// FeatureBySlug finds one feature's definition, nil when the
// vocabulary doesn't include the slug.
func FeatureBySlug(slug string) *FeatureDefinition {
	for i := range Features {
		if Features[i].Slug == slug {
			return &Features[i]
		}
	}
	return nil
}

// FeatureSlugs returns every slug in table order, for error messages
// and the CRD parity test.
func FeatureSlugs() []string {
	slugs := make([]string, len(Features))
	for i, f := range Features {
		slugs[i] = f.Slug
	}
	return slugs
}

// EnabledFeatures returns the slugs this cluster's declarations
// enable, sorted: the declared opt-ins plus everything they require
// (traefik pulls in helm, because k3s deploys Traefik through a
// HelmChart resource only the Helm controller can render). The
// closure runs to a fixed point, so a requirement's own requirements
// are honored too. A nil Cluster (a machine with no cluster
// document) enables nothing, which is how the minimum viable cluster
// stays the default.
func (c *Cluster) EnabledFeatures() []string {
	if c == nil {
		return nil
	}
	enabled := map[string]bool{}
	for slug := range c.Spec.Features {
		enabled[slug] = true
	}
	for changed := true; changed; {
		changed = false
		for _, f := range Features {
			if !enabled[f.Slug] {
				continue
			}
			for _, req := range f.Requires {
				if !enabled[req] {
					enabled[req] = true
					changed = true
				}
			}
		}
	}
	return slices.Sorted(maps.Keys(enabled))
}

// FeatureEnabled reports whether one feature is on for this cluster,
// declared or required by a declared one.
func (c *Cluster) FeatureEnabled(slug string) bool {
	return slices.Contains(c.EnabledFeatures(), slug)
}

// DisabledComponents computes the k3s disable list: every bundled
// component minus this cluster's opt-ins, sorted. It is always the
// complete list, never a fragment for k3s to merge with a default
// somewhere else, so the rendered value has exactly one author: init,
// which writes it into the boot drop-in on leaders (init/k3s.go). A
// nil Cluster disables everything bundled, keeping the principled
// default for a machine on its own.
func (c *Cluster) DisabledComponents() []string {
	var disabled []string
	for _, f := range Features {
		if f.Kind != FeatureBundled {
			continue
		}
		if c != nil {
			if _, enabled := c.Spec.Features[f.Slug]; enabled {
				continue
			}
		}
		disabled = append(disabled, f.Slug)
	}
	slices.Sort(disabled)
	return disabled
}

// validateFeatures holds spec.features to its shape. ParseCluster
// calls it, so every file door (init vetting a staged or proven
// document, the operator hashing a rendered one) refuses a null the
// same way the CRD refuses it at admission, with a message that says
// what to write instead.
//
// An unknown slug is deliberately not an error here, though the CRD
// refuses one at admission. The difference is what each door knows: a
// fleet has exactly one vocabulary at its API (the newest image's
// CRD), but each machine's parser knows only the vocabulary its own
// image was built with, and a fleet mid-upgrade holds several of
// those at once. A document declaring a feature this binary predates
// must still parse, or the machine could not read its own proven
// document after a downgrade, could not derive its role, and would
// sit Blocked on a document the rest of the fleet is happily running.
// The unknown slug is reported instead, through the feature pass:
// FeaturesReady goes False naming the slug and this image's
// vocabulary, which covers both real causes (an image that predates
// the feature, and a misspelling in a hand-written seed) with the
// machine degraded rather than down.
func validateFeatures(features map[string]*FeatureConfig) error {
	for _, slug := range slices.Sorted(maps.Keys(features)) {
		if features[slug] == nil {
			return fmt.Errorf("spec.features: %s is null, which in Kubernetes means unset; presence is the opt-in, so write %q to enable the feature or remove the key entirely",
				slug, slug+": {}")
		}
	}
	return nil
}
