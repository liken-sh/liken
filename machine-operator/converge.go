package main

// Convergence: keeping the cluster's spec and the machine's boot in
// agreement.
//
// Sysctls reconcile live. Storage cannot, because the system cannot
// swap a filesystem under a running cluster. The declared module list
// cannot reconcile live either, because loading a module is one-way:
// the kernel offers no safe way to remove a driver while something is
// using it. Both storage and modules therefore converge through a
// reboot. The operator stages the desired manifest onto the
// machineState filesystem, where the next boot finds it, tries it,
// and promotes or rejects it (machine/staging.go covers that side).
// This file covers the operator's half of that work: notice drift,
// refuse what the machine cannot satisfy, stage what it can, and
// either request the reboot (rebootPolicy: Auto) or report that a
// reboot is pending (Manual, the default policy).
//
// Every decision in this file is a pure function over the cluster's
// Machine and the boot's facts. reconcile() supplies the few lines of
// I/O. init's storage code uses the same split between decisions and
// actions, and this split makes the whole feature testable with
// tables, without a cluster or a disk.

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

// validateStaging checks everything that admission cannot check,
// because these checks need the actual machine. CEL rules in the CRD
// compare the spec against the last boot's published status, but
// only the facts record what partitions exist and what disks are
// attached.
func validateStaging(spec machine.StorageSpec, facts *machine.MachineStatus) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	for _, role := range spec.Roles() {
		placed := facts.Storage.Role(role.Name)
		if placed != nil && placed.Backing == machine.BackingPartition {
			// The role has a partition, so its declared size may only
			// grow. This also catches the case of a remainder role
			// given a fixed size smaller than the space it already
			// occupies.
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
		// A new role must name a disk this machine actually has. The
		// device path only matters at claim time, which is the boot
		// that this staging prepares for.
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
// Machine document with no status. The document carries the whole
// spec, including sysctls and network settings, so the reboot
// converges everything, even though only storage triggers the
// reboot. The rendering is deterministic: sigs.k8s.io/yaml marshals
// through JSON with sorted keys, so the same spec always produces
// the same bytes. The hash of those bytes is the spec's identity
// everywhere: in staging idempotence, in rejections, and in the
// facts.
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

// A turn is the machine's standing with the rollout conductor
// (rollout.go): whether the machine may reboot right now. A machine
// with no cluster document has no conductor, so it reboots whenever
// it needs to. A cluster member waits until the conductor writes a
// RebootApproved condition onto it. Only rebootPolicy: Auto ever
// checks this. A Manual machine waits for a person regardless of its
// turn.
type turn int

const (
	turnStandalone turn = iota // no cluster: reboots whenever it needs to
	turnAwaiting               // cluster member, waiting for a grant
	turnGranted                // cluster member, already granted
)

// A convergence is one reconcile pass's decision: the condition to
// publish and which side effects to perform. decideConvergence
// decides; reconcile() acts.
type convergence struct {
	condition      api.Condition
	stage          bool   // write the manifest to the machineState filesystem
	requestReboot  bool   // write the reboot intent for init
	requestRestart bool   // write the restart intent; a k3s restart applies it
	requestLoad    bool   // write the modules intent; init loads the staged additions while the system runs
	withdraw       bool   // remove the staged manifest; the spec no longer wants it
	clearRejection bool   // remove the rejection record; the spec it blocks is gone
	manifest       []byte // the bytes to stage
	hash           string // the bytes' identity
}

// The condition constructors for every convergence verdict. Three
// documents converge through this machinery, each under its own
// condition type (SpecConverged, ClusterConverged, VersionConverged).
// All three share one set of reasons, so the constructors take the
// type as a parameter instead of hard-coding it.
func converged(condType, reason, message string) api.Condition {
	return api.Condition{Type: condType, Status: api.ConditionTrue, Reason: reason, Message: message}
}

func notConverged(condType, reason, message string) api.Condition {
	return api.Condition{Type: condType, Status: api.ConditionFalse, Reason: reason, Message: message}
}

func convergenceUnknown(condType, reason, message string) api.Condition {
	return api.Condition{Type: condType, Status: api.ConditionUnknown, Reason: reason, Message: message}
}

// The convergence constructors for the verdicts that every document's
// decision table shares. The decision tables mirror one another by
// design: they use the same guards in the same order, so a reader
// who has followed one document's convergence can follow them all.
// These constructors keep that mirroring exact, rather than
// accidental.

// factsIncomplete is the guard every decision starts with. With no
// facts, or with facts that carry no boot record (an older init, or
// a machine in the middle of an upgrade), the verdict is Unknown.
// Guessing here could reboot a machine because of a misreading.
func factsIncomplete(condType string) convergence {
	return convergence{condition: convergenceUnknown(condType, "FactsIncomplete",
		"the machine's facts carry no boot record yet")}
}

// machineStateEphemeral is the verdict for when there is nowhere
// durable to stage a document. The machineState role is backed by
// memory, so anything staged would disappear at the next reboot,
// which is exactly when it would be needed. what names the document
// that has nowhere to go.
func machineStateEphemeral(condType, what string) convergence {
	return convergence{condition: notConverged(condType, "MachineStateEphemeral",
		fmt.Sprintf("machineState is backed by memory; there is no durable filesystem to stage %s into; declare machineState in the machine's manifest", what))}
}

// convergedWithCleanup wraps a True verdict with the cleanup that
// every document performs on convergence. When a manifest is still
// staged for a spec the cluster no longer wants, this function
// withdraws it, because the next boot would otherwise apply it. This
// function also clears a standing rejection for the same reason: the
// spec it blocks is no longer requested, so the record no longer
// blocks anything.
func convergedWithCleanup(cond api.Condition, stagedHash string, rejection *machine.Rejection) convergence {
	return convergence{
		condition:      cond,
		withdraw:       stagedHash != "",
		clearRejection: rejection != nil,
	}
}

// gateDisruption finishes a staged document's convergence. The
// staged bytes are already in the convergence, and what remains is
// whether this machine may take its disruption right now. The
// decision table is the same for every staged document, and it is
// the safety core of the rollout design, so this function holds it
// in one place. Manual policy always waits for a person. A cluster
// member on Auto waits for the conductor's turn (AwaitingTurn is the
// same reason for both kinds of disruption, which lets the conductor
// sequence them without knowing the difference between them). Only a
// standalone machine, or a machine that has been granted a turn,
// asks init to act. The restart flag picks the kind of disruption: a
// k3s restart for changes that k3s reads only when its process
// starts, and a machine reboot for everything else. A leader's
// restart still restarts the embedded datastore. This is the same
// exposure to a lost quorum that a reboot has, so restarts wait for
// the same turns as reboots do. The messages differ for each
// document, but the reasons and their order of precedence must not.
func gateDisruption(c *convergence, condType string, policy machine.RebootPolicy, t turn, restart bool, pending, awaiting, requested string) {
	pendingReason, requestedReason := "RebootPending", "RebootRequested"
	if restart {
		pendingReason, requestedReason = "RestartPending", "RestartRequested"
	}
	switch {
	case policy != machine.RebootAuto:
		c.condition = notConverged(condType, pendingReason, pending)
	case t == turnAwaiting:
		c.condition = notConverged(condType, "AwaitingTurn", awaiting)
	default:
		c.requestReboot = !restart
		c.requestRestart = restart
		c.condition = notConverged(condType, requestedReason, requested)
	}
}

// decideConvergence makes the whole convergence decision in one pure
// function. The cases run in this order, and each one stops the
// function as soon as it applies:
//
//  1. No facts, or facts with no boot record (an older init, or a
//     machine in the middle of an upgrade): the verdict is Unknown.
//     Guessing here could reboot a machine because of a misreading.
//  2. No drift: the verdict is converged. This case also cleans up
//     after an edit that was reverted. When a manifest is still
//     staged for a spec the cluster no longer wants, this case
//     withdraws it, because the next boot would otherwise apply it.
//     This case also clears a standing rejection for the same
//     reason: the spec it blocks is no longer requested, so the
//     record no longer blocks anything.
//  3. The desired spec is the one init rejected: the function
//     refuses to stage it again. The rejection parameter comes from
//     the durable quarantine record on machineState, not from
//     facts. Facts are a snapshot taken at boot, and they do not
//     change while the machine runs. But when an edit is reverted
//     and then retried within one boot, the clearing of the
//     rejection must take effect right away, not at the next
//     reboot. Only a genuinely different edit, or clearing the
//     record through convergence, unblocks the hash.
//  4. The facts claim this exact manifest was actuated, yet drift
//     still computes: this is a contradiction, and it can only mean
//     a liken bug. Holding in a stuck condition is better than
//     rebooting the machine on every reconcile pass.
//  5. machineState is backed by memory: there is nowhere durable to
//     stage a document.
//  6. The spec fails validation against the machine's reality.
//  7. Valid drift: the function stages the manifest, unless these
//     exact bytes are already staged. Then, following rebootPolicy
//     and the machine's turn, it requests the reboot, waits for the
//     cluster's grant, or reports that a reboot is pending.
func decideConvergence(m *machine.Machine, facts *machine.MachineStatus, rejection *machine.Rejection, stagedHash string, t turn) convergence {
	if facts == nil || facts.Boot.ManifestSource == "" {
		return factsIncomplete("SpecConverged")
	}

	storageDiffs := machine.StorageDrift(m.Spec.Storage, facts.Boot.Storage)
	drift := append(storageDiffs, machine.ModulesDrift(m.Spec.Modules, facts.Boot.Modules)...)
	if len(drift) == 0 {
		return convergedWithCleanup(
			converged("SpecConverged", "Converged", "this boot actuated the current spec"),
			stagedHash, rejection)
	}
	diffs := strings.Join(drift, "; ")

	manifest, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingFailed", err.Error())}
	}

	if rejection != nil && rejection.Hash == hash {
		return convergence{condition: notConverged("SpecConverged", "RejectedLastBoot",
			fmt.Sprintf("init rejected this exact spec at boot: %s; edit the spec to something different", rejection.Reason))}
	}
	if facts.Boot.ManifestHash == hash {
		return convergence{condition: notConverged("SpecConverged", "BootMismatch",
			fmt.Sprintf("facts claim this spec was actuated, yet it differs from the boot's record (%s); refusing to reboot over a contradiction; this is a liken bug", diffs))}
	}
	if facts.Storage.MachineState.Backing != machine.BackingPartition {
		return machineStateEphemeral("SpecConverged", "a manifest")
	}
	if err := validateStaging(m.Spec.Storage, facts); err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingRejected", err.Error())}
	}

	c := convergence{
		manifest: manifest,
		hash:     hash,
		stage:    stagedHash != hash, // idempotence: skip the write when these exact bytes are already staged
	}

	// Adding modules is the one machine-spec change that needs no
	// disruption. Loading can happen while the system runs: the
	// kernel binds a resident driver to hardware that is already
	// plugged in, on its own. So when the storage is unchanged and
	// no module is being removed, the manifest stages for
	// durability, and init loads the additions into the running
	// kernel. This case needs no policy gate and no reboot turn, the
	// same as the sysctls the operator reconciles live: the gates
	// exist for disruptions, and this is not one. (Removing a module
	// still needs a reboot, because loading is one-way. The kernel
	// offers no safe way to remove a driver while something is using
	// it.)
	_, retracted := machine.ModuleSetDiff(m.Spec.Modules, facts.Boot.Modules)
	if len(storageDiffs) == 0 && len(retracted) == 0 {
		c.requestLoad = true
		c.condition = notConverged("SpecConverged", "LoadRequested",
			fmt.Sprintf("module load requested to apply the staged spec (%.12s) in place: %s", hash, diffs))
		return c
	}

	gateDisruption(&c, "SpecConverged", m.Spec.RebootPolicyOrDefault(), t, false,
		fmt.Sprintf("spec staged for the next boot (%.12s); rebootPolicy is Manual, so reboot the machine (or set rebootPolicy: Auto) to apply: %s", hash, diffs),
		fmt.Sprintf("spec staged for the next boot (%.12s); waiting for the cluster to grant a reboot turn: %s", hash, diffs),
		fmt.Sprintf("reboot requested to apply the staged spec (%.12s): %s", hash, diffs))
	return c
}

// readStagedHash returns the hash of the document currently staged
// in the store, or "" when nothing is staged. The function hashes
// staged bytes even when they fail to parse, because the idempotence
// check compares bytes, not parsed meaning.
func readStagedHash(store machine.ManifestStore) string {
	raw, _ := store.LoadStaged()
	if raw == nil {
		return ""
	}
	return machine.ManifestHash(raw)
}
