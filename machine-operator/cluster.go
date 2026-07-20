package main

// The operator's half of the cluster document lifecycle: promotion
// and convergence.
//
// Init boots a staged cluster document without proof that the
// document works. Init cannot prove it, because problems with the
// document show up only after the boot. For example, a bad endpoint
// only means the machine never joins its cluster, and init sees only
// that k3s never settles. The operator provides the proof. It runs
// as a pod, so if this code executes, then containerd, the kubelet,
// and the machine's registration with its cluster all work under the
// document this boot ran. At that point the staged document is
// proven, and the operator is the right component to record this,
// because it already has the read-write machineState mount it uses
// to stage documents.
//
// The same authority records a first boot's seed as the first proven
// copy. This closes the loop for a machine that has never had a
// staged document: from that point on, the durable store carries the
// cluster document forward, not the image.
//
// Convergence applies the Machine's decision table to a different
// document. The operator reads the in-cluster Cluster resource,
// renders it in its canonical form, compares the result against what
// this boot ran, and stages the difference for the next boot. The
// one structural difference is scope. The Cluster is one document,
// but the convergence machinery runs per machine. Each machine
// stages its own copy on its own schedule, so machines can run
// different cluster documents for a time. Each Machine's
// ClusterConverged condition shows this disagreement when it
// happens. This file contains no fleet orchestration by design. A
// Cluster edit causes drift on every machine at once, and this code
// only stages the change and asks for a reboot. On a cluster member
// with rebootPolicy Auto, asking means waiting for a turn from the
// cluster operator's rollout conductor. The conductor grants
// reboots to one machine at a time, so a fleet-wide edit rolls
// through the fleet instead of rebooting every machine together.
// Manual, the default policy, leaves each machine's pending reboot
// visible and waiting for a person to act.

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// renderCluster produces the canonical bytes to stage: a Cluster
// document with no status. The rendering is deterministic for the
// same reason renderManifest's rendering is deterministic: yaml
// marshals through JSON with sorted keys. So the hash of these bytes
// is the document's identity everywhere.
//
// The rendering excludes the release feed (spec.version and
// spec.releases) before it runs. The operator reads those fields
// live: it rereads them from the in-cluster resource on every pass,
// so they take effect without a reboot. The drift comparison here
// hashes the whole document. If the rendering included those
// fields, every catalog append and every retargeting would change
// the hash. Each change would then read as drift on every machine at
// once and stage a fleet-wide reboot, even though the only work
// needed is a download. (The Machine can carry sysctls in its
// staged manifest, because its drift check, storageDrift, checks
// individual fields rather than hashing the whole document.)
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

// decideClusterConvergence follows the same order of checks as
// decideConvergence, applied to the cluster document. With no facts,
// the verdict is Unknown. When the boot ran the current document,
// the machine is converged. This case also withdraws a stale staged
// copy and clears a spent rejection. When init rejected a document
// at the last boot, this function holds rather than staging it
// again. When there is nowhere durable to stage, the condition
// states this. Otherwise the code stages the drift. The disruption
// then follows the Machine's rebootPolicy, the one setting that
// governs both kinds of staging, and the machine's turn with the
// rollout conductor. A cluster document edit causes drift on every
// machine at once, which is the case the conductor is built to
// sequence.
//
// The classifier in cluster/changes.go decides the kind of
// disruption. When every differing domain is read only when k3s
// starts its process (features, registries), a k3s restart applies
// the document, and the machine and its pods stay up. Any other
// difference requires a reboot. An unreadable boot document also
// requires a reboot, because a reboot is the one action that always
// works.
//
// bootDoc and bootHash describe the document this boot ran (see
// bootClusterDocument below). The hash is always the canonical
// hash, never facts.Boot.ClusterManifestHash. The facts field
// hashes raw bytes, and a hand-written seed and the operator's
// rendering of the same spec produce different bytes with the same
// meaning. Drift is a difference in meaning. A difference in
// formatting alone must never disrupt a fleet.
func decideClusterConvergence(clusterDoc *cluster.Cluster, m *machine.Machine, facts *machine.MachineStatus, rejection *machine.Rejection, bootDoc *cluster.Cluster, bootHash, stagedHash string, t turn) convergence {
	if facts == nil || facts.Boot.ManifestSource == "" {
		return factsIncomplete("ClusterConverged")
	}
	if facts.Boot.ClusterManifestSource != "" && bootHash == "" {
		return convergence{condition: convergenceUnknown("ClusterConverged", "FactsIncomplete",
			"the boot ran a cluster document but its publication is unreadable")}
	}

	manifest, hash, err := renderCluster(clusterDoc.Metadata.Name, clusterDoc.Spec)
	if err != nil {
		return convergence{condition: notConverged("ClusterConverged", "StagingFailed", err.Error())}
	}

	if hash == bootHash {
		return convergedWithCleanup(
			converged("ClusterConverged", "Converged", "this boot ran the current cluster document"),
			stagedHash, rejection)
	}

	// The rejection comes from the durable record, not from facts.
	// Facts are a snapshot taken at boot, and they do not change
	// while the machine runs. But when a revert clears a rejection,
	// a retry must work again within the same boot.
	if rejection != nil && rejection.Hash == hash {
		return convergence{condition: notConverged("ClusterConverged", "RejectedLastBoot",
			fmt.Sprintf("init rejected this exact cluster document at boot: %s; edit the cluster to something different", rejection.Reason))}
	}
	if facts.Storage.MachineState.Backing != machine.BackingPartition {
		return machineStateEphemeral("ClusterConverged", "the cluster document")
	}

	restart := bootDoc != nil && cluster.RestartApplies(bootDoc.Spec, clusterDoc.Spec)

	c := convergence{
		manifest: manifest,
		hash:     hash,
		stage:    stagedHash != hash,
	}
	// On Manual, a person can only apply the change with a reboot:
	// the boot path applies staged documents too, and nobody can
	// restart k3s by hand on a machine with no shell. So this
	// message only mentions the lighter tier as an option. The
	// messages used after a grant name what init will actually do.
	pending := fmt.Sprintf("cluster document staged (%.12s); rebootPolicy is Manual, so reboot the machine to apply (or set rebootPolicy: Auto)", hash)
	if restart {
		pending = fmt.Sprintf("cluster document staged (%.12s); rebootPolicy is Manual, so reboot the machine to apply (or set rebootPolicy: Auto, which would apply it with just a k3s restart)", hash)
	}
	apply := "a reboot"
	if restart {
		apply = "a k3s restart"
	}
	gateDisruption(&c, "ClusterConverged", m.Spec.RebootPolicyOrDefault(), t, restart,
		pending,
		fmt.Sprintf("cluster document staged (%.12s); waiting for the cluster to grant a turn to apply it by %s", hash, apply),
		fmt.Sprintf("%s requested to apply the staged cluster document (%.12s)", apply, hash))
	return c
}

// convergeClusterDocument runs the cluster document's part of one
// reconcile pass. It reads the live Cluster resource, loads this
// machine's durable rejection and staged copy from the store, and
// makes the convergence decision. The decision is where the fleet's
// temporary disagreement about the Cluster becomes visible: each
// machine stages its own copy and reboots on its own policy, and
// this condition reports where this one machine stands. The
// function returns the live Cluster alongside the decision, because
// version convergence (release.go) reads its release feed live. A
// nil Cluster means the read failed, and the verdict already
// reports that.
func convergeClusterDocument(c *kubernetes.Client, store machine.ManifestStore, clusterName string, m *machine.Machine, facts *machine.MachineStatus, t turn) (convergence, *cluster.Cluster) {
	liveCluster, err := kubernetes.GetCluster(c, clusterName)
	if err != nil {
		return convergence{condition: convergenceUnknown("ClusterConverged", "ClusterUnavailable",
			fmt.Sprintf("reading cluster %s: %v", clusterName, err))}, nil
	}
	rejection, _ := store.LoadRejection()
	bootDoc, bootHash := bootClusterDocument(cluster.BootClusterManifestPath)
	return decideClusterConvergence(liveCluster, m, facts, rejection,
		bootDoc, bootHash, readStagedHash(store), t), liveCluster
}

// bootClusterDocument describes the document this boot ran. It
// returns the parsed document, so the classifier can compare it
// against the desired spec, and the document's canonical hash.
// Canonical means the function parses the bytes init published and
// re-renders them the way the operator renders every document. Both
// sides of the drift comparison then pass through the same
// rendering, so any remaining difference is a difference in
// content, not formatting. A nil document and an empty hash mean the
// published document is missing or unreadable.
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

// settleClusterLifecycle promotes whatever this boot proved. It runs
// on every reconcile pass and is idempotent. Once the operator
// promotes a document, or once a newer document is staged for its
// own proving boot, there is nothing left to do. The facts identify
// exactly which bytes this boot ran, and the operator promotes only
// those bytes.
func settleClusterLifecycle(root, seedPath string, facts *machine.MachineStatus) {
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
			// A newer document arrived after this boot started. It
			// has not had its own proving boot yet, and promoting it
			// now would skip that trial.
			return
		}
		if err := store.Promote(); err != nil {
			fmt.Fprintf(os.Stderr, "promoting the cluster document: %v\n", err)
			return
		}
		fmt.Printf("the cluster document proved out; %.12s is now proven\n", facts.Boot.ClusterManifestHash)

	case machine.ManifestSourceSeed:
		if proven, err := store.LoadProven(); proven != nil || err != nil {
			return
		}
		raw, err := os.ReadFile(seedPath)
		if err != nil {
			return
		}
		if machine.ManifestHash(raw) != facts.Boot.ClusterManifestHash {
			// The seed file changed after this machine booted.
			// Recording it now would mark as proven bytes that
			// nobody ran.
			return
		}
		if err := store.WriteProven(raw); err != nil {
			fmt.Fprintf(os.Stderr, "recording the seed cluster document as proven: %v\n", err)
			return
		}
		fmt.Printf("the seed cluster document is now proven (%.12s)\n", facts.Boot.ClusterManifestHash)
	}
}
