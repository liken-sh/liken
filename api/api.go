// Package api is the grammar of the liken.sh API group. It holds the
// shapes and the vocabulary that every liken document shares, and
// nothing more.
//
// liken's documents include the Machine, the Cluster, and the
// smaller records that use the same machinery. Kubernetes resources
// shape all of them, and this package defines that shape. It
// declares the group and version, and the metadata each document
// carries. It declares the conditions and phases each status uses,
// the role vocabulary both documents use, and the version grammar
// that names releases. The machine package and the cluster package
// each own one document, and both build on this package. Neither
// package imports the other. Kubernetes uses the same layering:
// metav1 sits underneath, and the typed APIs sit beside each other
// on top.
//
// This package covers a narrow scope, chosen on purpose: only facts
// that every document shares belong here. Nothing behavioral belongs
// here. Nothing about storage or staging belongs here. A type that
// names one document is in the wrong package.
//
// k8s.io/apimachinery already defines these shapes, as
// metav1.ObjectMeta and metav1.Condition. liken redeclares them on
// purpose. The kubernetes package speaks to the API server with only
// net/http, for the same reason: liken's only Kubernetes dependency
// is the YAML converter. Importing apimachinery for two structs
// would add its whole dependency tree and release cadence. The
// narrow copies also do real work. metav1.ObjectMeta has about
// twenty-five fields, and liken uses three of them. Every liken
// document parses strictly, so liken refuses metadata it would
// otherwise ignore silently: labels, annotations, and owner
// references in a hand-written manifest. The wire format stays
// compatible with the Kubernetes convention. The Go types represent
// only the fields liken uses.
package api

// APIVersion is the full group/version string every liken document
// declares. It is also the URL segment operators use to speak to the
// API server: /apis/liken.sh/v1alpha1/machines. CRD groups are DNS
// names, and liken owns liken.sh.
const APIVersion = "liken.sh/v1alpha1"

// ObjectMeta is the slice of Kubernetes object metadata that liken
// uses. Name identifies the object. ResourceVersion is the cluster's
// optimistic-concurrency counter. The operator sends it back when it
// watches, so the server knows where to resume. Generation counts
// changes to the spec. The API server increases generation on spec
// writes and leaves it unchanged on status writes. This lets a
// condition record which version of the spec it judged.
type ObjectMeta struct {
	Name            string `json:"name"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Generation      int64  `json:"generation,omitempty"`
}

// Phase summarizes a document's state in one word. Go models a
// closed vocabulary with a named string type. The constants below
// are the only legal values. The compiler catches an error when code
// passes a Phase where a Role belongs. The wire format stays
// unchanged, because a named string marshals exactly like a bare
// string. Kubernetes' own API types use the same method: v1.PodPhase
// and metav1.ConditionStatus.
type Phase string

// These are the phases a machine can report, listed from most severe
// to least severe. Each phase summarizes the conditions; it is not a
// fact on its own. The operator derives the phase from the
// conditions on every pass. The table that does this work,
// machine-operator/phase.go, decides which condition puts a machine
// in which phase. Lost is the exception to this rule. A machine
// cannot report its own death, so the cluster operator writes Lost
// on the machine's behalf when the machine's heartbeat goes silent.
// The Cluster's status reuses this same vocabulary (Ready, Updating,
// Degraded), so a fleet and its machines use the same words to
// describe their state.
const (
	PhaseUnknown       Phase = "Unknown"       // the facts are unreadable, so the operator cannot judge the machine's state
	PhaseBooting       Phase = "Booting"       // init has not yet finished publishing this boot's record
	PhaseLost          Phase = "Lost"          // the heartbeat went silent, and the cluster operator wrote this, not the machine
	PhaseBlocked       Phase = "Blocked"       // drift exists but the system cannot stage it; it needs a different edit, not more time
	PhaseUpdating      Phase = "Updating"      // a reboot is under way to apply a staged change
	PhaseUpdatePending Phase = "UpdatePending" // a change is staged and waits for a Manual reboot
	PhaseDegraded      Phase = "Degraded"      // something is wrong that does not match one of the specific states above
	PhaseReady         Phase = "Ready"         // every condition is True
)

// Role is what a machine is in its cluster. There are exactly two
// roles. Leaders run a control plane: an API server, a scheduler,
// and the datastore. Followers run workloads and take direction from
// the leaders. k3s calls these roles "server" and "agent". liken
// translates between the two names in exactly one place, the moment
// it execs k3s (init/supervisor.go), and uses leader and follower
// everywhere else. Both documents share this vocabulary directly:
// the Cluster's spec declares the leaders, and each Machine's status
// reports the role it derived.
type Role string

const (
	RoleLeader   Role = "leader"
	RoleFollower Role = "follower"
)
