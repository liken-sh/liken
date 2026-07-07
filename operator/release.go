package main

// Version convergence: moving a machine toward the cluster's target
// release.
//
// The Cluster declares one target version (spec.version) and the
// catalog that vouches for it; each machine's operator compares the
// version its boot reported against that target, live, every pass.
// There is nothing to compare on the Machine's spec — machines carry
// no version field, which is what makes an upgrade one edit instead
// of one per machine.
//
// Convergence here is a download, not a reboot: the operator brings
// the release's artifacts onto the machine's inactive system slot and
// verifies every byte against the catalog's digest chain. The reboot
// that makes the downloaded release the running one is the next
// chapter (the proving reboot); this file's job ends with verified
// bytes waiting on the other slot.
//
// The decisions are pure functions, reconcile supplies the I/O, and
// the fetch itself runs on its own goroutine (fetch.go) — a blocking
// 100MB GET inside a reconcile pass would starve the heartbeat lease
// and read as a death, which is milestone 10's lesson made
// structural.

import (
	"fmt"

	"github.com/chrisguidry/liken/machine"
)

func versionConverged(status machine.ConditionStatus, reason, message string) machine.Condition {
	return machine.Condition{Type: "VersionConverged", Status: status, Reason: reason, Message: message}
}

// versionAsk decides whether this machine should be downloading a
// release, and which one: the ask is the fetcher's work order. When
// the answer is no — converged, no target, or a machine that can't
// take a release — the returned condition is the whole verdict and
// ok is false.
//
// The short-circuit order mirrors decideConvergence's: tell the
// blocked stories before the busy ones, so a machine that can never
// comply says so instead of pretending to try.
func versionAsk(cluster *machine.Cluster, facts *machine.MachineStatus) (fetchAsk, machine.Condition, bool) {
	none := fetchAsk{}
	if facts == nil {
		return none, versionConverged("Unknown", "FactsIncomplete",
			"the machine's facts haven't been read yet"), false
	}

	target := cluster.Spec.Version
	if target == "" {
		return none, versionConverged("True", "NoTarget",
			"the cluster declares no target version"), false
	}
	if facts.Version.Liken == target {
		return none, versionConverged("True", "Converged",
			fmt.Sprintf("this machine runs the cluster's target version %s", target)), false
	}

	// The catalog admission rule makes this unreachable through the
	// API, but the operator checks anyway: an API server's promise is
	// not a substitute for handling the lookup that failed.
	entry := cluster.Spec.Releases.Entry(target)
	if entry == nil {
		return none, versionConverged("False", "VersionNotInCatalog",
			fmt.Sprintf("the target version %s is not in the release catalog", target)), false
	}
	if cluster.Spec.Releases.Source == "" {
		return none, versionConverged("False", "NoReleaseSource",
			"the catalog names releases but spec.releases.source gives nowhere to fetch them from"), false
	}

	// A release needs somewhere to land (both slots, claimed and
	// partition-backed) and a running slot to be the other half of:
	// a machine that didn't boot from a slot has no boot entries to
	// reboot through, so a download could never become a boot.
	if facts.Storage.SystemA.Backing != machine.BackingPartition ||
		facts.Storage.SystemB.Backing != machine.BackingPartition {
		return none, versionConverged("False", "NoSystemSlots",
			"this machine has no system slots to hold a release; declare systemA and systemB in its manifest"), false
	}
	slot := machine.InactiveSlot(facts.Boot.Slot)
	if slot == "" {
		return none, versionConverged("False", "NotInstalled",
			"this boot didn't come from a system slot; install the machine (make install) before it can take releases"), false
	}

	return fetchAsk{
		version: target,
		digest:  entry.Digest,
		source:  cluster.Spec.Releases.Source,
		slot:    slot,
		slotDir: machine.SystemSlotDir(slot),
	}, machine.Condition{}, true
}

// versionCondition turns the fetcher's answer about an ask into the
// VersionConverged condition. Every state here is "not converged yet";
// what differs is whether time will fix it. A failed fetch reads as
// Downloading on purpose: a down release server is transient by
// definition, the fetcher retries every pass, and the condition's
// message carries the story. A digest mismatch is the opposite —
// refetching can't change what the server publishes, so the machine
// holds at DigestMismatch (phase Blocked) until the catalog names
// different bytes, and nothing is ever staged.
func versionCondition(ask fetchAsk, snap fetchSnapshot) machine.Condition {
	switch snap.state {
	case fetchVerified:
		return versionConverged("False", "Fetched",
			fmt.Sprintf("release %s is verified on slot %s; %s", ask.version, ask.slot, snap.detail))
	case fetchRejected:
		return versionConverged("False", "DigestMismatch", snap.detail)
	case fetchFailed:
		return versionConverged("False", "Downloading",
			fmt.Sprintf("downloading release %s to slot %s; will retry: %s", ask.version, ask.slot, snap.detail))
	}
	return versionConverged("False", "Downloading",
		fmt.Sprintf("downloading release %s to slot %s: %s", ask.version, ask.slot, snap.detail))
}
