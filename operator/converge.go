package main

// Convergence: closing the loop between the cluster's spec and the
// machine's boot.
//
// Sysctls reconcile live; storage cannot (a filesystem can't be
// swapped under a running cluster), so a storage edit converges by
// reboot: the operator stages the desired manifest onto the
// machineState filesystem, where the next boot finds it, tries it,
// and promotes or rejects it (machine/staging.go tells that side).
// This file is the operator's half: notice drift, refuse what the
// machine can't satisfy, stage what it can, and either request the
// reboot (rebootPolicy: Auto) or report that one is pending (Manual,
// the default).
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
// actuated, role by role, sizes normalized (2048Mi and 2Gi are the
// same ask). The returned diffs are written for humans; they appear
// verbatim in condition messages. Sysctls never count as drift: the
// operator already reconciles those live, and rebooting to apply
// something already applied would be absurd.
func storageDrift(desired, actuated machine.StorageSpec) []string {
	var diffs []string
	desiredRoles := rolesByName(desired)
	actuatedRoles := rolesByName(actuated)
	for _, name := range []string{"machineState", "machineEphemeral", "clusterState", "podStorage", "podEphemeral"} {
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

func rolesByName(spec machine.StorageSpec) map[string]machine.DeclaredRole {
	byName := map[string]machine.DeclaredRole{}
	for _, role := range spec.Roles() {
		byName[role.Name] = role
	}
	return byName
}

// sameSize compares two size declarations by what they mean, not how
// they're spelled. An unparseable size (which validation will refuse
// anyway) falls back to string comparison rather than panicking here.
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

// validateStaging is everything admission can't check, because it
// takes the actual machine: CEL rules in the CRD compare the spec
// against the last boot's published status, but only the facts know
// what partitions exist and what disks are attached.
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
// storage triggers it) and no status. Deterministic: sigs.k8s.io/yaml
// marshals through JSON with sorted keys, so the same spec always
// yields the same bytes, and the hash of those bytes is the spec's
// identity everywhere (staging idempotence, rejections, facts).
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

func converged(reason, message string) machine.Condition {
	return machine.Condition{Type: "SpecConverged", Status: "True", Reason: reason, Message: message}
}

func notConverged(reason, message string) machine.Condition {
	return machine.Condition{Type: "SpecConverged", Status: "False", Reason: reason, Message: message}
}

// decideConvergence is the whole feature as one decision. The cases,
// in the order they short-circuit:
//
//  1. No facts, or facts without a boot record (an older init, a
//     machine mid-upgrade): Unknown. Guessing could reboot a machine
//     over a misreading.
//  2. No drift: converged. This case also cleans up after an edit
//     that was taken back. A manifest still staged for a spec the
//     cluster no longer wants is withdrawn, because the next boot
//     would otherwise apply it. A standing rejection is cleared for
//     the same reason: the spec it blocks is no longer being asked
//     for, so the record has nothing left to do.
//  3. The desired spec is the one init rejected: refuse to re-stage
//     it. The rejection parameter is read from the durable quarantine
//     record on machineState, not from facts — facts are the boot's
//     frozen memory, and an edit that reverts and then retries within
//     one boot must see the clearing take effect immediately, not at
//     the next reboot. Only a genuinely different edit (or clearing
//     the record by converging) unblocks the hash.
//  4. Facts claim this exact manifest was actuated, yet drift still
//     computes: a contradiction, necessarily a liken bug. A wedged
//     condition beats a machine rebooting every reconcile pass.
//  5. machineState is memory-backed: there is nowhere durable to
//     stage into.
//  6. The spec fails validation against the machine's reality.
//  7. Valid drift: stage (unless these exact bytes already are), and
//     per rebootPolicy either request the reboot or report one
//     pending.
func decideConvergence(m *machine.Machine, facts *machine.MachineStatus, rejection *machine.Rejection, stagedHash string) convergence {
	if facts == nil || facts.Boot.ManifestSource == "" {
		return convergence{condition: machine.Condition{
			Type: "SpecConverged", Status: "Unknown", Reason: "FactsIncomplete",
			Message: "the machine's facts carry no boot record yet",
		}}
	}

	drift := storageDrift(m.Spec.Storage, facts.Boot.Storage)
	if len(drift) == 0 {
		return convergence{
			condition:      converged("BootCurrent", "this boot actuated the current spec"),
			withdraw:       stagedHash != "",
			clearRejection: rejection != nil,
		}
	}
	diffs := strings.Join(drift, "; ")

	manifest, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		return convergence{condition: notConverged("StagingFailed", err.Error())}
	}

	if r := rejection; r != nil && r.Hash == hash {
		return convergence{condition: notConverged("RejectedLastBoot",
			fmt.Sprintf("init rejected this exact spec at boot: %s; edit the spec to something different", r.Reason))}
	}
	if facts.Boot.ManifestHash == hash {
		return convergence{condition: notConverged("BootMismatch",
			fmt.Sprintf("facts claim this spec was actuated, yet it differs from the boot's storage (%s); refusing to reboot over a contradiction — this is a liken bug", diffs))}
	}
	if facts.Storage.MachineState.Backing != machine.BackingPartition {
		return convergence{condition: notConverged("MachineStateEphemeral",
			"machineState is backed by memory; there is no durable filesystem to stage a manifest into — declare machineState in the machine's manifest")}
	}
	if err := validateStaging(m.Spec.Storage, facts); err != nil {
		return convergence{condition: notConverged("StagingRejected", err.Error())}
	}

	c := convergence{
		manifest: manifest,
		hash:     hash,
		stage:    stagedHash != hash, // idempotence: no disk churn when these bytes already wait
	}
	if m.Spec.RebootPolicyOrDefault() == machine.RebootAuto {
		c.requestReboot = true
		c.condition = notConverged("RebootRequested",
			fmt.Sprintf("reboot requested to apply the staged spec (%.12s): %s", hash, diffs))
	} else {
		c.condition = notConverged("RebootPending",
			fmt.Sprintf("spec staged for the next boot (%.12s); rebootPolicy is Manual, so reboot the machine (or set rebootPolicy: Auto) to apply: %s", hash, diffs))
	}
	return c
}

// readStagedHash is the identity of whatever currently waits on the
// machineState filesystem, "" when nothing does. Unparseable staged
// bytes still hash: idempotence is about bytes, not meaning.
func readStagedHash() string {
	raw, _ := machine.MachineManifests(machine.MachineStateDir).LoadStaged()
	if raw == nil {
		return ""
	}
	return machine.ManifestHash(raw)
}
