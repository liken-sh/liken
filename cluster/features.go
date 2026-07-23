package cluster

// The feature vocabulary: the curated set of optional capabilities a
// cluster may opt into through spec.features (see ClusterSpec).
//
// liken is a minimum viable highly-available cluster, and
// capabilities that people may not need should not run on every
// deployment. But an OS must be able to offer more than its minimum,
// so the Cluster document carries a vocabulary of optional features,
// and this table is that vocabulary. Deployments name features. What
// a feature is made of is liken's concern, recorded here. Everything
// that must agree on the vocabulary agrees by consulting this table.
// Init validates the cluster document against it and renders the k3s
// disable list from it. The operator judges each machine's standing
// against it. A parity test holds the hand-written CRD
// (cluster/manifests/clusters-crd.yaml) to exactly these slugs.
//
// A feature is a slug plus any subset of these contributions:
//
//   - k3s configuration rendered at boot: today, membership in the
//     disable list that init computes into the leader's boot
//     drop-in (init/k3s.go).
//   - Vendored userspace binaries: a top-level domain in this
//     repository, with a pinned VERSION and a fetch.sh that produces
//     sha256-verified static binaries. These ship in every image and
//     stay inert until declared.
//   - Kernel modules: the domain's modules.conf, staged into the
//     image at /etc/liken/features/<slug>/modules.conf and loaded by
//     init only when the feature is declared. That file's presence
//     is also how init knows the booted image carries the payload,
//     so no feature-to-modules mapping lives in Go.
//   - Workload manifests and OCI images: these ride the image, and
//     init seeds them into k3s's auto-deploy directory only when the
//     feature is declared.
//   - An init boot hook: per-slug code gated on declaration, for the
//     few features with a boot-time contribution, such as an iSCSI
//     initiator name.
//   - Parameters: the feature's configuration object, empty for
//     every feature today (see FeatureConfig).
//
// Payloads ship in every image because they are small. Enabling a
// feature is then purely a runtime act: one Cluster edit that rolls
// through the fleet as staged changes and granted reboots, never an
// image rebuild. A feature too large to ship unconditionally, such
// as a GPU toolkit, would be the moment to introduce build-time
// conditioning, and not before.

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// FeatureKind is how a feature is delivered. Deployments never see
// this distinction. The machinery needs it, to know what enabling a
// feature actually does.
type FeatureKind string

const (
	// FeatureBundled is a component the k3s binary already carries:
	// Traefik, the service load balancer, and metrics-server. The
	// image ships it whether or not anyone wants it, so opting in
	// costs nothing at build time. The whole actuation is init
	// leaving the component out of the disable list it renders into
	// the k3s boot drop-in.
	FeatureBundled FeatureKind = "Bundled"

	// FeatureEmbedded is a controller compiled into the k3s server
	// process itself, rather than a component k3s deploys into the
	// cluster. Two features use this kind: the Helm controller,
	// which watches HelmChart resources and renders them into
	// workloads, and the network policy controller, which turns
	// NetworkPolicy resources into packet filtering, something the
	// flannel CNI cannot do alone. Each costs memory in the k3s
	// process whether or not any of its resources exist. An embedded
	// feature can never appear on the disable list, because that
	// list names only deployable components. Its actuation is a
	// dedicated disable key that init renders into the leader's boot
	// drop-in (init/k3s.go), and init omits that key only when the
	// feature is enabled.
	FeatureEmbedded FeatureKind = "Embedded"

	// FeatureVendored is a payload this repository vendors as a
	// top-level domain: static binaries, kernel modules, and
	// sometimes a workload manifest. Every image includes the payload.
	// When /etc/liken/features/<slug>/modules.conf exists in the
	// booted image, init detects that the image has the payload. That
	// check matters when an older image boots against a cluster
	// document that declares a feature the image predates: the
	// machine reports the gap, instead of silently lacking the
	// capability.
	FeatureVendored FeatureKind = "Vendored"

	// FeatureWorkload is a capability that runs entirely as cluster
	// workloads: manifests that ride the image and seed into k3s's
	// auto-deploy directory when the feature is declared, with no
	// vendored host binaries and no kernel modules. The workload's
	// container images are ordinary registry pulls, not baked
	// payloads, so the feature needs no image-side presence check:
	// an image whose vocabulary knows the slug also carries its
	// manifests, because one build produces both. flux takes this
	// shape.
	FeatureWorkload FeatureKind = "Workload"
)

// FeatureDefinition names one feature: its slug, which is the key
// deployments write in spec.features, and its kind. Requires names
// the features this one cannot work without. Enabling a feature
// enables everything it requires, so a deployment declares what it
// wants and never has to know the dependency exists. Params names
// the parameters the feature's configuration accepts; most features
// have none, and their configuration is exactly {}. The CRD holds a
// parameterized feature to these names at admission, the parity
// test holds the CRD to this table, and ValidateParams is the same
// judgment at the file doors.
type FeatureDefinition struct {
	Slug     string
	Kind     FeatureKind
	Requires []string
	Params   []string
}

// Features is the vocabulary, listed in the order that explains it
// best. The bundled components' slugs are exactly k3s's names for
// them, the same words the disable list uses. A vendored feature's
// slug names the capability (iscsi), never the project that
// implements it (open-iscsi), because an implementation can change
// and an API should not have to change with it.
var Features = []FeatureDefinition{
	{Slug: "traefik", Kind: FeatureBundled, Requires: []string{"helm"}},
	{Slug: "servicelb", Kind: FeatureBundled},
	{Slug: "metrics-server", Kind: FeatureBundled},
	{Slug: "helm", Kind: FeatureEmbedded},
	{Slug: "network-policy", Kind: FeatureEmbedded},
	{Slug: "iscsi", Kind: FeatureVendored},
	{Slug: "nfs", Kind: FeatureVendored},
	// flux names the project, not the capability, and this is a
	// deliberate exception to the naming rule above. The rule exists
	// so an implementation can change behind a stable name, and for
	// iscsi that holds: the kernel interface is the capability, and
	// open-iscsi could be swapped behind it. A GitOps engine is
	// different. Its in-cluster resources and its repository
	// conventions are the interface the deployment builds its whole
	// repository against, so a generic gitops slug would promise a
	// swappability the design could never honor. A deployment that
	// needs a different engine needs a different feature.
	{Slug: "flux", Kind: FeatureWorkload,
		Params: []string{"repository", "path", "branch"}},
}

// FeatureConfig is one feature's configuration: the object under its
// slug in spec.features. {} is every feature's zero configuration,
// and a parameterized feature's parameters are its keys. The type is
// a plain map on purpose, never a struct of named fields. Each
// machine's binary knows only the parameter vocabulary its image was
// built with, and a fleet mid-upgrade holds several of those
// vocabularies at once. A struct parsed strictly would refuse a
// document from a newer vocabulary, and then a downgraded machine
// could not read its own proven document, could not derive its role,
// and would sit Blocked. So the file doors accept any parameters,
// and the judgment happens where a verdict can be reported: the CRD
// refuses a parameter its vocabulary does not know at admission, and
// init's feature pass reports a parameter this image cannot honor
// (ValidateParams below), leaving the machine degraded rather than
// down.
type FeatureConfig map[string]any

// ValidateParams holds one feature's configuration to this
// definition's parameter vocabulary: every key must be a declared
// parameter, and every value must be a string, the one value type
// the vocabulary uses. A nil configuration validates like {}; the
// null refusal belongs to validateFeatures, at parse time. The error
// names both possible causes, because an unknown parameter reads the
// same from here whether a newer vocabulary defined it or a
// hand-written seed misspelled it.
func (def *FeatureDefinition) ValidateParams(cfg *FeatureConfig) error {
	if cfg == nil {
		return nil
	}
	for _, key := range slices.Sorted(maps.Keys(*cfg)) {
		if !slices.Contains(def.Params, key) {
			offers := "no parameters"
			if len(def.Params) > 0 {
				offers = strings.Join(def.Params, ", ")
			}
			return fmt.Errorf(
				"%s: this image's vocabulary has no %q parameter; upgrade to a release that carries it, or fix the name if it is a misspelling (this image offers: %s)",
				def.Slug, key, offers)
		}
		if _, ok := (*cfg)[key].(string); !ok {
			return fmt.Errorf("%s: %s must be a string", def.Slug, key)
		}
	}
	return nil
}

// FeatureFlux is the GitOps feature's slug: the declaration that the
// fleet's workloads and configuration sync from a git repository
// through Flux.
const FeatureFlux = "flux"

// The sync defaults: the repository's root, on the branch most
// forges create by default. A deployment that keeps several clusters
// in one repository sets path to its cluster's directory instead.
const (
	FluxDefaultPath   = "."
	FluxDefaultBranch = "main"
)

// FluxConfig is the flux feature's configuration, typed: where the
// fleet's declared state lives, and which part of it this cluster
// syncs.
type FluxConfig struct {
	Repository string
	Path       string
	Branch     string
}

// FluxConfig reads the flux feature's declaration, applies the sync
// defaults, and requires the one parameter that has no default: the
// repository. It returns nil with no error when the cluster does not
// declare the feature; that is the ordinary state, not a mistake. An
// empty string counts as unset, so `path: ""` gets the default
// rather than an impossible sync path.
func (c *Cluster) FluxConfig() (*FluxConfig, error) {
	if c == nil {
		return nil, nil
	}
	cfg, declared := c.Spec.Features[FeatureFlux]
	if !declared {
		return nil, nil
	}
	def := FeatureBySlug(FeatureFlux)
	if err := def.ValidateParams(cfg); err != nil {
		return nil, err
	}
	out := &FluxConfig{Path: FluxDefaultPath, Branch: FluxDefaultBranch}
	if cfg != nil {
		if s, _ := (*cfg)["repository"].(string); s != "" {
			out.Repository = s
		}
		if s, _ := (*cfg)["path"].(string); s != "" {
			out.Path = s
		}
		if s, _ := (*cfg)["branch"].(string); s != "" {
			out.Branch = s
		}
	}
	if out.Repository == "" {
		return nil, fmt.Errorf(
			"flux: repository is required: the git URL the fleet's declared state syncs from, for example ssh://git@forge.example/fleet.git")
	}
	return out, nil
}

// FeatureBySlug finds one feature's definition. It returns nil when
// the vocabulary does not include the slug.
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
// enable, sorted. This is the declared opt-ins plus everything they
// require. For example, traefik pulls in helm, because k3s deploys
// Traefik through a HelmChart resource that only the Helm controller
// can render. The closure runs to a fixed point, so a requirement's
// own requirements are honored too. A nil Cluster, a machine with no
// cluster document, enables nothing. This is how the minimum viable
// cluster stays the default.
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
// defined somewhere else. The rendered value therefore has exactly
// one author: init, which writes it into the boot drop-in on leaders
// (init/k3s.go). A nil Cluster disables everything bundled, which
// keeps the same default for a machine on its own.
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
// calls it, so every file door, such as init vetting a staged or
// proven document, or the operator hashing a rendered one, refuses a
// null the same way the CRD refuses it at admission. Each refusal
// carries a message that says what to write instead.
//
// An unknown slug is deliberately not an error here, though the CRD
// refuses one at admission. The difference is what each door knows.
// A fleet has exactly one vocabulary at its API, the newest image's
// CRD. But each machine's parser knows only the vocabulary its own
// image was built with, and a fleet mid-upgrade holds several of
// those vocabularies at once. A document that declares a feature
// this binary predates must still parse. Otherwise the machine could
// not read its own proven document after a downgrade, could not
// derive its role, and would sit Blocked on a document the rest of
// the fleet is running without trouble. The feature pass reports the
// unknown slug instead: FeaturesReady goes False, naming the slug
// and this image's vocabulary. This message covers both real causes,
// an image that predates the feature and a misspelling in a
// hand-written seed, and it leaves the machine degraded rather than
// down.
func validateFeatures(features map[string]*FeatureConfig) error {
	for _, slug := range slices.Sorted(maps.Keys(features)) {
		if features[slug] == nil {
			return fmt.Errorf("spec.features: %s is null, which in Kubernetes means unset; presence is the opt-in, so write %q to enable the feature or remove the key entirely",
				slug, slug+": {}")
		}
	}
	return nil
}
