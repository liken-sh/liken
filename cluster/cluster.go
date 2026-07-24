// Package cluster is the Cluster API: the document that describes
// what the machines form together, as Go types.
//
// A Machine describes one computer. A Cluster describes the group.
// The choice of where a fact goes depends on who must agree on it.
// Every fact in a ClusterSpec is one of two kinds. Either every node
// must hold the fact identically (which machines run control
// planes, the address ranges pods and services live in), or the
// fact belongs to the group and no single machine owns it (the
// endpoint followers join through). Every fact specific to one
// machine (its interfaces, its addresses, its disks) stays on the
// Machine. The two document packages never import each other. The
// grammar they share (ObjectMeta, Phase, Condition, Role) lives in
// the api package underneath both.
//
// Like the Machine manifest, the Cluster manifest arrives as a file
// in the image. The liken operator seeds it into the cluster as a
// custom resource once the API is up. Unlike the Machine manifest,
// every machine's image includes the same cluster.yaml. The document
// is cluster-scoped, so there is exactly one. liken already treats
// "same image" as "same cluster": the image carries the cluster's CA
// and join token.
//
// The type names repeat a word (cluster.ClusterSpec), for the same
// reason the machine package's type names do. They mirror the CRD
// kind and Kubernetes' XxxSpec/XxxStatus convention. Matching what
// `kubectl explain cluster.spec` shows is worth more than avoiding
// the repeated word.
package cluster

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"

	"github.com/liken-sh/liken/api"
	"sigs.k8s.io/yaml"
)

// ClusterManifestPath is where the image carries the cluster's
// manifest. Init reads it to learn this machine's role. The operator
// reads the same file through a hostPath mount, to seed the
// in-cluster Cluster resource.
const ClusterManifestPath = "/etc/liken/cluster.yaml"

// BootClusterManifestPath is where init publishes the cluster
// document this boot actually derived its role from: the staged or
// proven copy from machineState, or the image's seed on a first
// boot. The operator needs the bytes, not only their hash, because
// drift detection compares documents by meaning. A hand-written seed
// and the operator's canonical rendering of the same spec are
// different bytes that say the same thing. A formatting difference
// must never reboot the fleet.
const BootClusterManifestPath = "/run/liken/cluster.yaml"

type Cluster struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   api.ObjectMeta `json:"metadata"`
	Spec       ClusterSpec    `json:"spec,omitzero"`
	Status     ClusterStatus  `json:"status,omitzero"`
}

// ClusterOrigin records how the cluster's datastore came to exist.
// Founded means liken created the datastore, through the founding
// leader's cluster-init. Adopted means the datastore already existed
// in a cluster liken did not create, and liken machines join it as
// members instead of starting one. This distinction matters in one
// place: the founding leader's datastore decision (init/k3s.go). An
// adopted cluster's founder joins like every other machine, because
// initializing a second datastore next to a live one would split the
// cluster in two.
// The values are CamelCase because Kubernetes API constants are
// CamelCase, the same convention the phase and policy vocabularies
// follow.
type ClusterOrigin string

const (
	OriginFounded ClusterOrigin = "Founded"
	OriginAdopted ClusterOrigin = "Adopted"
)

// ClusterStatus is what a reader can observe about the cluster as a
// whole. The cluster operator writes it (see cluster-operator/fleet.go);
// observing the fleet is that program's whole job. The staged and
// proven copies of the Cluster document carry spec only. Status
// never appears in the lifecycle bytes, because observations are not
// part of the document's identity.
//
// The conditions are the real observations, and the phase is their
// one-word summary, the same arrangement the Machine uses. Phase is
// Ready when every machine is Ready. Phase is Updating when the only
// machines not Ready are mid-transition: rebooting into a change,
// waiting on one, or booting. Phase is Degraded when any machine is
// Lost, Blocked, or otherwise unhealthy. This status can never show
// one state: quorum loss. Losing a majority of leaders takes the API
// server down with it, so nobody is left to write the status. When
// quorum is lost, the symptom is a status that stops updating.
type ClusterStatus struct {
	Phase    api.Phase             `json:"phase,omitempty"`
	Machines MachineTally          `json:"machines,omitzero"`
	Releases ClusterReleasesStatus `json:"releases,omitzero"`
	Flux     ClusterFluxStatus     `json:"flux,omitzero"`

	// ObservedGeneration is the metadata.generation of the spec that
	// this status judged, stamped by the sweep on every write. The
	// conditions each carry the same stamp, but a client that only
	// asks "has any controller seen my edit yet" should not have to
	// parse conditions to learn it. Kubernetes controllers publish
	// this field at the top of status for exactly that question, and
	// `kubectl rollout status` and the common wait libraries read it
	// there.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	Conditions []api.Condition `json:"conditions,omitempty"`
}

// ClusterReleasesStatus is what the sweep observes about releases.
// Newest is the catalog's highest version: spec-local, with no
// network call. Available is the version the channel's own document
// announces as its latest, learned by polling <source>/channel.yaml.
// It may name a version the catalog does not hold yet, which is
// exactly the signal it exists to surface. Available is advisory by
// design: adopting a release still means committing a digest-pinned
// catalog entry. Like every status field, both values are derived.
// They exist so printer columns can show the whole story next to
// spec.version, without anyone comparing versions at the terminal.
type ClusterReleasesStatus struct {
	Newest    string `json:"newest,omitempty"`
	Available string `json:"available,omitempty"`
}

// ClusterFluxStatus is the flux feature's observable half.
// PublicKey is the fleet's deploy key: the public half, in the
// authorized_keys form a forge accepts. The private half lives only
// in the flux-system Secret, minted there by the cluster operator
// (cluster-operator/flux.go), so nobody ever handles private
// material to set the sync up: they register this value at the
// forge, and the fleet does the rest. An empty status means the
// feature is not declared, or the key is not minted yet.
type ClusterFluxStatus struct {
	PublicKey string `json:"publicKey,omitempty"`
}

// MachineTally counts the cluster's Machines: how many are fully
// healthy (phase Ready, with a fresh heartbeat) out of how many
// exist. Summary holds the same two numbers formatted as "4/5". The
// struct stores this because a CRD printer column can read one
// field, but cannot combine two.
type MachineTally struct {
	Ready   int    `json:"ready"`
	Total   int    `json:"total"`
	Summary string `json:"summary,omitempty"`
}

type ClusterSpec struct {
	// Origin is how the cluster's datastore came to exist: Founded
	// (the default when unset) or Adopted. An adopted cluster is one
	// liken is migrating into, rather than one it created. Its
	// machines join an existing datastore through the endpoint, and
	// no leader ever initializes a new one. The one legal edit is
	// the promotion, Adopted to Founded, made once the last foreign
	// member is gone. After that edit, a rebuild from scratch
	// behaves like any founded cluster, with the founder running
	// cluster-init.
	Origin ClusterOrigin `json:"origin,omitempty"`

	// Leaders names the machines that run control planes, by their
	// Machine names. A machine's role is derived, never declared: a
	// machine is a leader exactly when its name appears here.
	// Promoting a follower is therefore one Cluster edit, not a
	// coordinated pair of Machine edits. One name means k3s keeps
	// its state in sqlite. More than one name means embedded etcd,
	// whose majority quorum calls for an odd count of leaders:
	// three, not two or four. No admission rule enforces the odd
	// count, on purpose. Growing from one leader to three in a
	// single edit never passes through two, and a transient even
	// state is not worth rejecting.
	Leaders []string `json:"leaders,omitempty"`

	// Endpoint is the URL followers join the cluster through, for
	// example https://10.10.0.1:6443. The system uses it for first
	// contact only. After a follower joins, k3s agents keep a
	// client-side load balancer that learns every leader's address,
	// so a dead endpoint strands only brand-new followers, never
	// running ones. (Followers' time queries ask each leader by its
	// own address and bypass the endpoint entirely.) Endpoint is a
	// single, explicit input, on purpose. An endpoint that should
	// outlive any single leader, such as a DNS name or a virtual IP,
	// is a choice for the deployment to make, never the OS.
	Endpoint string `json:"endpoint,omitempty"`

	// Network holds the network facts that k3s requires every node
	// to agree on. These are cluster-scoped facts, and declaring
	// them per node would misstate them.
	Network ClusterNetworkSpec `json:"network,omitzero"`

	// Time is the cluster's time hierarchy: where the leaders get
	// their time. It lives on the Cluster for the same reason the
	// network plan does. Clocks are a fact the whole fleet must
	// agree on, and TLS, and with it the cluster itself, stops
	// working when they do not agree.
	Time ClusterTimeSpec `json:"time,omitzero"`

	// Disruption bounds how much of the fleet may be down at once
	// when the cluster sequences reboots (cluster-operator/rollout.go).
	Disruption ClusterDisruptionSpec `json:"disruption,omitzero"`

	// Features is the cluster's set of opt-ins from liken's curated
	// feature vocabulary (features.go): optional capabilities that
	// the fleet as a whole offers. It lives on the Cluster because a
	// feature is a fact every node must agree on. A PersistentVolume
	// can attach to any node the scheduler picks, and even the k3s
	// disable list applies cluster-wide. Features is an object keyed
	// by feature slug, rather than a list of names, so a feature can
	// grow parameters without breaking the schema. The key's
	// presence is the opt-in, and a feature's zero configuration is
	// {}. The value is a pointer, so an explicit null arrives as
	// present-but-nil, and validateFeatures refuses it loudly.
	// Everywhere else in Kubernetes, null means unset, so a bare
	// `traefik:` in a hand-written manifest must be an error. It
	// must never become a quiet enable, or an even quieter no-op.
	// Features converge by reboot like every other cluster fact.
	// They stay in the canonical staged document, so an edit changes
	// the document's hash and rolls through the fleet as staged
	// changes and granted reboots.
	Features map[string]*FeatureConfig `json:"features,omitempty"`

	// Runtime is the Go runtime discipline the cluster imposes on the
	// k3s process init launches (runtime.go). It shapes only that
	// environment: containerd and the shims inherit it, and no other
	// process reads it. Like features and registries, k3s reads these
	// values only when its process starts, so an edit converges by
	// restarting k3s in place, not by rebooting.
	Runtime ClusterRuntimeSpec `json:"runtime,omitzero"`

	// Registries is how container images arrive on the fleet's
	// machines: mirror endpoints that containerd pulls through, and
	// k3s's embedded peer-to-peer registry. It lives on the Cluster
	// because any node may be asked to pull any image, so how images
	// arrive is a fact the whole fleet must agree on. Credentials
	// are deliberately not here. A spec is public, so credentials
	// enter through the registry-credentials Secret instead
	// (machine/registries.go tells that story). Like features,
	// registries stay in the canonical staged document. Both are
	// read only when the k3s process starts, so their edits converge
	// by restarting k3s in place, instead of rebooting the machine
	// (changes.go).
	Registries RegistriesSpec `json:"registries,omitzero"`

	// Version is the fleet's target liken release: the one field an
	// upgrade edits. Machines carry no version in their specs.
	// Instead, each machine's operator compares the version its boot
	// reported (status.version.liken) against this target, live, and
	// moves toward it through the same staged-change-and-granted-reboot
	// machinery that applies every other change. Declaring the
	// version here, and only here, makes an upgrade one edit instead
	// of one edit per machine.
	Version string `json:"version,omitempty"`

	// Releases is where release artifacts come from, and which
	// releases exist: the catalog that Version must name its target
	// in. An admission rule on the CRD enforces that requirement, so
	// the system rejects a mistyped target at admission, instead of
	// blocking machines.
	Releases ClusterReleasesSpec `json:"releases,omitzero"`
}

// ClusterReleasesSpec is the cluster's release feed. The system
// reads Version and Releases live, on purpose: the operator reads
// them from the in-cluster resource on every pass, and the canonical
// cluster document that machines stage and reboot into excludes them
// (machine-operator/cluster.go's renderCluster). Because of this
// split, publishing a release or retargeting the fleet moves
// machines through downloads and sequenced reboots. It never stages
// a fleet-wide configuration change of its own.
type ClusterReleasesSpec struct {
	// Source is the base URL releases are served under. A release's
	// artifacts live at <source>/<version>/, starting with
	// release.yaml, the document that names every artifact by digest.
	Source string `json:"source,omitempty"`

	// Catalog is the set of releases machines may be asked to run.
	// The digest is the sha256 of release.yaml's exact bytes, and it
	// is the root of the trust chain: the API names the document,
	// the document names the artifacts, and the system checks every
	// downloaded byte against one or the other.
	Catalog []ReleaseCatalogEntry `json:"catalog,omitempty"`
}

// CheckReleasesAnnotation is the annotation that requests an
// immediate channel poll. The fleet observer polls the channel's
// root document on a lazy interval. Setting this annotation on the
// Cluster to any new value, such as a timestamp (the content itself
// means nothing), makes the very next sweep poll immediately. This
// is an annotation, not a spec field, because it asks for one
// action rather than declaring a standing state. kubectl requests a
// Deployment rollout with the restartedAt annotation in the same
// shape.
const CheckReleasesAnnotation = "liken.sh/check-releases"

// ReleaseCatalogEntry names one release: its version and the digest
// of its release document, as "sha256:<64 hex digits>".
type ReleaseCatalogEntry struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// Entry finds one version's catalog entry. It returns nil when the
// catalog does not list the version. The CRD requires spec.version
// to be a catalog member at admission, so this lookup should always
// succeed for the target version. Callers still handle nil, rather
// than rely on that admission rule.
func (s ClusterReleasesSpec) Entry(version string) *ReleaseCatalogEntry {
	for i := range s.Catalog {
		if s.Catalog[i].Version == version {
			return &s.Catalog[i]
		}
	}
	return nil
}

// NewestVersion is the catalog's highest version. It returns "" when
// the catalog is empty. The fleet sweep publishes this value as
// status.releases.newest, so a printer column can answer "is there
// something newer than the target?" at a glance. The ordering itself
// belongs to the version grammar (api.CompareVersions).
func NewestVersion(catalog []ReleaseCatalogEntry) string {
	newest := ""
	for _, entry := range catalog {
		if newest == "" || api.CompareVersions(entry.Version, newest) > 0 {
			newest = entry.Version
		}
	}
	return newest
}

// ClusterDisruptionSpec is the machine-level equivalent of a
// workload's PodDisruptionBudget, reduced to the one number that
// matters for a fleet: how many machines may be voluntarily down at
// the same time. It governs only disruptions the cluster chooses,
// such as rolling reboots that apply staged changes. It cannot
// promise anything about machines that fail on their own. The
// rollout does count failed machines against the budget, though, so
// a fleet that is already losing machines pauses its own rollout.
type ClusterDisruptionSpec struct {
	// MaxUnavailable is how many machines may be unavailable at
	// once, planned and unplanned together. Zero means unset, and
	// the system defaults it to 1. One machine at a time is the
	// safest rollout, and the right answer for small fleets. The
	// leaders keep a stricter, automatic floor, regardless of this
	// number: only one leader may ever be down at a time. That floor
	// is not a policy choice. Losing more leaders at once could cost
	// the datastore its majority quorum, and no budget setting can
	// change that arithmetic.
	MaxUnavailable int `json:"maxUnavailable,omitempty"`
}

// MaxUnavailableOrDefault applies the default: an unset budget is 1.
func (d ClusterDisruptionSpec) MaxUnavailableOrDefault() int {
	if d.MaxUnavailable < 1 {
		return 1
	}
	return d.MaxUnavailable
}

// ClusterTimeSpec declares where time comes from. Only the leaders
// consult it. Followers sync from the leaders themselves, resolved
// from the fleet's Machine manifests, with the endpoint as the
// fallback. The hierarchy is therefore upstreams, then leaders, then
// everyone else.
type ClusterTimeSpec struct {
	// Upstreams are the NTP servers the cluster's leaders sync from,
	// as hostnames or addresses. There is no default. A distro that
	// shipped pool.ntp.org here would enroll every deployment's
	// machines in a volunteer-run service without asking. An empty
	// list is legal and means the fleet free-runs. The machines stay
	// consistent with each other, but they are correct only if the
	// hardware clocks happen to be correct too.
	Upstreams []string `json:"upstreams,omitempty"`
}

// ClusterNetworkSpec is the cluster's address plan. Every field is
// optional. Whatever is left unset falls back to k3s's default. The
// value of writing a field here is that every node provably agrees
// on it.
type ClusterNetworkSpec struct {
	// NodeCIDR is the subnet the nodes address each other on: the
	// network all cluster traffic crosses. A machine may have
	// several interfaces, such as an internet uplink and a
	// management port. Kubernetes traffic uses the interface whose
	// address falls inside this subnet, and that address becomes
	// the machine's node IP.
	NodeCIDR string `json:"nodeCIDR,omitempty"`

	// ClusterCIDR is the range pod addresses are drawn from
	// (k3s default 10.42.0.0/16).
	ClusterCIDR string `json:"clusterCIDR,omitempty"`

	// ServiceCIDR is the range service addresses are drawn from
	// (k3s default 10.43.0.0/16).
	ServiceCIDR string `json:"serviceCIDR,omitempty"`

	// ClusterDNS is the service address of the cluster's DNS resolver,
	// which must sit inside ServiceCIDR (k3s default 10.43.0.10).
	ClusterDNS string `json:"clusterDNS,omitempty"`

	// ClusterDomain is the DNS suffix cluster-internal names live
	// under (k3s default cluster.local).
	ClusterDomain string `json:"clusterDomain,omitempty"`
}

// Role is what a machine should be in this cluster. A nil Cluster
// answers leader. A machine with no cluster manifest is on its own,
// and a machine on its own runs as its own single-node cluster,
// which is liken's default arrangement.
func (c *Cluster) Role(machineName string) api.Role {
	if c == nil || slices.Contains(c.Spec.Leaders, machineName) {
		return api.RoleLeader
	}
	return api.RoleFollower
}

// ParseCluster reads a Cluster manifest from its bytes, strictly,
// for the same reason Machine parsing is strict. A misspelled field
// should produce an error someone sees, rather than become a setting
// that silently never applies.
func ParseCluster(raw []byte) (*Cluster, error) {
	c := &Cluster{}
	if err := yaml.UnmarshalStrict(raw, c); err != nil {
		return nil, err
	}
	if c.Kind != "Cluster" {
		return nil, fmt.Errorf("expected kind Cluster, got %q", c.Kind)
	}
	if err := validateFeatures(c.Spec.Features); err != nil {
		return nil, err
	}
	if err := validateRegistries(c.Spec.Registries); err != nil {
		return nil, err
	}
	if err := c.Spec.Runtime.Validate(); err != nil {
		return nil, fmt.Errorf("spec.runtime.k3s: %w", err)
	}
	return c, nil
}

// RuntimeSpec is the k3s runtime section, safe on a nil Cluster. A
// machine on its own, with no cluster document, gets the zero section,
// which resolves to the same defaults as an unset section.
func (c *Cluster) RuntimeSpec() K3sRuntimeSpec {
	if c == nil {
		return K3sRuntimeSpec{}
	}
	return c.Spec.Runtime.K3s
}

// LoadCluster reads the Cluster manifest from a file. A machine with
// no cluster manifest gets nil, which is a valid, single-node
// arrangement (see Role). A manifest that exists but does not parse
// is a configuration error, and the function reports it as one.
func LoadCluster(path string) (*Cluster, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c, err := ParseCluster(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}
