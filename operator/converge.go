package main

// Convergence: closing the loop between the cluster's spec and the
// machine's boot.
//
// Sysctls reconcile live; storage cannot, because a filesystem can't
// be swapped under a running cluster. A storage edit therefore
// converges by reboot: the operator stages the desired manifest onto
// the machineState filesystem, where the next boot finds it, tries
// it, and promotes or rejects it (machine/staging.go covers that
// side). This file is the operator's half: notice drift, refuse what
// the machine can't satisfy, stage what it can, and either request
// the reboot (rebootPolicy: Auto) or report that one is pending
// (Manual, the default).
//
// Every decision here is a pure function over the cluster's Machine
// and the boot's facts; reconcile() supplies the few lines of I/O.
// That's the same decisions-from-actions split init's storage uses,
// and it's what makes the whole feature table-testable without a
// cluster or a disk.

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/chrisguidry/liken/machine"
)

// storageDrift compares the declared storage against what the boot
// actuated, role by role, with sizes normalized (2048Mi and 2Gi
// declare the same thing). The returned diffs are written for humans;
// they appear verbatim in condition messages. Sysctls never count as
// drift: the operator already reconciles those live, so a reboot
// would apply nothing that isn't already applied.
func storageDrift(desired, actuated machine.StorageSpec) []string {
	var diffs []string
	desiredRoles := rolesByName(desired)
	actuatedRoles := rolesByName(actuated)
	for _, name := range machine.StorageRoleNames {
		d, dok := desiredRoles[name]
		a, aok := actuatedRoles[name]
		switch {
		case dok && !aok:
			diffs = append(diffs, fmt.Sprintf("%s: declared but not actuated", name))
		case !dok && aok:
			diffs = append(diffs, fmt.Sprintf("%s: actuated but no longer declared", name))
		case dok && aok:
			if d.Device != a.Device {
				diffs = append(diffs, fmt.Sprintf("%s: device %s declared, %s actuated", name, d.Device, a.Device))
			}
			if !sameSize(d.Size, a.Size) {
				diffs = append(diffs, fmt.Sprintf("%s: size %s declared, %s actuated", name, orRemainder(d.Size), orRemainder(a.Size)))
			}
		}
	}
	return diffs
}

func rolesByName(spec machine.StorageSpec) map[machine.StorageRoleName]machine.DeclaredRole {
	byName := map[machine.StorageRoleName]machine.DeclaredRole{}
	for _, role := range spec.Roles() {
		byName[role.Name] = role
	}
	return byName
}

// sameSize compares two size declarations by the number of bytes they
// describe rather than by their spelling. An unparseable size (which
// validation will refuse anyway) falls back to string comparison
// rather than panicking here.
func sameSize(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	aBytes, aErr := machine.ParseSize(a)
	bBytes, bErr := machine.ParseSize(b)
	if aErr != nil || bErr != nil {
		return a == b
	}
	return aBytes == bBytes
}

func orRemainder(size string) string {
	if size == "" {
		return "(remainder)"
	}
	return size
}

// validateStaging checks everything admission can't, because these
// checks need the actual machine: CEL rules in the CRD compare the
// spec against the last boot's published status, but only the facts
// record what partitions exist and what disks are attached.
func validateStaging(spec machine.StorageSpec, facts *machine.MachineStatus) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	for _, role := range spec.Roles() {
		placed := facts.Storage.Role(role.Name)
		if placed != nil && placed.Backing == machine.BackingPartition {
			// The role has a partition; its declared size may only
			// grow. This also catches a remainder role being given a
			// fixed size smaller than what it already occupies.
			if role.Size == "" {
				continue
			}
			declared, err := machine.ParseSize(role.Size)
			if err != nil {
				return err
			}
			if declared < placed.CapacityBytes {
				return fmt.Errorf("%s: declared %s is smaller than its partition's %d bytes; storage roles are grow-only",
					role.Name, role.Size, placed.CapacityBytes)
			}
			continue
		}
		// A new role must name a disk this machine actually has; the
		// device path only matters at claim time, which is exactly
		// the boot this staging is for.
		if !deviceAttached(role.Device, facts.Hardware.BlockDevices) {
			return fmt.Errorf("%s: device %s is not among this machine's block devices (%s)",
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

// renderManifest produces the canonical bytes to stage: a complete
// Machine document carrying the whole spec (sysctls and network
// included, so the reboot converges everything even though only
// storage triggers it) and no status. The rendering is deterministic:
// sigs.k8s.io/yaml marshals through JSON with sorted keys, so the
// same spec always yields the same bytes, and the hash of those bytes
// is the spec's identity everywhere (staging idempotence, rejections,
// facts).
func renderManifest(name string, spec machine.MachineSpec) ([]byte, string, error) {
	doc := machine.Machine{
		APIVersion: machine.APIVersion,
		Kind:       "Machine",
		Metadata:   machine.ObjectMeta{Name: name},
		Spec:       spec,
	}
	body, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, "", err
	}
	return body, machine.ManifestHash(body), nil
}

// A turn is the machine's standing with the rollout conductor
// (rollout.go): whether it may reboot right now. A machine with no
// cluster document has no conductor and reboots at will; a cluster
// member waits until the conductor writes a RebootApproved condition
// onto it. Only rebootPolicy: Auto ever consults this. A Manual
// machine waits for a person regardless.
type turn int

const (
	turnStandalone turn = iota // no cluster: reboot at will
	turnAwaiting               // cluster member, not yet granted
	turnGranted                // cluster member, grant in hand
)

// A convergence is one reconcile pass's decision: the condition to
// publish, and which side effects to perform. decideConvergence is
// pure; reconcile() acts.
type convergence struct {
	condition      machine.Condition
	stage          bool   // write manifest to the machineState filesystem
	requestReboot  bool   // write the reboot intent for init
	withdraw       bool   // remove the staged manifest; the spec no longer wants it
	clearRejection bool   // remove the rejection record; the spec it blocks is gone
	manifest       []byte // the bytes to stage
	hash           string // their identity
}

// The condition constructors for every convergence verdict. Three
// documents converge through this machinery, each under its own
// condition type (SpecConverged, ClusterConverged, VersionConverged),
// and all three share one reason vocabulary, so the constructors take
// the type rather than hard-coding it.
func converged(condType, reason, message string) machine.Condition {
	return machine.Condition{Type: condType, Status: machine.ConditionTrue, Reason: reason, Message: message}
}

func notConverged(condType, reason, message string) machine.Condition {
	return machine.Condition{Type: condType, Status: machine.ConditionFalse, Reason: reason, Message: message}
}

func convergenceUnknown(condType, reason, message string) machine.Condition {
	return machine.Condition{Type: condType, Status: machine.ConditionUnknown, Reason: reason, Message: message}
}

// gateReboot finishes a staged document's convergence: the staged
// bytes are already in the convergence, and what remains is whether
// this machine may reboot to apply them right now. The decision
// table is identical for every staged document and is the safety
// core of the rollout design, so it lives here once: Manual policy
// always waits for a person (RebootPending), a cluster member on
// Auto waits for the conductor's turn (AwaitingTurn), and only a
// standalone or granted machine actually asks init to reboot
// (RebootRequested). The messages differ per document; the reasons
// and their order of precedence must not.
func gateReboot(c *convergence, condType string, policy machine.RebootPolicy, t turn, pending, awaiting, requested string) {
	switch {
	case policy != machine.RebootAuto:
		c.condition = notConverged(condType, "RebootPending", pending)
	case t == turnAwaiting:
		c.condition = notConverged(condType, "AwaitingTurn", awaiting)
	default:
		c.requestReboot = true
		c.condition = notConverged(condType, "RebootRequested", requested)
	}
}

// decideConvergence makes the whole convergence decision in one pure
// function. The cases, in the order they short-circuit:
//
//  1. No facts, or facts without a boot record (an older init, a
//     machine mid-upgrade): Unknown. Guessing could reboot a machine
//     over a misreading.
//  2. No drift: converged. This case also cleans up after an edit
//     that was taken back. A manifest still staged for a spec the
//     cluster no longer wants is withdrawn, because the next boot
//     would otherwise apply it. A standing rejection is cleared for
//     the same reason: the spec it blocks is no longer being asked
//     for, so the record no longer blocks anything.
//  3. The desired spec is the one init rejected: refuse to re-stage
//     it. The rejection parameter is read from the durable quarantine
//     record on machineState, not from facts. Facts are a snapshot
//     taken at boot and never change while the machine runs, but an
//     edit that reverts and then retries within one boot must see the
//     clearing take effect immediately, not at the next reboot. Only
//     a genuinely different edit (or clearing the record by
//     converging) unblocks the hash.
//  4. Facts claim this exact manifest was actuated, yet drift still
//     computes: a contradiction, necessarily a liken bug. Holding in
//     a wedged condition is better than rebooting the machine on
//     every reconcile pass.
//  5. machineState is memory-backed: there is nowhere durable to
//     stage into.
//  6. The spec fails validation against the machine's reality.
//  7. Valid drift: stage (unless these exact bytes already are), and
//     per rebootPolicy and the machine's turn either request the
//     reboot, wait for the cluster's grant, or report one pending.
func decideConvergence(m *machine.Machine, facts *machine.MachineStatus, rejection *machine.Rejection, stagedHash string, t turn) convergence {
	if facts == nil || facts.Boot.ManifestSource == "" {
		return convergence{condition: convergenceUnknown("SpecConverged", "FactsIncomplete",
			"the machine's facts carry no boot record yet")}
	}

	drift := storageDrift(m.Spec.Storage, facts.Boot.Storage)
	if len(drift) == 0 {
		return convergence{
			condition:      converged("SpecConverged", "Converged", "this boot actuated the current spec"),
			withdraw:       stagedHash != "",
			clearRejection: rejection != nil,
		}
	}
	diffs := strings.Join(drift, "; ")

	manifest, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingFailed", err.Error())}
	}

	if r := rejection; r != nil && r.Hash == hash {
		return convergence{condition: notConverged("SpecConverged", "RejectedLastBoot",
			fmt.Sprintf("init rejected this exact spec at boot: %s; edit the spec to something different", r.Reason))}
	}
	if facts.Boot.ManifestHash == hash {
		return convergence{condition: notConverged("SpecConverged", "BootMismatch",
			fmt.Sprintf("facts claim this spec was actuated, yet it differs from the boot's storage (%s); refusing to reboot over a contradiction — this is a liken bug", diffs))}
	}
	if facts.Storage.MachineState.Backing != machine.BackingPartition {
		return convergence{condition: notConverged("SpecConverged", "MachineStateEphemeral",
			"machineState is backed by memory; there is no durable filesystem to stage a manifest into — declare machineState in the machine's manifest")}
	}
	if err := validateStaging(m.Spec.Storage, facts); err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingRejected", err.Error())}
	}

	c := convergence{
		manifest: manifest,
		hash:     hash,
		stage:    stagedHash != hash, // idempotence: skip the write when these exact bytes are already staged
	}
	gateReboot(&c, "SpecConverged", m.Spec.RebootPolicyOrDefault(), t,
		fmt.Sprintf("spec staged for the next boot (%.12s); rebootPolicy is Manual, so reboot the machine (or set rebootPolicy: Auto) to apply: %s", hash, diffs),
		fmt.Sprintf("spec staged for the next boot (%.12s); waiting for the cluster to grant a reboot turn: %s", hash, diffs),
		fmt.Sprintf("reboot requested to apply the staged spec (%.12s): %s", hash, diffs))
	return c
}

// readStagedHash returns the hash of whatever document is currently
// staged in the store, or "" when nothing is staged. Staged bytes
// that fail to parse still get hashed, because the idempotence check
// compares bytes, not parsed meaning.
func readStagedHash(store machine.ManifestStore) string {
	raw, _ := store.LoadStaged()
	if raw == nil {
		return ""
	}
	return machine.ManifestHash(raw)
}
