package main

// Version convergence moves a machine to the release in the Cluster's
// spec.version. A Machine has no version field, so an upgrade is one
// edit to the Cluster and not one edit per machine.
//
// When the version that init reports for this boot differs from the
// target, the operator downloads the release into the inactive system
// slot, verifies it against the digest in spec.releases.catalog, and
// stages a SystemRelease record. The reboot into that slot is the
// trial, and settleSystemReleaseLifecycle promotes the record when the
// proving boot runs the operator.
//
// The download runs on the fetcher's goroutine (fetch.go). A GET that
// blocked a pass would stop the heartbeat lease from renewing, and the
// cluster would treat the machine as dead.

import (
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// releaseToFetch returns the release that the fetcher should download,
// and true. When there is nothing to download, it returns the
// VersionConverged condition that gives the reason, and false. A
// machine that can never take the release, such as one with no system
// slots, gets a condition that says so in place of a download that
// would fail.
func releaseToFetch(clusterDoc *cluster.Cluster, facts *machine.MachineStatus) (releaseFetch, api.Condition, bool) {
	none := releaseFetch{}
	if facts == nil {
		return none, convergenceUnknown("VersionConverged", "FactsIncomplete",
			"The machine's facts are not read yet"), false
	}

	target := clusterDoc.Spec.Version
	if target == "" {
		return none, converged("VersionConverged", "NoTarget",
			"The Cluster declares no target version"), false
	}
	if facts.Version.Liken == target {
		return none, converged("VersionConverged", "Converged",
			fmt.Sprintf("This machine runs the target version %s of the Cluster", target)), false
	}

	entry := clusterDoc.Spec.Releases.Entry(target)
	if entry == nil {
		return none, notConverged("VersionConverged", "VersionNotInCatalog",
			fmt.Sprintf("The target version %s is not in the release catalog", target)), false
	}
	if clusterDoc.Spec.Releases.Source == "" {
		return none, notConverged("VersionConverged", "NoReleaseSource",
			"The catalog lists releases, but spec.releases.source is not set, so there is no server to download them from"), false
	}

	// The machine must also have booted from a system slot. Otherwise
	// it has no boot entries that could boot the downloaded slot.
	if facts.Storage.SystemA.Backing != machine.BackingPartition ||
		facts.Storage.SystemB.Backing != machine.BackingPartition {
		return none, notConverged("VersionConverged", "NoSystemSlots",
			"This machine has no system slots for a release. Declare systemA and systemB in its manifest"), false
	}
	slot := machine.InactiveSlot(facts.Boot.Slot)
	if slot == "" {
		return none, notConverged("VersionConverged", "NotInstalled",
			"This boot did not start from a system slot. Install the machine (make install) before it can download releases"), false
	}

	return releaseFetch{
		version: target,
		digest:  entry.Digest,
		source:  clusterDoc.Spec.Releases.Source,
		slot:    slot,
		slotDir: machine.SystemSlotDir(slot),
		// The deployment layer is specific to this machine, so the
		// release does not contain it. The fetcher copies it from the
		// running slot.
		activeSlotDir: machine.SystemSlotDir(facts.Boot.Slot),
	}, api.Condition{}, true
}

// versionCondition returns the VersionConverged condition for a
// download that has not verified.
//
// cluster-operator does not grant a turn to a machine whose
// reason is Downloading, so that the release reboots in the same turn
// as the machine's other staged changes (cluster-operator/requests.go).
// A release server that is down must not delay the turn, so a failed
// fetch reports DownloadRetrying, and the reason stays DownloadRetrying
// through every retry until a download verifies. A server that keeps
// failing then posts one Event. The message gives the time of the next
// attempt and not the time left, so it does not change between passes
// and the status is not written again.
//
// A digest mismatch reports DigestMismatch, and the machine stays
// Blocked until the catalog names different bytes, because fetching
// again cannot change what the server publishes.
func versionCondition(fetch releaseFetch, snap fetchSnapshot) api.Condition {
	switch snap.state {
	case fetchRejected:
		return notConverged("VersionConverged", "DigestMismatch", snap.detail)
	case fetchFailed:
		return notConverged("VersionConverged", "DownloadRetrying",
			fmt.Sprintf("Downloading release %s to slot %s, and the download retries at %s: %s",
				fetch.version, fetch.slot, snap.retryAt.UTC().Format(time.RFC3339), snap.detail))
	}
	if snap.retrying {
		return notConverged("VersionConverged", "DownloadRetrying",
			fmt.Sprintf("Downloading release %s to slot %s: %s", fetch.version, fetch.slot, snap.detail))
	}
	return notConverged("VersionConverged", "Downloading",
		fmt.Sprintf("Downloading release %s to slot %s: %s", fetch.version, fetch.slot, snap.detail))
}

// versionConvergence turns a condition from releaseToFetch into a
// convergence. When the condition is True, it also withdraws the
// staged record, which would otherwise reboot the machine into a
// release that the Cluster no longer targets, and clears the
// rejection.
func versionConvergence(cond api.Condition, stagedHash string, rejection *machine.Rejection) convergence {
	if cond.Status == api.ConditionTrue {
		return convergedWithCleanup(cond, stagedHash, rejection)
	}
	return convergence{condition: cond}
}

// convergeSystemRelease runs version convergence for one pass. It
// reads the stored rejection and staged record, reads from the fetcher
// the state of the download without waiting for it, and decides.
func convergeSystemRelease(store machine.ManifestStore, liveCluster *cluster.Cluster, m *machine.Machine, facts *machine.MachineStatus, f *fetcher, t turn, out *passOutcome) convergence {
	rejection, _ := store.LoadRejection()
	stagedHash := readStagedHash(store)
	fetch, cond, ok := releaseToFetch(liveCluster, facts)
	if !ok {
		return versionConvergence(cond, stagedHash, rejection)
	}
	stagedHash, err := withdrawOtherStage(store, fetch, stagedHash)
	if err != nil {
		out.fail("withdrawing another release's staged record", err)
		return convergence{condition: notConverged("VersionConverged", "StagingFailed",
			fmt.Sprintf("Release %s waits to download onto slot %s, because the staged record of another release cannot be withdrawn: %v",
				fetch.version, fetch.slot, err))}
	}
	snap := f.Ensure(fetch)
	if snap.state == fetchFailed {
		out.wakeBy(snap.retryAt)
	}
	return decideSystemStaging(fetch, snap, m, rejection, stagedHash, t)
}

// withdrawOtherStage withdraws a staged record for a different
// release, slot, or digest than the download, and returns the staged
// hash that remains. It must run before the download starts, because
// the download writes the slot that the old record names, and while
// the record exists, any reboot that init manages boots that slot as
// a trial with files of two releases in it.
func withdrawOtherStage(store machine.ManifestStore, fetch releaseFetch, stagedHash string) (string, error) {
	if stagedHash == "" {
		return "", nil
	}
	if _, hash, err := machine.RenderSystemRelease(fetch.version, fetch.slot, fetch.digest); err == nil && hash == stagedHash {
		return stagedHash, nil
	}
	if err := store.WithdrawStaged(); err != nil {
		return stagedHash, err
	}
	fmt.Printf("withdrew staged system release %.12s because the cluster now targets release %s\n", stagedHash, fetch.version)
	return "", nil
}

// decideSystemStaging turns a verified download into a staged
// SystemRelease record, which waits for its reboot through
// gateDisruption like every other staged document. On that reboot,
// init sets the firmware's BootNext to the staged slot.
func decideSystemStaging(fetch releaseFetch, snap fetchSnapshot, m *machine.Machine, rejection *machine.Rejection, stagedHash string, t turn) convergence {
	if snap.state != fetchVerified {
		return convergence{condition: versionCondition(fetch, snap)}
	}

	record, hash, err := machine.RenderSystemRelease(fetch.version, fetch.slot, fetch.digest)
	if err != nil {
		return convergence{condition: notConverged("VersionConverged", "StagingFailed", "The release record is not staged: "+err.Error())}
	}
	// The rejection records a proving boot that fell back to the proven
	// slot. Staging the same record again would loop through the same
	// failed boot. A new version or a republished digest has a
	// different hash, so it stages.
	if rejection != nil && rejection.Hash == hash {
		return convergence{condition: notConverged("VersionConverged", "RejectedLastBoot",
			fmt.Sprintf("The machine booted release %s on slot %s and fell back: %s. Publish a corrected release under a new version",
				fetch.version, fetch.slot, rejection.Reason))}
	}

	c := convergence{manifest: record, hash: hash, stage: stagedHash != hash}
	gateDisruption(&c, "VersionConverged", m.Spec.RebootPolicyOrDefault(), t, false,
		m.Metadata.Annotations[requestApprovalKey],
		fmt.Sprintf("release %s on slot %s", fetch.version, fetch.slot),
		fmt.Sprintf("Release %s is verified on slot %s and staged (%.12s). rebootPolicy is Manual, so reboot the machine or set rebootPolicy to Auto to prove it", fetch.version, fetch.slot, hash),
		fmt.Sprintf("Release %s is verified on slot %s and staged (%.12s), and waits for a reboot turn from the cluster", fetch.version, fetch.slot, hash),
		fmt.Sprintf("A reboot is requested to prove release %s on slot %s (%.12s)", fetch.version, fetch.slot, hash))
	return c
}

// settleSystemReleaseLifecycle promotes the staged release record
// when this boot is its trial: the machine booted the record's slot,
// and init reports the record's version. A pass that runs shows that
// the kernel, init, k3s, and the machine's cluster membership work on
// that release.
//
// The version comes from the facts, which init writes for the running
// OS, and not from machine.Version, the stamp of this binary. The
// operator runs the image that the cluster's DaemonSet pins, and that
// image stays on the older release until the leaders upgrade. With the
// binary's stamp, no machine in a mixed fleet would promote. A machine
// already on the target would withdraw the record, stage it again,
// and upgrade itself on every boot.
//
// A machine whose slot has no record, because it was installed before
// any catalog, writes its running release as the first proven record,
// because init repairs BootOrder from the proven record on every boot.
func settleSystemReleaseLifecycle(root string, facts *machine.MachineStatus, out *passOutcome) {
	if facts == nil || facts.Storage.MachineState.Backing != machine.BackingPartition ||
		facts.Boot.Slot == "" || facts.Version.Liken == "" {
		return
	}
	store := machine.SystemReleases(root)

	if staged, _ := store.LoadStaged(); staged != nil {
		record, err := machine.ParseSystemRelease(staged)
		if err != nil {
			return // init checks staged records at boot
		}
		if record.Slot != facts.Boot.Slot || record.Version != facts.Version.Liken {
			return // not this boot's trial
		}
		if err := store.Promote(); err != nil {
			fmt.Fprintf(os.Stderr, "promoting the system release: %v\n", err)
			out.fail("promoting the system release", err)
			return
		}
		out.wrote("promoting the system release")
		fmt.Printf("promoted release %s on slot %s to the proven record\n",
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
		out.fail("recording the running release as proven", err)
		return
	}
	out.wrote("recording the running release as proven")
	fmt.Printf("recorded the running release %s on slot %s as proven\n", facts.Version.Liken, facts.Boot.Slot)
}
