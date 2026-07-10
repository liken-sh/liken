package main

// Version convergence: moving a machine toward the cluster's target
// release.
//
// The Cluster declares one target version (spec.version) and the
// catalog that vouches for it; each machine's operator compares the
// version its boot reported against that target, live, every pass.
// There is nothing to compare on the Machine's spec, because machines
// carry no version field. That is what makes an upgrade one edit
// instead of one per machine.
//
// Convergence here is a download, not a reboot: the operator brings
// the release's artifacts onto the machine's inactive system slot and
// verifies every byte against the catalog's digest chain. The reboot
// that makes the downloaded release the running one comes later (the
// proving reboot); this file's job ends with verified bytes sitting
// on the other slot.
//
// The decisions are pure functions, reconcile supplies the I/O, and
// the fetch itself runs on its own goroutine (fetch.go). A blocking
// 100MB GET inside a reconcile pass would starve the heartbeat lease,
// and the fleet would read the silence as a dead machine.

import (
	"fmt"
	"os"

	"github.com/liken-sh/liken/machine"
)

// versionAsk decides whether this machine should be downloading a
// release, and which one: the ask is the fetcher's work order. When
// the answer is no (converged, no target, or a machine that can't
// take a release), the returned condition is the whole verdict and
// ok is false.
//
// The short-circuit order mirrors decideConvergence's: the
// permanently blocked cases are checked before the in-progress ones,
// so a machine that can never comply reports that instead of
// reporting an attempt that could not succeed.
func versionAsk(cluster *machine.Cluster, facts *machine.MachineStatus) (fetchAsk, machine.Condition, bool) {
	none := fetchAsk{}
	if facts == nil {
		return none, convergenceUnknown("VersionConverged", "FactsIncomplete",
			"the machine's facts haven't been read yet"), false
	}

	target := cluster.Spec.Version
	if target == "" {
		return none, converged("VersionConverged", "NoTarget",
			"the cluster declares no target version"), false
	}
	if facts.Version.Liken == target {
		return none, converged("VersionConverged", "Converged",
			fmt.Sprintf("this machine runs the cluster's target version %s", target)), false
	}

	// The catalog admission rule makes this unreachable through the
	// API, but the operator checks anyway: the lookup can fail, so
	// the failure has to be handled, whatever the API server
	// promised.
	entry := cluster.Spec.Releases.Entry(target)
	if entry == nil {
		return none, notConverged("VersionConverged", "VersionNotInCatalog",
			fmt.Sprintf("the target version %s is not in the release catalog", target)), false
	}
	if cluster.Spec.Releases.Source == "" {
		return none, notConverged("VersionConverged", "NoReleaseSource",
			"the catalog names releases but spec.releases.source gives nowhere to fetch them from"), false
	}

	// A release needs somewhere to land: both slots claimed and
	// partition-backed. It also needs the machine to be running from
	// a slot, because a machine that didn't boot from a slot has no
	// boot entries to reboot through, so a download could never
	// become a boot.
	if facts.Storage.SystemA.Backing != machine.BackingPartition ||
		facts.Storage.SystemB.Backing != machine.BackingPartition {
		return none, notConverged("VersionConverged", "NoSystemSlots",
			"this machine has no system slots to hold a release; declare systemA and systemB in its manifest"), false
	}
	slot := machine.InactiveSlot(facts.Boot.Slot)
	if slot == "" {
		return none, notConverged("VersionConverged", "NotInstalled",
			"this boot didn't come from a system slot; install the machine (make install) before it can take releases"), false
	}

	return fetchAsk{
		version: target,
		digest:  entry.Digest,
		source:  cluster.Spec.Releases.Source,
		slot:    slot,
		slotDir: machine.SystemSlotDir(slot),
		// The running slot lends its deployment layer to the download:
		// the release supplies the OS, the machine supplies itself.
		activeSlotDir: machine.SystemSlotDir(facts.Boot.Slot),
	}, machine.Condition{}, true
}

// versionCondition turns the fetcher's answer about an ask into the
// VersionConverged condition, for every state short of verified.
// (decideSystemStaging owns the verified state, because a verified
// download is reported through its staged record.) Every state here
// means "not converged yet"; what differs is whether time will fix
// it. A failed fetch deliberately reads as Downloading: a down
// release server is transient by definition, the fetcher retries
// every pass, and the condition's message says what failed. A digest
// mismatch is the opposite. Refetching can't change what the server
// publishes, so the machine holds at DigestMismatch (phase Blocked)
// until the catalog names different bytes, and nothing is ever
// staged.
func versionCondition(ask fetchAsk, snap fetchSnapshot) machine.Condition {
	switch snap.state {
	case fetchRejected:
		return notConverged("VersionConverged", "DigestMismatch", snap.detail)
	case fetchFailed:
		return notConverged("VersionConverged", "Downloading",
			fmt.Sprintf("downloading release %s to slot %s; will retry: %s", ask.version, ask.slot, snap.detail))
	}
	return notConverged("VersionConverged", "Downloading",
		fmt.Sprintf("downloading release %s to slot %s: %s", ask.version, ask.slot, snap.detail))
}

// versionConvergence wraps a short-circuit verdict from versionAsk
// into a convergence. A True verdict (converged, or no target at all)
// also cleans up: a staged record left behind would reboot the
// machine into an upgrade nobody is asking for anymore, and a
// standing rejection no longer blocks anything.
func versionConvergence(cond machine.Condition, stagedHash string, rejection *machine.Rejection) convergence {
	if cond.Status == machine.ConditionTrue {
		return convergedWithCleanup(cond, stagedHash, rejection)
	}
	return convergence{condition: cond}
}

// convergeSystemRelease is the version target's part of one reconcile
// pass: load this machine's durable rejection and staged record from
// the store, ask the fetcher where the download stands, and decide.
// The version target converges through its own machinery — a download
// aimed at the inactive slot. The download runs on the fetcher's
// goroutine so that no reconcile pass, and no heartbeat, ever waits
// on a socket (versionAsk decides, fetch.go moves the bytes). Once
// the download verifies, the rest works like the other documents: a
// staged SystemRelease record, the reboot chain, the drain gate, and
// the same carryOutConvergence.
func convergeSystemRelease(store machine.ManifestStore, liveCluster *machine.Cluster, m *machine.Machine, facts *machine.MachineStatus, f *fetcher, t turn) convergence {
	rejection, _ := store.LoadRejection()
	stagedHash := readStagedHash(store)
	ask, cond, ok := versionAsk(liveCluster, facts)
	if !ok {
		return versionConvergence(cond, stagedHash, rejection)
	}
	return decideSystemStaging(ask, f.Ensure(ask), m, rejection, stagedHash, t)
}

// decideSystemStaging finishes version convergence: a verified
// download becomes a staged SystemRelease record, and that record
// goes through exactly the reboot machinery every other staged
// document does. Manual policy reports RebootPending, a cluster
// member awaits its turn from the rollout conductor, and a granted
// turn requests the reboot, gated through the drain like all the
// rest. The proving boot is the reboot itself: init arms the
// firmware's BootNext at the staged slot on the way down, and the
// operator that comes up running the new release is the proof that
// promotes the record.
func decideSystemStaging(ask fetchAsk, snap fetchSnapshot, m *machine.Machine, rejection *machine.Rejection, stagedHash string, t turn) convergence {
	if snap.state != fetchVerified {
		return convergence{condition: versionCondition(ask, snap)}
	}

	record, hash, err := machine.RenderSystemRelease(ask.version, ask.slot, ask.digest)
	if err != nil {
		return convergence{condition: notConverged("VersionConverged", "StagingFailed", err.Error())}
	}
	// The rejection is durable memory of a trial that fell back: the
	// machine booted the staged slot and the firmware returned it to
	// the proven one. Refusing to re-stage the identical decision is
	// what breaks the reboot loop; a new version or a republished
	// digest is a different decision and passes.
	if rejection != nil && rejection.Hash == hash {
		return convergence{condition: notConverged("VersionConverged", "RejectedLastBoot",
			fmt.Sprintf("the machine tried release %s on slot %s and fell back: %s; publish a corrected release under a new version",
				ask.version, ask.slot, rejection.Reason))}
	}

	c := convergence{manifest: record, hash: hash, stage: stagedHash != hash}
	gateDisruption(&c, "VersionConverged", m.Spec.RebootPolicyOrDefault(), t, false,
		fmt.Sprintf("release %s is verified on slot %s and staged (%.12s); rebootPolicy is Manual, so reboot the machine (or set rebootPolicy: Auto) to prove it", ask.version, ask.slot, hash),
		fmt.Sprintf("release %s is verified on slot %s and staged (%.12s); waiting for the cluster to grant a reboot turn", ask.version, ask.slot, hash),
		fmt.Sprintf("reboot requested to prove release %s on slot %s (%.12s)", ask.version, ask.slot, hash))
	return c
}

// settleSystemReleaseLifecycle promotes what this boot proved, the
// way settleClusterLifecycle does for the cluster document: the
// operator's own existence is the evidence. If this pass is
// executing, then the kernel, init, k3s, and the machine's place in
// its cluster all work. And if the version this boot reported
// matches the staged record for the very slot it came from, then the
// trial release is what is doing that work, so the record is
// promoted.
//
// The comparison is against the *facts*, meaning init's version
// stamp for the OS actually running, and deliberately not this
// binary's own stamp (machine.Version). The operator pod comes from
// whatever image the cluster's DaemonSet pins, and in a mixed fleet
// that pin lags the OS: the proving boot of a new release runs the
// *old* operator until the leaders themselves upgrade. Judging by
// the pod's stamp would therefore veto every promotion a mixed fleet
// attempts. Worse, the unpromoted staged record would look stale to
// a machine that considers itself converged, be withdrawn, and be
// re-staged on the next pass, so the machine would re-upgrade itself
// on every boot, forever. The pod is a bystander to the trial; the
// machine is what is being proved.
//
// A machine running from a slot with no record at all (its install
// predates any catalog) writes its current standing down as the first
// proven record, so init's every-boot BootOrder repair has an
// authority to enforce from the start.
func settleSystemReleaseLifecycle(root string, facts *machine.MachineStatus) {
	if facts == nil || facts.Storage.MachineState.Backing != machine.BackingPartition ||
		facts.Boot.Slot == "" || facts.Version.Liken == "" {
		return
	}
	store := machine.SystemReleases(root)

	if staged, _ := store.LoadStaged(); staged != nil {
		record, err := machine.ParseSystemRelease(staged)
		if err != nil {
			return // init vets staged records at boot; the operator doesn't judge them
		}
		if record.Slot != facts.Boot.Slot || record.Version != facts.Version.Liken {
			return // not this boot's trial; nothing proved
		}
		if err := store.Promote(); err != nil {
			fmt.Fprintf(os.Stderr, "promoting the system release: %v\n", err)
			return
		}
		fmt.Printf("release %s proved out on slot %s; the store now names it proven\n",
			record.Version, record.Slot)
		return
	}

	if proven, err := store.LoadProven(); proven != nil || err != nil {
		return
	}
	raw, _, err := machine.RenderSystemRelease(facts.Version.Liken, facts.Boot.Slot, "")
	if err != nil {
		return
	}
	if err := store.WriteProven(raw); err != nil {
		fmt.Fprintf(os.Stderr, "recording the running release as proven: %v\n", err)
		return
	}
	fmt.Printf("recorded the running release %s on slot %s as proven\n", facts.Version.Liken, facts.Boot.Slot)
}
