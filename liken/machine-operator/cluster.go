package main

// This file promotes and converges the cluster document, the Cluster
// spec that init reads at boot.
//
// Init cannot tell at boot whether a cluster document works. A wrong
// endpoint, for example, shows only as a machine that never joins its
// cluster. When this operator's pod runs, containerd, the kubelet, and
// the node's registration all worked under the document this boot
// ran, so the operator promotes a staged document to proven. The
// operator does the promotion because it already mounts machineState
// read-write to stage documents.
//
// A first boot runs the seed, the copy in the image at
// cluster.ClusterManifestPath. The operator records the seed as the
// first proven copy, so later boots read the cluster document from
// machineState and not from the image.
//
// Convergence takes the same steps as decideConvergence for the
// Machine. The operator reads the Cluster resource, renders it in
// canonical form, compares it with the document this boot ran, and
// stages it for the next boot when they differ. Each machine converges
// on its own schedule, so machines can run different cluster documents
// for a while, and each Machine's `ClusterConverged` condition shows
// whether it runs the current one.
//
// This file does not order the rollout across the fleet. After a
// Cluster edit, every machine stages the change and requests the
// disruption it needs, if any. With rebootPolicy Auto, a cluster
// member then waits for cluster-operator to give it an entry in the
// Cluster's status.rebootTurns, and cluster-operator gives turns within
// the Cluster's disruption budget. With rebootPolicy Manual, the
// default, the condition reports the pending disruption until a person
// acts on it.

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// renderCluster returns the canonical bytes of a Cluster document with
// no status, and their hash. yaml.Marshal goes through JSON, which
// sorts the keys, so the same spec always gives the same hash.
//
// The rendering clears spec.version and spec.releases, because the
// operator reads them from the Cluster resource on every pass and
// applies them without a reboot. The drift check compares hashes of the
// whole document, so with those fields in the hash, every new catalog
// entry and every change of target version would stage a reboot on
// every machine.
func renderCluster(name string, spec cluster.ClusterSpec) ([]byte, string, error) {
	spec.Version = ""
	spec.Releases = cluster.ClusterReleasesSpec{}
	doc := cluster.Cluster{
		APIVersion: api.APIVersion,
		Kind:       "Cluster",
		Metadata:   api.ObjectMeta{Name: name},
		Spec:       spec,
	}
	body, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, "", err
	}
	return body, machine.ManifestHash(body), nil
}

// decideClusterConvergence decides what to stage and which disruption
// to request for the cluster document. It checks the cases in the same
// order as decideConvergence:
//
//   - With no facts, the condition is Unknown.
//   - When this boot ran the document, the condition is True. The result
//     also withdraws any staged copy and clears any rejection.
//   - When init rejected this document at the last boot, it is not
//     staged again.
//   - When machineState is in memory, nothing can be staged.
//   - Otherwise the document is staged, and gateDisruption sets the
//     condition and the disruption from the Machine's rebootPolicy and
//     the machine's turn.
//
// The functions in cluster/changes.go choose the disruption. When only
// spec.origin or spec.endpoint differs, the next boot applies the change
// and the machine requests nothing. When every difference is in fields
// that k3s reads only at process start, a k3s restart applies it. Any
// other difference needs a reboot, and so does a change when the boot
// document could not be read, because a reboot applies every kind of
// change.
//
// bootDoc and bootHash are the document this boot ran and its canonical
// hash, from bootClusterDocument. facts.Boot.ClusterManifestHash hashes
// the raw bytes instead, and a hand-written seed and the operator's
// rendering of the same spec have different bytes. A formatting
// difference must not reboot the fleet.
//
// reduced is the Cluster's document after reduceRetraction
// (retraction.go). It adds back each removed feature while the cluster
// still has objects that only that feature's controller can delete, and
// held lists those features. The decision runs on reduced, so each of
// those features keeps running through any disruption this pass
// requests.
func decideClusterConvergence(reduced *cluster.Cluster, held []featureHold, m *machine.Machine, facts *machine.MachineStatus, rejection *machine.Rejection, bootDoc *cluster.Cluster, bootHash, stagedHash string, t turn) convergence {
	if facts == nil || facts.Boot.ManifestSource == "" {
		return factsIncomplete("ClusterConverged")
	}
	if facts.Boot.ClusterManifestSource != "" && bootHash == "" {
		return convergence{condition: convergenceUnknown("ClusterConverged", "FactsIncomplete",
			"The boot ran a cluster document, but the file that init published for it cannot be read")}
	}

	manifest, hash, err := renderCluster(reduced.Metadata.Name, reduced.Spec)
	if err != nil {
		return convergence{condition: notConverged("ClusterConverged", "StagingFailed", "The cluster document is not staged: "+err.Error())}
	}

	if hash == bootHash {
		// While held lists a feature, no disruption can make progress
		// until the objects that block its removal are gone. A staged
		// copy is still withdrawn, so the next boot does not run it.
		if len(held) > 0 {
			return convergence{
				condition: notConverged("ClusterConverged", "RetractionBlocked", holdMessage(held)),
				withdraw:  stagedHash != "",
			}
		}
		return convergedWithCleanup(
			converged("ClusterConverged", "Converged", "This boot ran the current cluster document"),
			stagedHash, rejection)
	}

	// rejection is read from machineState and not from facts, which do
	// not change until the next boot. So after a revert clears the
	// record, the same document can be staged again during this boot.
	if rejection != nil && rejection.Hash == hash {
		return convergence{condition: notConverged("ClusterConverged", "RejectedLastBoot",
			fmt.Sprintf("init rejected this cluster document at boot: %s. The operator does not stage a rejected document again, so change the Cluster", rejection.Reason))}
	}
	if facts.Storage.MachineState.Backing != machine.BackingPartition {
		return machineStateEphemeral("ClusterConverged", "the cluster document")
	}

	// The next-boot case comes before gateDisruption because it needs no
	// turn, so a fleet-wide edit of spec.origin or spec.endpoint requests
	// nothing from cluster-operator. Until its next boot, a running
	// machine misses only this part of such an edit: a follower keeps
	// the old endpoint's host in its time sources (NextBootApplies in
	// cluster/changes.go).
	if bootDoc != nil && cluster.NextBootApplies(bootDoc.Spec, reduced.Spec) {
		return convergence{
			manifest: manifest,
			hash:     hash,
			stage:    stagedHash != hash,
			condition: notConverged("ClusterConverged", "StagedForNextBoot",
				fmt.Sprintf("The cluster document (%.12s) is staged, and this machine applies it at its next boot without a reboot turn", hash)),
		}
	}

	restart := bootDoc != nil && cluster.RestartApplies(bootDoc.Spec, reduced.Spec)

	c := convergence{
		manifest: manifest,
		hash:     hash,
		stage:    stagedHash != hash,
	}
	// On Manual, the messages tell a person to reboot, because a machine
	// has no shell from which to restart k3s by hand. Approving the
	// operator's Disruption request also applies the document, by the
	// restart or the reboot that gateDisruption chooses.
	pending := fmt.Sprintf("The cluster document (%.12s) is staged. rebootPolicy is Manual, so reboot the machine or set rebootPolicy to Auto to apply it", hash)
	if restart {
		pending = fmt.Sprintf("The cluster document (%.12s) is staged. rebootPolicy is Manual, so reboot the machine to apply it, or set rebootPolicy to Auto, which applies it with a k3s restart", hash)
	}
	apply := "a reboot"
	if restart {
		apply = "a k3s restart"
	}
	gateDisruption(&c, "ClusterConverged", m.Spec.RebootPolicyOrDefault(), t, restart,
		m.Metadata.Annotations[requestApprovalKey],
		"the cluster document",
		pending,
		fmt.Sprintf("The cluster document (%.12s) is staged and waits for a turn from the cluster to apply it with %s", hash, apply),
		fmt.Sprintf("The operator requests %s to apply the staged cluster document (%.12s)", apply, hash))
	return c
}

// convergeClusterDocument runs the cluster document's part of a
// reconcile pass. It also returns the Cluster, because version
// convergence (release.go) reads spec.version and spec.releases from
// it. A nil Cluster means the read failed, and the condition reports
// the error.
//
// reduceRetraction reads the live cluster only when the edit removes a
// feature, to check whether objects such as HelmCharts still exist.
// waits.go watches the HelmCharts and the LoadBalancer Services only
// while a removal waits on them.
func convergeClusterDocument(r *reader, store machine.ManifestStore, clusterName string, m *machine.Machine, facts *machine.MachineStatus, t turn) (convergence, *cluster.Cluster) {
	liveCluster, err := r.cluster(clusterName)
	if err != nil {
		return convergence{condition: convergenceUnknown("ClusterConverged", "ClusterUnavailable",
			fmt.Sprintf("The Cluster %s cannot be read: %v", clusterName, err))}, nil
	}
	rejection, _ := store.LoadRejection()
	bootDoc, bootHash := bootClusterDocument(cluster.BootClusterManifestPath)
	reduced, held := reduceRetraction(bootDoc, liveCluster, func(p cluster.Precondition) (bool, string, error) {
		return evaluatePrecondition(r, p)
	})
	return decideClusterConvergence(reduced, held, m, facts, rejection,
		bootDoc, bootHash, readStagedHash(store), t), liveCluster
}

// bootClusterDocument reads the cluster document that init published
// for this boot, and returns it parsed, with its canonical hash. It
// renders the document the same way as the Cluster resource, so the
// drift check compares content and not formatting. It returns nil and
// "" when the file is missing or cannot be parsed.
func bootClusterDocument(path string) (*cluster.Cluster, string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	c, err := cluster.ParseCluster(raw)
	if err != nil {
		return nil, ""
	}
	_, hash, err := renderCluster(c.Metadata.Name, c.Spec)
	if err != nil {
		return nil, ""
	}
	return c, hash
}

// settleClusterLifecycle records the cluster document that this boot
// ran as proven. It runs on every pass and does nothing once the
// document is proven. It promotes only bytes whose hash matches the one
// in facts.
func settleClusterLifecycle(root, seedPath string, facts *machine.MachineStatus, out *passOutcome) {
	if facts == nil || facts.Storage.MachineState.Backing != machine.BackingPartition {
		return // nothing durable to settle
	}
	store := machine.ClusterManifests(root)

	switch facts.Boot.ClusterManifestSource {
	case machine.ManifestSourceStaged:
		raw, err := store.LoadStaged()
		if err != nil || raw == nil {
			return // already promoted, or nothing staged
		}
		if machine.ManifestHash(raw) != facts.Boot.ClusterManifestHash {
			// A document staged after this boot started must prove
			// itself at its own boot.
			return
		}
		if err := store.Promote(); err != nil {
			fmt.Fprintf(os.Stderr, "promoting the cluster document: %v\n", err)
			out.fail("promoting the cluster document", err)
			return
		}
		out.wrote("promoting the cluster document")
		fmt.Printf("the staged cluster document is now proven (%.12s)\n", facts.Boot.ClusterManifestHash)

	case machine.ManifestSourceSeed:
		if proven, err := store.LoadProven(); proven != nil || err != nil {
			return
		}
		raw, err := os.ReadFile(seedPath)
		if err != nil {
			return
		}
		if machine.ManifestHash(raw) != facts.Boot.ClusterManifestHash {
			// The seed differs from the bytes this boot ran, and only
			// bytes that a boot ran are recorded as proven.
			return
		}
		if err := store.WriteProven(raw); err != nil {
			fmt.Fprintf(os.Stderr, "recording the seed cluster document as proven: %v\n", err)
			out.fail("recording the seed cluster document as proven", err)
			return
		}
		out.wrote("recording the seed cluster document as proven")
		fmt.Printf("the seed cluster document is now proven (%.12s)\n", facts.Boot.ClusterManifestHash)
	}
}
