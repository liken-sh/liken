package machine

// The Cluster API is the document that describes what the machines
// form together.
//
// A Machine describes one computer; a Cluster describes the group.
// What goes where is decided by who has to agree on it. Everything in
// a ClusterSpec is either a fact that every node must hold identically
// (which machines run control planes, the address ranges pods and
// services live in) or a fact about the group that no single machine
// owns (the endpoint followers join through). Everything specific to
// one machine (its interfaces, its addresses, its disks) stays on the
// Machine.
//
// Like the Machine manifest, the Cluster manifest is delivered as a
// file in the image and seeded into the cluster as a custom resource
// by the liken operator once the API is up. Unlike the Machine
// manifest, the same cluster.yaml is included in every machine's
// image. The document is cluster-scoped, so there is exactly one, and
// liken already equates "same image" with "same cluster": the image
// carries the cluster's CA and join token.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"

	"sigs.k8s.io/yaml"
)

// ClusterManifestPath is where the image carries the cluster's
// manifest. Init reads it to learn this machine's role; the operator
// reads the same file through a hostPath mount to seed the in-cluster
// Cluster resource.
const ClusterManifestPath = "/etc/liken/cluster.yaml"

// Role is what a machine is in its cluster. There are exactly two:
// leaders run a control plane (an API server, a scheduler, the
// datastore), followers run workloads and take direction from the
// leaders. k3s calls these "server" and "agent". liken translates in
// exactly one place, the moment it execs k3s (supervisor.go), and
// uses leader/follower everywhere else.
type Role string

const (
	RoleLeader   Role = "leader"
	RoleFollower Role = "follower"
)

type Cluster struct {
	APIVersion string        `json:"apiVersion"`
	Kind       string        `json:"kind"`
	Metadata   ObjectMeta    `json:"metadata"`
	Spec       ClusterSpec   `json:"spec,omitzero"`
	Status     ClusterStatus `json:"status,omitzero"`
}

// ClusterOrigin records how the cluster's datastore came to exist.
// Founded means liken created it, via the founding leader's
// cluster-init. Adopted means the datastore already existed in a
// cluster liken did not create, and liken machines join it as
// members rather than starting one. The distinction matters in one
// place, the founding leader's datastore decision (init/k3s.go): an
// adopted cluster's founder joins like everyone else, because
// initializing a second datastore next to a live one would split
// the cluster in two.
type ClusterOrigin string

const (
	OriginFounded ClusterOrigin = "founded"
	OriginAdopted ClusterOrigin = "adopted"
)

// ClusterStatus is what can be observed about the cluster as a whole,
// written by the leaders (see operator/fleet.go): they are the only
// machines positioned to observe the fleet, because a follower that
// can reach the API is by definition reaching a leader. The staged
// and proven copies of the Cluster document carry spec only. Status
// never appears in the lifecycle bytes, because observations aren't
// part of the document's identity.
//
// The conditions are the real observations and the phase is their
// one-word summary, the same arrangement the Machine uses: Ready when
// every machine is Ready, Updating when the only machines not Ready
// are mid-transition (rebooting into a change, waiting on one, or
// booting), and Degraded when any machine is Lost, Blocked, or
// otherwise unhealthy. One state this status can never show is quorum
// loss. Losing a majority of leaders takes the API server down with
// it, so there is nobody left to write the status; when quorum is
// lost, the symptom is a status that stops updating.
type ClusterStatus struct {
	Phase      Phase                 `json:"phase,omitempty"`
	Machines   MachineTally          `json:"machines,omitzero"`
	Releases   ClusterReleasesStatus `json:"releases,omitzero"`
	Conditions []Condition           `json:"conditions,omitempty"`
}

// ClusterReleasesStatus is what the sweep observes about the release
// catalog. Newest is the catalog's highest version. Like every status
// field it is derived, and it exists so a printer column can show
// "the catalog offers 0.3.0" next to "the target is 0.2.0" without
// anyone comparing versions at the terminal.
type ClusterReleasesStatus struct {
	Newest string `json:"newest,omitempty"`
}

// MachineTally counts the cluster's Machines: how many are fully
// healthy (phase Ready with a fresh heartbeat) out of how many exist.
// Summary holds the same two numbers formatted as "4/5", stored
// because a CRD printer column can read one field but can't combine
// two.
type MachineTally struct {
	Ready   int    `json:"ready"`
	Total   int    `json:"total"`
	Summary string `json:"summary,omitempty"`
}

type ClusterSpec struct {
	// Origin is how the cluster's datastore came to exist: founded
	// (the default when unset) or adopted. An adopted cluster is one
	// liken is migrating into rather than one it created: the
	// machines join an existing datastore through the endpoint, and
	// no leader ever initializes a new one. The one legal edit is
	// the promotion, adopted to founded, made once the last foreign
	// member is gone; after it, a rebuild from scratch behaves like
	// any founded cluster, with the founder running cluster-init.
	Origin ClusterOrigin `json:"origin,omitempty"`

	// Leaders names the machines that run control planes, by their
	// Machine names. A machine's role is derived, never declared: it
	// is a leader exactly when its name appears here, so promoting a
	// follower is one Cluster edit, not a coordinated pair of Machine
	// edits. One name means k3s keeps its state in sqlite; more than
	// one means embedded etcd, whose majority quorum calls for an odd
	// count of leaders: three, not two or four. No admission rule
	// enforces the odd count, deliberately. Growing from one leader
	// to three in a single edit never passes through two, and a
	// transient even state is not worth rejecting.
	Leaders []string `json:"leaders,omitempty"`

	// Endpoint is the URL followers join the cluster through, e.g.
	// https://10.10.0.1:6443. It is used for first contact only:
	// after joining, k3s agents maintain a client-side load balancer
	// that learns every leader's address, so a dead endpoint strands
	// only brand-new followers, never running ones. (Followers' time
	// queries ask each leader by its own address and bypass the
	// endpoint entirely.) It is deliberately a single explicit input.
	// An endpoint that should outlive any single leader, such as a
	// DNS name or a virtual IP, is a choice for the deployment to
	// make, never the OS.
	Endpoint string `json:"endpoint,omitempty"`

	// Network holds the network facts k3s requires every node to agree
	// on: cluster-scoped truths that would otherwise masquerade as
	// per-node flags.
	Network ClusterNetworkSpec `json:"network,omitzero"`

	// Time is the cluster's time hierarchy: where the leaders get
	// their time. It lives on the Cluster for the same reason the
	// network plan does: clocks are a fact the whole fleet must agree
	// on, and TLS (and with it the cluster itself) stops working when
	// they don't.
	Time ClusterTimeSpec `json:"time,omitzero"`

	// Disruption bounds how much of the fleet may be down at once
	// when the cluster sequences reboots (operator/rollout.go).
	Disruption ClusterDisruptionSpec `json:"disruption,omitzero"`

	// Version is the fleet's target liken release: the one field an
	// upgrade edits. Machines carry no version in their specs.
	// Instead, each machine's operator compares the version its boot
	// reported (status.version.liken) against this target, live, and
	// moves toward it through the same staged-change-and-granted-reboot
	// machinery that applies every other change. Declaring the version
	// here and only here is what makes an upgrade one edit instead of
	// one per machine.
	Version string `json:"version,omitempty"`

	// Releases is where release artifacts come from and which
	// releases exist: the catalog Version must name its target in.
	// An admission rule on the CRD enforces that, so a mistyped
	// target is rejected at admission instead of blocking machines.
	Releases ClusterReleasesSpec `json:"releases,omitzero"`
}

// ClusterReleasesSpec is the cluster's release feed. Version and
// Releases are deliberately consumed live: the operator reads them
// from the in-cluster resource on every pass, and the canonical
// cluster document that machines stage and reboot into excludes them
// (operator/cluster.go's renderCluster). This split means publishing
// a release or retargeting the fleet moves machines through downloads
// and sequenced reboots, and never stages a fleet-wide configuration
// change of its own.
type ClusterReleasesSpec struct {
	// Source is the base URL releases are served under. A release's
	// artifacts live at <source>/<version>/, starting with
	// release.yaml, the document that names every artifact by digest.
	Source string `json:"source,omitempty"`

	// Catalog is the set of releases machines may be asked to run.
	// The digest is the sha256 of the release.yaml's exact bytes,
	// which is the root of the trust chain: the API names the
	// document, the document names the artifacts, and every byte
	// downloaded is checked against one or the other.
	Catalog []ReleaseCatalogEntry `json:"catalog,omitempty"`
}

// ReleaseCatalogEntry names one release: its version and the digest
// of its release document, as "sha256:<64 hex digits>".
type ReleaseCatalogEntry struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// Entry finds one version's catalog entry, nil when the catalog
// doesn't list it. The CRD requires spec.version to be a catalog
// member at admission, so for the target this lookup should always
// succeed. Callers still handle nil rather than relying on that
// admission rule.
func (s ClusterReleasesSpec) Entry(version string) *ReleaseCatalogEntry {
	for i := range s.Catalog {
		if s.Catalog[i].Version == version {
			return &s.Catalog[i]
		}
	}
	return nil
}

// ClusterDisruptionSpec is the machine-level analogue of a workload's
// PodDisruptionBudget, reduced to the one number that matters for a
// fleet: how many machines may be voluntarily down at the same time.
// It governs only disruptions the cluster chooses (rolling reboots to
// apply staged changes); it cannot promise anything about machines
// that fail on their own. The rollout does count failed machines
// against the budget, though, so a fleet that is already losing
// machines pauses its own rollout.
type ClusterDisruptionSpec struct {
	// MaxUnavailable is how many machines may be unavailable at once,
	// planned and unplanned together. Zero means unset and defaults
	// to 1: one machine at a time is the safest rollout and the right
	// answer for small fleets. The leaders have a stricter, automatic
	// floor regardless of this number: only one leader may ever be
	// down at a time. That floor is not a policy choice; losing more
	// leaders at once could cost the datastore its majority quorum,
	// and no budget setting can change that arithmetic.
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
// consult it: followers sync from the leaders themselves (resolved
// from the fleet's Machine manifests, with the endpoint as the
// fallback), so the hierarchy is upstreams, then leaders, then
// everyone else.
type ClusterTimeSpec struct {
	// Upstreams are the NTP servers the cluster's leaders sync from,
	// as hostnames or addresses. There is no default: a distro that
	// shipped pool.ntp.org here would sign every deployment's
	// machines up for a volunteer-run service without asking. An
	// empty list is legal and means the fleet free-runs: the machines
	// stay consistent with each other, but they are only correct if
	// the hardware clocks happen to be.
	Upstreams []string `json:"upstreams,omitempty"`
}

// ClusterNetworkSpec is the cluster's address plan. Every field is
// optional: whatever is left unset falls to k3s's defaults, and the
// value of writing one here is that every node provably agrees on it.
type ClusterNetworkSpec struct {
	// NodeCIDR is the subnet the nodes address each other on: the
	// network all cluster traffic crosses. A machine may have several
	// interfaces (an internet uplink, a management port); the one
	// whose address falls inside this subnet is the one Kubernetes
	// traffic uses, and its address becomes the machine's node IP.
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
// answers leader: a machine with no cluster manifest is on its own,
// and a machine on its own runs as its own single-node cluster, which
// is liken's default arrangement.
func (c *Cluster) Role(machineName string) Role {
	if c == nil || slices.Contains(c.Spec.Leaders, machineName) {
		return RoleLeader
	}
	return RoleFollower
}

// ParseCluster reads a Cluster manifest from its bytes, strictly, for
// the same reason Machine parsing is strict: a misspelled field
// should produce an error someone sees, rather than becoming a
// setting that silently never applies.
func ParseCluster(raw []byte) (*Cluster, error) {
	c := &Cluster{}
	if err := yaml.UnmarshalStrict(raw, c); err != nil {
		return nil, err
	}
	if c.Kind != "Cluster" {
		return nil, fmt.Errorf("expected kind Cluster, got %q", c.Kind)
	}
	return c, nil
}

// LoadCluster reads the Cluster manifest from a file. A machine with
// no cluster manifest gets nil, which is a valid, single-node
// arrangement (see Role). A manifest that exists but doesn't parse is
// a configuration error and is reported as one.
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
