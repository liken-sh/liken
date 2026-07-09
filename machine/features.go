package machine

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
// parity test holds the hand-written CRD (manifests/clusters-crd.yaml)
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
	"strings"
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
// deployments write in spec.features, and its kind.
type FeatureDefinition struct {
	Slug string
	Kind FeatureKind
}

// Features is the vocabulary, in the order the story is told. The
// bundled components' slugs are exactly k3s's names for them, the
// same words the disable list uses.
var Features = []FeatureDefinition{
	{Slug: "traefik", Kind: FeatureBundled},
	{Slug: "servicelb", Kind: FeatureBundled},
	{Slug: "metrics-server", Kind: FeatureBundled},
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

// EnabledFeatures returns the slugs this cluster opts into, sorted.
// A nil Cluster (a machine with no cluster document) enables nothing,
// which is how the minimum viable cluster stays the default.
func (c *Cluster) EnabledFeatures() []string {
	if c == nil {
		return nil
	}
	return slices.Sorted(maps.Keys(c.Spec.Features))
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

// validateFeatures holds spec.features to the vocabulary. ParseCluster
// calls it, so every file door (init vetting a staged or proven
// document, the operator hashing a rendered one) refuses the same
// mistakes the CRD refuses at admission, with messages that say what
// to write instead.
func validateFeatures(features map[string]*FeatureConfig) error {
	for _, slug := range slices.Sorted(maps.Keys(features)) {
		if FeatureBySlug(slug) == nil {
			return fmt.Errorf("spec.features: unknown feature %q (the vocabulary is %s)",
				slug, strings.Join(FeatureSlugs(), ", "))
		}
		if features[slug] == nil {
			return fmt.Errorf("spec.features: %s is null, which in Kubernetes means unset; presence is the opt-in, so write %q to enable the feature or remove the key entirely",
				slug, slug+": {}")
		}
	}
	return nil
}
