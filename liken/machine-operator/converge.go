package main

// This file decides how a machine moves to its Machine spec. Sysctls
// apply while the system runs. Storage, network, resource limits, and
// a removed module apply only at boot:
//
//   - A filesystem cannot be replaced under a running cluster.
//   - A new address on a running machine would cut its connection to
//     the cluster, and a liken machine has no shell to fix it from.
//   - The kernel has no safe way to unload a driver that is in use.
//
// The operator writes the new manifest to the machineState
// filesystem. At the next boot, init tries it, then promotes or
// rejects it (machine/staging.go). The code here detects drift,
// refuses a spec the machine cannot satisfy, stages the rest, and
// then requests a reboot (rebootPolicy: Auto) or reports that one is
// pending (rebootPolicy: Manual, the default).
//
// The decisions are pure functions of the Machine and the boot's
// facts, and reconcile() does the I/O, so the tests cover the decision
// table without a cluster or a disk.

import (
	"fmt"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// validateStaging checks a storage spec against this machine's actual
// partitions and disks. The CRD's CEL rules cannot do that, because
// they only compare the spec with the last boot's published status.
func validateStaging(spec machine.StorageSpec, facts *machine.MachineStatus) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	for _, role := range spec.Roles() {
		placed := facts.Storage.Role(role.Name)
		if placed != nil && placed.Backing == machine.BackingPartition {
			// A partition can only grow. A role with no size takes the
			// rest of the disk, so giving it a size smaller than its
			// partition is a shrink too.
			if role.Size == "" {
				continue
			}
			declared, err := machine.ParseSize(role.Size)
			if err != nil {
				return err
			}
			if declared < placed.CapacityBytes {
				// The usual cause is a reinstall that laid out the disk
				// differently from the Machine document, which survives a
				// reinstall. So the message says to edit the document and
				// gives a size that passes.
				return fmt.Errorf("storage role %s: the spec declares %s and the partition is %s, but a storage role can only grow. Declare %s or more. If the machine was reinstalled with a different layout, edit its Machine document to the layout it has now",
					role.Name, role.Size, machine.SizeText(placed.CapacityBytes), machine.SizeText(placed.CapacityBytes))
			}
			continue
		}
		// A role with no partition yet gets one at the next boot, on a
		// disk that must be attached.
		if !deviceAttached(role.Device, facts.Hardware.BlockDevices) {
			return fmt.Errorf("storage role %s: device %s is not one of the block devices of this machine (%s)",
				role.Name, role.Device, deviceNames(facts.Hardware.BlockDevices))
		}
	}
	return nil
}

func deviceAttached(device string, disks []machine.BlockDevice) bool {
	for _, d := range disks {
		if "/dev/"+d.Name == device {
			return true
		}
	}
	return false
}

func deviceNames(disks []machine.BlockDevice) string {
	var names []string
	for _, d := range disks {
		names = append(names, d.Name)
	}
	if len(names) == 0 {
		return "none attached"
	}
	return strings.Join(names, ", ")
}

// renderManifest returns the bytes to stage and their hash: a Machine
// document with the whole spec, sysctls included, and no status.
// sigs.k8s.io/yaml marshals through JSON with sorted keys, so the same
// spec always gives the same bytes and the same hash. The hash
// identifies the spec in staging, in rejection records, and in the
// boot's facts.
func renderManifest(name string, spec machine.MachineSpec) ([]byte, string, error) {
	doc := machine.Machine{
		APIVersion: api.APIVersion,
		Kind:       "Machine",
		Metadata:   api.ObjectMeta{Name: name},
		Spec:       spec,
	}
	body, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, "", err
	}
	return body, machine.ManifestHash(body), nil
}

// A turn records whether the machine may reboot or restart k3s now. A
// machine with no Cluster does so whenever it needs to. A cluster
// member waits until cluster-operator gives it an entry in the
// Cluster's status.rebootTurns (turn.go).
type turn int

const (
	turnStandalone turn = iota // no Cluster
	turnAwaiting               // cluster member with no entry in status.rebootTurns
	turnGranted                // cluster member with an entry in status.rebootTurns
)

// A convergence is the result of one decision: the condition to
// publish and the side effects that reconcile() performs.
type convergence struct {
	condition      api.Condition
	stage          bool                       // write manifest to the machineState filesystem
	requestReboot  bool                       // ask init to reboot
	requestRestart bool                       // ask init to restart k3s
	requestLoad    bool                       // ask init to load the added modules and serio entries into the running kernel
	withdraw       bool                       // delete the staged manifest
	clearRejection bool                       // delete the rejection record
	manifest       []byte                     // the bytes to stage
	hash           string                     // the hash of manifest
	pending        *machine.PendingDisruption // the status.pending entry, while the document waits for a reboot or restart
	keepBootNext   string                     // the request whose BootNext the reboot keeps (hold.go)
}

// converged, notConverged, and convergenceUnknown build a condition.
// Each staged document reports under its own condition type, such as
// SpecConverged or ClusterConverged, so the type is a parameter.
func converged(condType, reason, message string) api.Condition {
	return api.Condition{Type: condType, Status: api.ConditionTrue, Reason: reason, Message: message}
}

func notConverged(condType, reason, message string) api.Condition {
	return api.Condition{Type: condType, Status: api.ConditionFalse, Reason: reason, Message: message}
}

func convergenceUnknown(condType, reason, message string) api.Condition {
	return api.Condition{Type: condType, Status: api.ConditionUnknown, Reason: reason, Message: message}
}

// factsIncomplete is the result when there are no facts, or the facts
// have no boot record, as from an older init. The condition is
// Unknown and nothing happens, because a decision made from missing
// facts could reboot a machine by mistake.
func factsIncomplete(condType string) convergence {
	return convergence{condition: convergenceUnknown(condType, "FactsIncomplete",
		"The machine's facts have no boot record yet")}
}

// machineStateEphemeral is the result when the machineState role is
// backed by memory, because a document staged there is lost at the
// reboot that would apply it.
func machineStateEphemeral(condType, what string) convergence {
	return convergence{condition: notConverged(condType, "MachineStateEphemeral",
		fmt.Sprintf("machineState is backed by memory, so there is no durable filesystem to stage %s into. Declare machineState in the machine's manifest", what))}
}

// convergedWithCleanup returns cond with a request to delete any
// staged document and any rejection record. When the boot already
// runs the spec, a staged document is from an edit that was reverted,
// and the next boot would apply it. A rejection record blocks a spec
// that is no longer requested.
func convergedWithCleanup(cond api.Condition, stagedHash string, rejection *machine.Rejection) convergence {
	return convergence{
		condition:      cond,
		withdraw:       stagedHash != "",
		clearRejection: rejection != nil,
	}
}

// gateDisruption sets c's condition and requests for a staged
// document that needs a reboot, or a k3s restart when restart is set.
// It always records the document in status.pending. Every document
// passes its own messages, but the reasons and their order are the
// same for all of them:
//
//   - With rebootPolicy: Manual, the condition stays RebootPending or
//     RestartPending until a person approves the operator's
//     Disruption. approval is the comma-separated list of document
//     hashes that the approved Disruption covers, which
//     withRequestApprovals (requests.go) writes. When it has c.hash,
//     the machine continues as under Auto, with a turn and a drain,
//     so the disruption budgets apply.
//   - A cluster member without a turn gets AwaitingTurn, for a reboot
//     and a restart alike, so cluster-operator orders both in one
//     queue. A restart waits for a turn too, because on a leader it
//     restarts the embedded datastore, which can lose quorum as a
//     reboot can.
//   - Otherwise the result asks init to reboot or to restart k3s.
func gateDisruption(c *convergence, condType string, policy machine.RebootPolicy, t turn, restart bool, approval, summary, pending, awaiting, requested string) {
	kind := machine.DisruptionReboot
	pendingReason, requestedReason := "RebootPending", "RebootRequested"
	if restart {
		kind = machine.DisruptionRestart
		pendingReason, requestedReason = "RestartPending", "RestartRequested"
	}
	c.pending = &machine.PendingDisruption{
		Condition: condType, Kind: kind, Hash: c.hash, Summary: summary,
	}
	switch {
	case policy != machine.RebootAuto && !approvedByRequest(approval, c.hash):
		c.condition = notConverged(condType, pendingReason, pending)
	case t == turnAwaiting:
		c.condition = notConverged(condType, "AwaitingTurn", awaiting)
	default:
		c.requestReboot = !restart
		c.requestRestart = restart
		c.condition = notConverged(condType, requestedReason, requested)
	}
}

// decideConvergence compares the Machine's spec with what this boot
// applied, and decides what to stage and whether to request a reboot.
// It returns at the first case that applies:
//
//  1. There are no facts, or they have no boot record: Unknown.
//  2. There is no drift: True, with cleanup (convergedWithCleanup).
//  3. Init rejected this exact spec at boot: refuse to stage it
//     again. rejection comes from the record on machineState and not
//     from the facts, which are a snapshot from boot, so a reverted
//     edit clears its rejection at once. A different edit has a
//     different hash and is not blocked.
//  4. The boot reports this exact manifest's hash, but drift remains:
//     BootMismatch, which is a bug in liken. Without this case the
//     operator would request a reboot on every pass. The exception is
//     drift in module parameters only, because a live load promotes
//     the manifest and applies only the parameters it can
//     (init/liveload.go). That drift continues to case 7.
//  5. machineState is backed by memory, so nothing can be staged.
//  6. The spec fails validation against this machine.
//  7. Stage the manifest, unless these exact bytes are staged already.
//     Then request a live load, or let gateDisruption decide the
//     reboot.
func decideConvergence(m *machine.Machine, facts *machine.MachineStatus, rejection *machine.Rejection, stagedHash string, t turn) convergence {
	if facts == nil || facts.Boot.ManifestSource == "" {
		return factsIncomplete("SpecConverged")
	}

	storageDiffs := machine.StorageDrift(m.Spec.Storage, facts.Boot.Storage)
	networkDiffs := machine.NetworkDrift(m.Spec.Network, facts.Boot.Network)
	added, retracted := machine.ModuleSetDiff(m.Spec.Modules, facts.Boot.Modules)
	parameterDiffs := machine.ModuleParameterDrift(m.Spec.Modules, facts.Boot.Modules,
		m.Spec.ModuleParameters, facts.Boot.ModuleParameters)
	serioAdded, serioRetracted := machine.SerioSetDiff(m.Spec.Serio, facts.Boot.Serio)
	drift := slices.Concat(storageDiffs, networkDiffs,
		machine.ModulesDrift(m.Spec.Modules, facts.Boot.Modules,
			m.Spec.ModuleParameters, facts.Boot.ModuleParameters),
		machine.SerioDrift(m.Spec.Serio, facts.Boot.Serio),
		machine.RlimitDrift(m.Spec.Rlimits, facts.Boot.Rlimits))
	// A change of module order is kept out of drift, because the checks
	// below count drift lines, and a reorder is handled apart from them.
	orderDiffs := machine.ModuleOrderDrift(m.Spec.Modules, facts.Boot.Modules)
	if len(drift) == 0 && len(orderDiffs) == 0 {
		return convergedWithCleanup(
			converged("SpecConverged", "Converged", "This boot applied the current spec"),
			stagedHash, rejection)
	}
	diffs := strings.Join(slices.Concat(drift, orderDiffs), "; ")

	manifest, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingFailed", "The spec is not staged: "+err.Error())}
	}

	if rejection != nil && rejection.Hash == hash {
		return convergence{condition: notConverged("SpecConverged", "RejectedLastBoot",
			fmt.Sprintf("init rejected this spec at boot: %s. The operator does not stage a rejected spec again, so change the spec", rejection.Reason))}
	}
	// After a live load, the boot reports the manifest's hash while
	// drift remains in module parameters the load could not apply, or
	// in module order. Order is not in drift. parametersOnly compares
	// counts, so any other drift line still makes a BootMismatch.
	parametersOnly := len(storageDiffs) == 0 && len(networkDiffs) == 0 &&
		len(added) == 0 && len(retracted) == 0 && len(parameterDiffs) == len(drift)
	if facts.Boot.ManifestHash == hash && !parametersOnly {
		return convergence{condition: notConverged("SpecConverged", "BootMismatch",
			fmt.Sprintf("The facts report that this boot applied the spec, but the spec differs from the boot's record (%s). The operator does not reboot while the two disagree. This is a defect in liken", diffs))}
	}
	if facts.Storage.MachineState.Backing != machine.BackingPartition {
		return machineStateEphemeral("SpecConverged", "a manifest")
	}
	// Init validates the network spec at boot too. Checking here saves
	// a reboot that ends on the proven manifest with a rejection
	// record. Whether the machine has a port with each declared name
	// is known only at boot.
	if err := m.Spec.Network.Validate(); err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingRejected", "The spec is not staged: "+err.Error())}
	}
	// Init skips a resource limit it cannot apply and boots anyway, so
	// a typo would cost a reboot, apply nothing, and show only on the
	// console. Refusing here reports it in the condition.
	if err := machine.ValidateRlimits(m.Spec.Rlimits); err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingRejected", "The spec is not staged: "+err.Error())}
	}
	// The API server refuses the same serio entries, so this check
	// fails only for a spec that the API server did not validate.
	if err := machine.ValidateSerio(m.Spec.Serio); err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingRejected", "The spec is not staged: "+err.Error())}
	}
	if err := validateStaging(m.Spec.Storage, facts); err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingRejected", "The spec is not staged: "+err.Error())}
	}

	c := convergence{
		manifest: manifest,
		hash:     hash,
		stage:    stagedHash != hash, // skip the write when these exact bytes are staged already
	}

	// When only the module order changed, the manifest is staged and
	// nothing is requested. A live load cannot reorder loaded modules,
	// and a reorder alone does not justify a reboot, so the next boot,
	// whatever its cause, loads the new order. cluster.go treats a
	// change that only a boot reads the same way.
	if len(drift) == 0 {
		c.condition = notConverged("SpecConverged", "StagedForNextBoot",
			fmt.Sprintf("The spec (%.12s) is staged for the next boot. The machine loads the modules in the new order at its next boot, without a reboot turn: %s", hash, diffs))
		return c
	}

	// When every drift line is an added module or an added serio
	// entry, init can apply the change to the running kernel, which
	// binds a newly loaded driver to hardware already plugged in. The
	// operator requests a load with no rebootPolicy check and no turn.
	// A removed module or serio entry still needs a reboot, because
	// pods can hold the devices it created.
	//
	// The check compares counts and does not list the spec fields that
	// must be unchanged. ModulesDrift and SerioDrift write one line for
	// each addition, so the counts are equal only when additions are
	// all of the drift, and a spec field added later needs a reboot by
	// default. A list of fields would let a forgotten field take this
	// path. Init's live loader promotes the staged manifest whether or
	// not it applied everything, so the machine would report a spec
	// that it never applied. cluster.RestartApplies protects the
	// cluster document the same way.
	//
	// A reorder in the same edit applies at the next boot.
	if len(retracted) == 0 && len(serioRetracted) == 0 && len(drift) == len(added)+len(serioAdded) {
		c.requestLoad = true
		c.condition = notConverged("SpecConverged", "LoadRequested",
			fmt.Sprintf("A module load is requested to apply the staged spec (%.12s) in place: %s", hash, diffs))
		return c
	}

	gateDisruption(&c, "SpecConverged", m.Spec.RebootPolicyOrDefault(), t, false,
		m.Metadata.Annotations[requestApprovalKey],
		"the machine spec: "+diffs,
		fmt.Sprintf("The spec (%.12s) is staged for the next boot. rebootPolicy is Manual, so reboot the machine or set rebootPolicy to Auto to apply it: %s", hash, diffs),
		fmt.Sprintf("The spec (%.12s) is staged for the next boot and waits for a reboot turn from the cluster: %s", hash, diffs),
		fmt.Sprintf("A reboot is requested to apply the staged spec (%.12s): %s", hash, diffs))
	return c
}
