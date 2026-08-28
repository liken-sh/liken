package main

// Convergence: keeping the cluster's spec and the machine's boot in
// agreement.
//
// Sysctls reconcile live. Storage cannot, because the system cannot
// swap a filesystem under a running cluster. The network cannot
// either, because the cluster reaches this machine over the very
// addresses an edit changes: re-addressing a running machine would
// cut the connection that carries the next instruction, on the one
// kind of machine that has no shell to repair it from. The declared
// module list cannot reconcile live either, because loading a module
// is one-way: the kernel offers no safe way to remove a driver while
// something is using it. Storage, network, and modules therefore
// converge through a reboot. The operator stages the desired
// manifest onto the machineState filesystem, where the next boot
// finds it, tries it, and promotes or rejects it (machine/staging.go
// covers that side).
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
	"slices"
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
				// The message carries the remedy, because the person who
				// reads it is usually meeting this for the first time,
				// after a reinstall laid the disk out differently from
				// the Machine document that outlived it. The document is
				// authoritative, so the fix is always to edit the
				// document, and the size it must name is a size they can
				// paste.
				return fmt.Errorf("%s: the spec declares %s, and this machine's partition holds %s; storage roles are grow-only, so declare %s or more. A machine reinstalled with a different layout needs its Machine document edited to the layout it now carries",
					role.Name, role.Size, machine.SizeText(placed.CapacityBytes), machine.SizeText(placed.CapacityBytes))
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
// spec, including the sysctls that need no reboot at all, so the
// reboot converges everything at once. The rendering is
// deterministic: sigs.k8s.io/yaml marshals through JSON with sorted
// keys, so the same spec always produces the same bytes. The hash of
// those bytes is the spec's identity everywhere: in staging
// idempotence, in rejections, and in the facts.
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
// RebootApproved condition onto it.
// rebootPolicy: Auto checks this, and so does a Manual machine that
// a person has approved through the approve-disruption annotation:
// the approval moves the machine onto the same turn-taking path.
type turn int

const (
	turnStandalone turn = iota // no cluster: reboots whenever it needs to
	turnAwaiting               // cluster member, waiting for a grant
	turnGranted                // cluster member, already granted
)

// A convergence is one reconcile pass's decision: the condition to
// publish and which side effects to perform. decideConvergence
// makes the decision; reconcile() acts.
type convergence struct {
	condition      api.Condition
	stage          bool                       // write the manifest to the machineState filesystem
	requestReboot  bool                       // write the reboot intent for init
	requestRestart bool                       // write the restart intent; a k3s restart applies it
	requestLoad    bool                       // write the modules intent; init loads the staged additions while the system runs
	withdraw       bool                       // remove the staged manifest; the spec no longer names it
	clearRejection bool                       // remove the rejection record; the spec it blocks is gone
	manifest       []byte                     // the bytes to stage
	hash           string                     // the bytes' identity
	pending        *machine.PendingDisruption // the status.pending entry for this document, when one waits
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
// staged for a spec the cluster no longer requests, this function
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
// in one place. Manual policy waits for a person, and the
// approve-disruption annotation is how the person answers: an
// approval naming the staged hash moves the machine onto the same
// path Auto takes, through the conductor's turn and the drain,
// which is safer than the state it replaces, where an operator
// following the machine's own advice cuts power and no budget
// applies at all. An approval naming some other hash is reported,
// not ignored: the pending message carries both values, so a wrong
// paste is visible where the person is already looking. A cluster
// member on Auto (or approved Manual) waits for the conductor's
// turn (AwaitingTurn is the same reason for both kinds of
// disruption, which lets the conductor sequence them without
// knowing the difference between them). Only a standalone machine,
// or a machine that has been granted a turn, asks init to act. The
// restart flag picks the kind of disruption: a k3s restart for
// changes that k3s reads only when its process starts, and a
// machine reboot for everything else. A leader's restart still
// restarts the embedded datastore. This is the same exposure to a
// lost quorum that a reboot has, so restarts wait for the same
// turns as reboots do. Every branch also records the document in
// status.pending, because a staged document waits for its
// disruption until the disruption runs, whatever it waits on. The
// messages differ for each document, but the reasons and their
// order of precedence must not.
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
	case policy != machine.RebootAuto && !machine.ApprovalGrants(approval, c.hash):
		message := pending
		if approval != "" {
			message = fmt.Sprintf("%s; the %s annotation names %s, and the staged document is %.12s",
				pending, machine.ApproveDisruptionAnnotation, approval, c.hash)
		}
		c.condition = notConverged(condType, pendingReason, message)
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
//     staged for a spec the cluster no longer requests, this case
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
//     a liken bug, with one exception. Parameter-only drift under a
//     matching hash is a state a live load produces by design: it
//     promotes the manifest and records only the parameters it
//     delivered, so an undelivered one drifts toward the reboot
//     that can deliver it (init/liveload.go). That shape takes the
//     staged path below. Every other drift shape under a matching
//     hash holds in the stuck condition, because holding is better
//     than rebooting the machine on every reconcile pass.
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
	networkDiffs := machine.NetworkDrift(m.Spec.Network, facts.Boot.Network)
	added, retracted := machine.ModuleSetDiff(m.Spec.Modules, facts.Boot.Modules)
	parameterDiffs := machine.ModuleParameterDrift(m.Spec.Modules, facts.Boot.Modules,
		m.Spec.ModuleParameters, facts.Boot.ModuleParameters)
	drift := slices.Concat(storageDiffs, networkDiffs,
		machine.ModulesDrift(m.Spec.Modules, facts.Boot.Modules,
			m.Spec.ModuleParameters, facts.Boot.ModuleParameters),
		machine.RlimitDrift(m.Spec.Rlimits, facts.Boot.Rlimits))
	// The order difference stays apart from drift and never joins it.
	// The lines in drift are the count that decides the live-load
	// tier below, and every line there names something a load or a
	// reboot can actuate. Only a boot actuates a reorder, and a
	// reorder is worth no boot of its own, so it takes neither tier.
	// It stages, and any later boot loads the list in the order the
	// spec now gives.
	orderDiffs := machine.ModuleOrderDrift(m.Spec.Modules, facts.Boot.Modules)
	if len(drift) == 0 && len(orderDiffs) == 0 {
		return convergedWithCleanup(
			converged("SpecConverged", "Converged", "this boot actuated the current spec"),
			stagedHash, rejection)
	}
	diffs := strings.Join(slices.Concat(drift, orderDiffs), "; ")

	manifest, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingFailed", err.Error())}
	}

	if rejection != nil && rejection.Hash == hash {
		return convergence{condition: notConverged("SpecConverged", "RejectedLastBoot",
			fmt.Sprintf("init rejected this exact spec at boot: %s; edit the spec to something different", rejection.Reason))}
	}
	// A matching hash may legitimately carry two shapes, and both are
	// what a live load leaves behind. The first is a parameter the
	// load could not deliver (list item 4 above): every drift line is
	// a module parameter on an unchanged module set. The second is an
	// order the load could not change, because the modules the boot
	// loaded keep their places in the running kernel. An order
	// difference lives outside drift, so it passes this test with no
	// term of its own. The test is structural, a count over the
	// drift's parts, so no drift text is ever matched and any other
	// shape stays a contradiction.
	parametersOnly := len(storageDiffs) == 0 && len(networkDiffs) == 0 &&
		len(added) == 0 && len(retracted) == 0 && len(parameterDiffs) == len(drift)
	if facts.Boot.ManifestHash == hash && !parametersOnly {
		return convergence{condition: notConverged("SpecConverged", "BootMismatch",
			fmt.Sprintf("facts claim this spec was actuated, yet it differs from the boot's record (%s); refusing to reboot over a contradiction; this is a liken bug", diffs))}
	}
	if facts.Storage.MachineState.Backing != machine.BackingPartition {
		return machineStateEphemeral("SpecConverged", "a manifest")
	}
	// The network spec is checked here as well as at boot. A spec
	// that init would refuse is a spec that costs a reboot to find
	// out about, and the machine would come back on its proven
	// manifest with a rejection record instead of the network the
	// person asked for. The check is the manifest's own consistency
	// only: whether this machine really has a port with a declared
	// name is a question only the boot can settle.
	if err := m.Spec.Network.Validate(); err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingRejected", err.Error())}
	}
	// Resource limits are checked for the same reason. Init would skip
	// a limit it cannot apply rather than refuse the boot, so a typo
	// here costs a reboot and then applies nothing at all, with only a
	// console line to say why. Refusing to stage it says so in a
	// condition instead.
	if err := machine.ValidateRlimits(m.Spec.Rlimits); err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingRejected", err.Error())}
	}
	if err := validateStaging(m.Spec.Storage, facts); err != nil {
		return convergence{condition: notConverged("SpecConverged", "StagingRejected", err.Error())}
	}

	c := convergence{
		manifest: manifest,
		hash:     hash,
		stage:    stagedHash != hash, // idempotence: skip the write when these exact bytes are already staged
	}

	// The next-boot tier comes first, because a difference in nothing
	// but the declared order is the lightest answer here. Nothing can
	// load a resident module again in a new place, so no load applies
	// a reorder. Nothing on the machine is wrong while the reorder
	// waits, so no reboot is worth asking for. Staging is the whole of
	// the work, and the next boot, whatever its cause, loads the list
	// in the new order. The cluster document has the same tier for the
	// same reason (cluster.go).
	if len(drift) == 0 {
		c.condition = notConverged("SpecConverged", "StagedForNextBoot",
			fmt.Sprintf("spec staged for the next boot (%.12s); the machine loads the modules in the new order at its next boot and asks for no turn: %s", hash, diffs))
		return c
	}

	// Adding modules is the one machine-spec change that needs no
	// disruption. Loading can happen while the system runs: the
	// kernel binds a resident driver to hardware that is already
	// plugged in, on its own. So when every difference is an added
	// module, the manifest stages for durability, and init loads the
	// additions into the running kernel. This case needs no policy
	// gate and no reboot turn, the same as the sysctls the operator
	// reconciles live: the gates exist for disruptions, and this is
	// not one. (Removing a module still needs a reboot, because
	// loading is one-way. The kernel offers no safe way to remove a
	// driver while something is using it.)
	//
	// The test counts rather than naming the fields that must be
	// unchanged. ModulesDrift writes exactly one line for each added
	// module, so the counts match only when the added modules are the
	// whole of the drift. This makes the safety property structural,
	// the same way RestartApplies does it for the cluster document: a
	// new spec field is reboot-class from the day it is added,
	// because its diffs land in drift and never in added. Naming the
	// fields instead would make a forgotten field silently live-class,
	// and init's live loader promotes the staged manifest to proven
	// whether or not it applied anything. A machine would then report
	// itself converged on a spec it never actuated.
	//
	// A declared reorder rides along with the additions. The load
	// applies the additions in the order the manifest lists them, and
	// the reorder of what the boot already loaded stays staged for the
	// next boot.
	if len(retracted) == 0 && len(drift) == len(added) {
		c.requestLoad = true
		c.condition = notConverged("SpecConverged", "LoadRequested",
			fmt.Sprintf("module load requested to apply the staged spec (%.12s) in place: %s", hash, diffs))
		return c
	}

	gateDisruption(&c, "SpecConverged", m.Spec.RebootPolicyOrDefault(), t, false,
		m.Metadata.Annotations[machine.ApproveDisruptionAnnotation],
		"the machine spec: "+diffs,
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
