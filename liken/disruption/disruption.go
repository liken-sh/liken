// Package disruption holds the two kinds that take a machine out of
// service: a Disruption, which asks for one machine to reboot or to
// restart k3s, and a MachineDisruptionBudget, which limits how many of
// the machines it selects may be out of service at once.
//
// Every reboot and every k3s restart that liken makes waits for a turn
// that cluster-operator grants (cluster-operator/rollout.go), and the
// turn drains the machine first. Before these kinds existed, a machine
// asked for a turn with a reason on one of its own conditions, and a
// person read the fleet's requests from the conditions of each
// Machine. A Disruption puts each request in its own object, so
// `kubectl get disruptions` lists who asked, why, and where each
// request stands, and so any program that may create one can ask.
//
// The grant is still the entry in the Cluster's status.rebootTurns.
// That one object holds every turn of the fleet, so the budget stays
// one compare-and-swap on one object (milestone 82). The conditions on
// a Disruption report the grant for a person to read, and no program
// acts on them.
//
// This package imports the machine package for the reboot policy and
// for the rule that matches a hash a person pasted. The machine
// package does not import this one.
package disruption

import (
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// Kind is what a Disruption asks for. A reboot covers a restart,
// because the boot starts k3s again, so a machine with both pending
// takes only the reboot.
type Kind string

const (
	KindReboot  Kind = "Reboot"
	KindRestart Kind = "Restart"
)

// MachineOperator is the requester of the Disruption that each
// machine's operator keeps for its own staged documents.
const MachineOperator = "liken.sh/machine-operator"

// The conditions a Disruption reports. cluster-operator writes
// Approved and Granted, and machine-operator writes Complete on a
// person's request.
const (
	ApprovedCondition = "Approved"
	GrantedCondition  = "Granted"
	CompleteCondition = "Complete"
)

// Disruption is one request to take one machine out of service.
type Disruption struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   Meta   `json:"metadata"`
	Spec       Spec   `json:"spec"`
	Status     Status `json:"status,omitzero"`
}

// Meta is the Disruption's metadata. A Disruption names its Machine as
// its owner, so the garbage collector deletes it with the Machine.
type Meta struct {
	api.ObjectMeta  `json:",inline"`
	OwnerReferences []Owner `json:"ownerReferences,omitempty"`
}

// Owner is one owner reference.
type Owner struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

// Spec is the request.
//
// Requester names who asked: a key with a domain prefix for a program,
// and the Kubernetes user name for a person. Hash names what the
// request applies: the staged documents for machine-operator's
// request, and the boot it was filed against for a person's reboot.
// ApprovedHash is a Manual machine's approval, and it approves the
// request only while it names Hash, so an approval never carries over
// to a request for other bytes.
type Spec struct {
	Machine      string `json:"machine"`
	Requester    string `json:"requester"`
	Kind         Kind   `json:"kind"`
	Reason       string `json:"reason,omitempty"`
	Hash         string `json:"hash,omitempty"`
	ApprovedHash string `json:"approvedHash,omitempty"`
}

// Status reports the request.
type Status struct {
	Conditions []api.Condition `json:"conditions,omitempty"`
}

// GetObjectMeta answers the metadata that a watch's store and the
// memo of an operator's writes key the Disruption by.
func (d *Disruption) GetObjectMeta() api.Meta { return d.Metadata.ObjectMeta }

// OwnName is the name of the Disruption that machine-operator keeps for
// a machine's staged documents.
func OwnName(machineName string) string { return machineName + ".machine-operator" }

// RebootRequestName is the name of a person's request to reboot a
// machine, named for the boot it was filed against, so a second
// request for the same boot is the same object.
func RebootRequestName(machineName, bootID string) string {
	return machineName + ".reboot-" + short(bootID)
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// New answers a Disruption for a machine, owned by that Machine.
func New(name string, m *machine.Machine, spec Spec) *Disruption {
	return &Disruption{
		APIVersion: api.APIVersion,
		Kind:       "Disruption",
		Metadata: Meta{
			ObjectMeta: api.ObjectMeta{Name: name},
			OwnerReferences: []Owner{{
				APIVersion: api.APIVersion, Kind: "Machine",
				Name: m.Metadata.Name, UID: m.Metadata.UID,
			}},
		},
		Spec: spec,
	}
}

// Own reports whether machine-operator filed this request.
func (d *Disruption) Own() bool { return d.Spec.Requester == MachineOperator }

// Approved reports whether a request may take a turn. A machine on
// rebootPolicy Auto approves every request. A Manual machine's request
// is approved when its approvedHash names its hash: the full hash, or
// a prefix of 12 characters or more, because a person pastes the
// short form that a listing shows (machine.ApprovalGrants).
func (d *Disruption) Approved(policy machine.RebootPolicy) bool {
	return policy == machine.RebootAuto || d.ApprovedByHash()
}

// ApprovedByHash reports whether approvedHash names the request's hash.
func (d *Disruption) ApprovedByHash() bool {
	return d.Spec.Hash != "" && machine.ApprovalGrants(d.Spec.ApprovedHash, d.Spec.Hash)
}

// Complete reports whether machine-operator recorded the request as
// carried out.
func (d *Disruption) Complete() bool {
	c := api.FindCondition(d.Status.Conditions, CompleteCondition)
	return c != nil && c.Status == api.ConditionTrue
}

// Spent reports whether a person's reboot has happened: the request
// names a boot, and the machine runs another one. A request that names
// no boot the machine ran, such as a mistyped hash, reads as spent
// too, the same as the request annotation, so it never holds a turn.
// machine-operator's own request is never spent this way, because it
// names staged documents, and machine-operator deletes it when nothing
// is staged.
func (d *Disruption) Spent(bootID string) bool {
	if d.Own() || d.Spec.Kind != KindReboot || bootID == "" {
		return false
	}
	return !machine.RebootRequestNames(d.Spec.Hash, bootID)
}

// Done reports whether the request needs nothing more from its
// machine.
func (d *Disruption) Done(bootID string) bool {
	return d.Complete() || d.Spent(bootID)
}
