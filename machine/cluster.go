package machine

// The Cluster API: the document that describes what the machines
// form together.
//
// A Machine describes one computer; a Cluster describes the group.
// The split follows who has to agree on what. Everything in a
// ClusterSpec is a fact that every node must hold identically (which
// machines run control planes, the address ranges pods and services
// live in) or a fact about the group that no single machine owns (the
// endpoint followers join through). Everything per-machine (this NIC,
// this address, these disks) stays on the Machine.
//
// Like the Machine manifest, the Cluster manifest is delivered as a
// file in the image and seeded into the cluster as a custom resource
// by the liken operator once the API is up. Unlike the Machine
// manifest, the same cluster.yaml rides in every machine's image:
// it's cluster-scoped truth, so there is exactly one, and "same image
// = same cluster" is already how liken's identity works (the image
// carries the cluster's CA and join token).

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
// leaders. k3s calls these "server" and "agent"; liken translates at
// exactly one place, the moment it execs k3s (supervisor.go), and
// speaks leader/follower everywhere else.
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

// ClusterStatus is what can be observed about the cluster as a whole,
// written by the leaders (see operator/fleet.go): they are the only
// machines positioned to observe the fleet, because a follower that
// can reach the API is by definition reaching a leader. The staged
// and proven copies of the Cluster document carry spec only — status
// never lands in the lifecycle bytes, because observations aren't
// part of the document's identity.
//
// The conditions are the real observations and the phase is their
// one-word summary, exactly the Machine's arrangement: Ready when
// every machine is Ready, Updating when the only machines not Ready
// are mid-transition (rebooting into a change, waiting on one, or
// booting), and Degraded when any machine is Lost, Blocked, or
// otherwise unwell. One thing this status can never honestly show:
// quorum lost. Losing a majority of leaders takes the API server
// down with it, so there is nobody left to write — a frozen status
// is itself the symptom.
type ClusterStatus struct {
	Phase      Phase        `json:"phase,omitempty"`
	Machines   MachineTally `json:"machines,omitzero"`
	Conditions []Condition  `json:"conditions,omitempty"`
}

// MachineTally is the cluster's headcount: how many of its Machines
// are fully healthy (phase Ready with a fresh heartbeat) out of how
// many exist. Summary is the same two numbers as "4/5", stored
// because a CRD printer column can read one field but can't combine
// two.
type MachineTally struct {
	Ready   int    `json:"ready"`
	Total   int    `json:"total"`
	Summary string `json:"summary,omitempty"`
}

type ClusterSpec struct {
	// Leaders names the machines that run control planes, by their
	// Machine names. A machine's role is derived, never declared: it
	// is a leader exactly when its name appears here, so promoting a
	// follower is one Cluster edit, not a coordinated pair of Machine
	// edits.
	Leaders []string `json:"leaders,omitempty"`

	// Endpoint is the URL followers join the cluster through, e.g.
	// https://10.10.0.1:6443. With a single leader this is that
	// leader's address; a highly-available control plane will need a
	// better answer here (every leader listed, a DNS name, or a
	// virtual IP), which is a later milestone's problem.
	Endpoint string `json:"endpoint,omitempty"`

	// Network holds the network facts k3s requires every node to agree
	// on: cluster-scoped truths that would otherwise masquerade as
	// per-node flags.
	Network ClusterNetworkSpec `json:"network,omitzero"`

	// Time is the cluster's time hierarchy: where the leaders get
	// their time. It lives on the Cluster for the same reason the
	// network plan does — clocks are a fact the whole fleet must
	// agree on, and TLS (so the cluster itself) stops working when
	// they don't.
	Time ClusterTimeSpec `json:"time,omitzero"`
}

// ClusterTimeSpec declares where time comes from. Only the leaders
// consult it: followers sync from the leaders themselves (resolved
// from the fleet's Machine manifests, with the endpoint as the
// fallback), so the hierarchy is upstreams, then leaders, then
// everyone else.
type ClusterTimeSpec struct {
	// Upstreams are the NTP servers the cluster's leaders sync from,
	// as hostnames or addresses. There is no default — a distro that
	// shipped pool.ntp.org here would volunteer every deployment's
	// machines to a volunteer service without asking. An empty list
	// is legal and means the fleet free-runs: internally consistent,
	// correct only if the hardware clocks happen to be.
	Upstreams []string `json:"upstreams,omitempty"`
}

// ClusterNetworkSpec is the cluster's address plan. Every field is
// optional: whatever is left unset falls to k3s's defaults, and the
// value of writing one here is that every node provably agrees on it.
type ClusterNetworkSpec struct {
	// NodeCIDR is the subnet the nodes address each other on: the wire
	// the cluster speaks over. A machine may have several interfaces
	// (an internet uplink, a management port); the one whose address
	// falls inside this subnet is the one Kubernetes traffic uses, and
	// its address becomes the machine's node IP.
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
// answers leader: a machine with no cluster manifest is alone, and a
// machine alone is its own single-node cluster, which is exactly what
// liken has always booted.
func (c *Cluster) Role(machineName string) Role {
	if c == nil || slices.Contains(c.Spec.Leaders, machineName) {
		return RoleLeader
	}
	return RoleFollower
}

// ParseCluster reads a Cluster manifest from its bytes, strictly, for
// the same reason Machine parsing is strict: a misspelled field
// should be an error someone sees, not a setting that silently never
// applies.
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
// no cluster manifest gets nil: a valid, single-node arrangement (see
// Role). A manifest that exists but doesn't parse is a configuration
// error and is reported as one.
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
