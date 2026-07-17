// Package api is the grammar of the liken.sh API group: the shapes
// and vocabulary every liken document shares, and nothing more.
//
// liken's documents — the Machine, the Cluster, and the smaller
// records that ride the same machinery — are all shaped like
// Kubernetes resources, and this package is what that shape is made
// of: the group/version they declare, the metadata they carry, the
// conditions and phases their statuses speak, the role vocabulary
// both documents use, and the version grammar releases are named in.
// The machine and cluster packages each own their document and both
// build on this one; neither imports the other, the same layering
// Kubernetes itself uses (metav1 underneath, the typed APIs beside
// each other on top).
//
// The charter is deliberately narrow: only what every document
// shares belongs here. Nothing behavioral, nothing about storage or
// staging or any one document's fields. A type that mentions one
// document by name is in the wrong package.
//
// These shapes exist in k8s.io/apimachinery as metav1.ObjectMeta and
// metav1.Condition, and liken redeclares them deliberately, for the
// same reason the kubernetes package speaks to the API server with
// nothing but net/http: liken's only Kubernetes dependency is the
// YAML converter, and importing apimachinery for two structs would
// buy its whole dependency tree and release cadence. The narrow
// copies also do real work: metav1.ObjectMeta has some twenty-five
// fields and liken honors three, and because every liken document
// parses strictly, metadata liken would silently ignore (labels,
// annotations, owner references in a hand-written manifest) is
// refused at parse instead. The wire format stays convention-
// compatible; the Go types are the honest subset.
package api

// APIVersion is the full group/version string every liken document
// declares, and the URL segment the operators speak to the API
// server: /apis/liken.sh/v1alpha1/machines. CRD groups are DNS
// names, and we own liken.sh.
const APIVersion = "liken.sh/v1alpha1"

// ObjectMeta is the slice of Kubernetes object metadata liken
// actually uses. Name identifies the object. ResourceVersion is
// the cluster's optimistic-concurrency counter, and the operator hands
// it back when watching so the server knows where to resume.
// Generation counts spec changes (the API server bumps it on spec
// writes and leaves it alone on status writes), which is what lets a
// condition say which version of the spec it judged.
type ObjectMeta struct {
	Name            string `json:"name"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Generation      int64  `json:"generation,omitempty"`
}

// Phase summarizes a document's state in one word. Named string types
// are how Go models a closed vocabulary: the constants below are the
// only values, the compiler catches a Phase handed where a Role
// belongs, and the wire format is unchanged (a named string marshals
// exactly like a bare one). Kubernetes' own API types use the same
// idiom (v1.PodPhase, metav1.ConditionStatus).
type Phase string

// The phases a machine can report, most severe first. Each is a
// summary of the conditions, not a fact of its own; the operator
// derives the phase from the conditions on every pass, and the table
// that does so (machine-operator/phase.go) is the authority on which
// condition puts a machine in which phase. Lost is the exception to
// "derived from own conditions": a machine cannot report its own
// death, so the cluster operator writes Lost on its behalf when its
// heartbeat goes silent. The Cluster's status deliberately reuses
// this vocabulary (Ready, Updating, Degraded), so a fleet and its
// machines are described in the same words.
const (
	PhaseUnknown       Phase = "Unknown"       // the facts are unreadable; the operator can't tell anything
	PhaseBooting       Phase = "Booting"       // init hasn't finished publishing this boot's record yet
	PhaseLost          Phase = "Lost"          // the heartbeat went silent; the cluster operator wrote this, not the machine
	PhaseBlocked       Phase = "Blocked"       // drift exists but can't be staged; it needs a different edit, not time
	PhaseUpdating      Phase = "Updating"      // a reboot is in flight to apply a staged change
	PhaseUpdatePending Phase = "UpdatePending" // a change is staged, waiting on a Manual reboot
	PhaseDegraded      Phase = "Degraded"      // something is wrong that isn't one of the specific states above
	PhaseReady         Phase = "Ready"         // every condition is True
)

// Role is what a machine is in its cluster. There are exactly two:
// leaders run a control plane (an API server, a scheduler, the
// datastore), followers run workloads and take direction from the
// leaders. k3s calls these "server" and "agent". liken translates in
// exactly one place, the moment it execs k3s (init/supervisor.go), and
// uses leader/follower everywhere else. The word is shared vocabulary
// in the most literal sense: the Cluster's spec declares the leaders,
// and each Machine's status reports the role it derived.
type Role string

const (
	RoleLeader   Role = "leader"
	RoleFollower Role = "follower"
)
