package main

// The operator's half of the cluster document lifecycle: promotion
// and convergence.
//
// Init boots a staged cluster document tentatively. Init cannot prove
// one, because the document's failure modes are downstream of the
// boot: a bad endpoint just means the machine never joins, which init
// only sees as k3s never settling. The proof is the operator itself.
// It runs as a pod, so if this code is executing, then containerd,
// the kubelet, and the machine's registration with its cluster all
// work under the document this boot ran. At that point the staged
// document is proven, and the operator is the right component to
// record it: it already has the read-write machineState mount it
// stages through.
//
// The same authority records a first boot's seed as the first proven
// copy, which is what closes the loop for a machine that has never
// had a staged document: from then on the durable store, not the
// image, carries the cluster document forward.
//
// Convergence is the Machine's decision table pointed at a different
// document: read the in-cluster Cluster resource, render it
// canonically, compare against what this boot ran, and stage the
// difference for the next boot. The one structural difference is
// scope. The Cluster is one document but the machinery is
// per-machine, so every machine stages its own copy on its own
// schedule, machines can transiently run different cluster documents,
// and each Machine's ClusterConverged condition is where that
// disagreement is visible. This file deliberately contains no fleet
// orchestration. A Cluster edit is drift on every machine at once,
// and this path only stages the change and asks for a reboot. On a
// cluster member with rebootPolicy Auto, asking means awaiting a
// turn from the sweep leader's rollout (rollout.go), which grants
// reboots one machine at a time, so a fleet-wide edit rolls through
// the fleet instead of rebooting every machine together. Manual (the
// default) leaves each machine's pending reboot visible and waiting
// for a person.

import (
	"fmt"
	"net/http"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/chrisguidry/liken/machine"
)

func getCluster(c *apiClient, name string) (*machine.Cluster, error) {
	cluster := &machine.Cluster{}
	if err := c.requestJSON(http.MethodGet, clustersPath+"/"+name, nil, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}

// renderCluster produces the canonical bytes to stage: a Cluster
// document with no status, deterministic for the same reason
// renderManifest is (yaml marshals through JSON with sorted keys), so
// the hash of these bytes is the document's identity everywhere.
//
// The release feed (spec.version and spec.releases) is excluded
// before rendering. Those fields are live-consumed: the operator
// reads them from the in-cluster resource every pass, so they never
// need a reboot to take effect. And the drift comparison here is a
// whole-document hash, so if those fields were included, every
// catalog append and every retargeting would change the hash, read
// as drift on every machine at once, and stage a fleet-wide reboot
// for a change whose entire actuation is a download. (The Machine
// can carry sysctls in its staged manifest because its drift check,
// storageDrift, is field-selective rather than a hash of the whole
// document.)
func renderCluster(name string, spec machine.ClusterSpec) ([]byte, string, error) {
	spec.Version = ""
	spec.Releases = machine.ClusterReleasesSpec{}
	doc := machine.Cluster{
		APIVersion: machine.APIVersion,
		Kind:       "Cluster",
		Metadata:   machine.ObjectMeta{Name: name},
		Spec:       spec,
	}
	body, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, "", err
	}
	return body, machine.ManifestHash(body), nil
}

// decideClusterConvergence mirrors decideConvergence's short-circuit
// order for the cluster document. With no facts, the verdict is
// Unknown. When the boot ran the current document, the machine is
// converged, and this case also withdraws a stale staged copy and
// clears a spent rejection. A document init rejected last boot holds
// rather than re-staging. With nowhere durable to stage, the
// condition says so. Otherwise the drift is staged, and the reboot
// follows the Machine's rebootPolicy (the one knob governing both
// kinds of staging) and its turn with the rollout conductor. A
// cluster document edit is drift on every machine at once, which is
// exactly the case the conductor sequences.
//
// bootHash is the *canonical* hash of the document this boot ran
// (bootClusterHash below), never facts.Boot.ClusterManifestHash. The
// facts hash raw bytes, and a hand-written seed and the operator's
// rendering of the same spec are different bytes with the same
// meaning. Drift is a difference in meaning, and a difference in
// formatting alone must never reboot a fleet.
func decideClusterConvergence(cluster *machine.Cluster, m *machine.Machine, facts *machine.MachineStatus, rejection *machine.Rejection, bootHash, stagedHash string, t turn) convergence {
	if facts == nil || facts.Boot.ManifestSource == "" {
		return convergence{condition: convergenceUnknown("ClusterConverged", "FactsIncomplete",
			"the machine's facts carry no boot record yet")}
	}
	if facts.Boot.ClusterManifestSource != "" && bootHash == "" {
		return convergence{condition: convergenceUnknown("ClusterConverged", "FactsIncomplete",
			"the boot ran a cluster document but its publication is unreadable")}
	}

	manifest, hash, err := renderCluster(cluster.Metadata.Name, cluster.Spec)
	if err != nil {
		return convergence{condition: notConverged("ClusterConverged", "StagingFailed", err.Error())}
	}

	if hash == bootHash {
		return convergence{
			condition:      converged("ClusterConverged", "Converged", "this boot ran the current cluster document"),
			withdraw:       stagedHash != "",
			clearRejection: rejection != nil,
		}
	}

	// The rejection comes from the durable record, not from facts.
	// Facts are a snapshot taken at boot and never change while the
	// machine runs, but a rejection cleared by a revert must unblock
	// a retry within the same boot.
	if r := rejection; r != nil && r.Hash == hash {
		return convergence{condition: notConverged("ClusterConverged", "RejectedLastBoot",
			fmt.Sprintf("init rejected this exact cluster document at boot: %s; edit the cluster to something different", r.Reason))}
	}
	if facts.Storage.MachineState.Backing != machine.BackingPartition {
		return convergence{condition: notConverged("ClusterConverged", "MachineStateEphemeral",
			"machineState is backed by memory; there is no durable filesystem to stage the cluster document into — declare machineState in the machine's manifest")}
	}

	c := convergence{
		manifest: manifest,
		hash:     hash,
		stage:    stagedHash != hash,
	}
	gateReboot(&c, "ClusterConverged", m.Spec.RebootPolicyOrDefault(), t,
		fmt.Sprintf("cluster document staged for the next boot (%.12s); rebootPolicy is Manual, so reboot the machine (or set rebootPolicy: Auto) to apply", hash),
		fmt.Sprintf("cluster document staged for the next boot (%.12s); waiting for the cluster to grant a reboot turn", hash),
		fmt.Sprintf("reboot requested to apply the staged cluster document (%.12s)", hash))
	return c
}

// bootClusterHash canonicalizes the document this boot ran: read the
// bytes init published, parse them, re-render them the way the
// operator renders everything, and hash that. Both sides of the
// drift comparison pass through the same rendering, so any remaining
// difference is a difference in content, not formatting. "" means
// the publication is missing or unreadable.
func bootClusterHash(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	c, err := machine.ParseCluster(raw)
	if err != nil {
		return ""
	}
	_, hash, err := renderCluster(c.Metadata.Name, c.Spec)
	if err != nil {
		return ""
	}
	return hash
}

// settleClusterLifecycle promotes whatever this boot proved. It runs
// every reconcile pass and is idempotent: once promoted (or when a
// newer document is staged for its own proving boot), there is
// nothing left to do. The facts identify exactly which bytes this
// boot ran; the operator promotes those bytes and nothing else.
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
			// A newer document arrived since this boot: it hasn't had
			// its proving boot, and promoting it would skip the trial.
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
			// The seed file changed since this machine booted;
			// recording it would prove bytes nobody ran.
			return
		}
		if err := store.WriteProven(raw); err != nil {
			fmt.Fprintf(os.Stderr, "recording the seed cluster document as proven: %v\n", err)
			return
		}
		fmt.Printf("the seed cluster document is now proven (%.12s)\n", facts.Boot.ClusterManifestHash)
	}
}
