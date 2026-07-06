package main

// The operator's half of the cluster document lifecycle: promotion
// and convergence.
//
// Init boots a staged cluster document tentatively — it cannot prove
// one, because the document's failure modes are downstream of the
// boot (a bad endpoint just means the machine never joins, which init
// only sees as k3s never settling). The proof is the operator itself:
// it runs as a pod, so if this code is executing, then containerd,
// the kubelet, and the machine's registration with its cluster all
// work under the document this boot ran. That is the moment the
// staged document has earned proven, and the operator holds the pen
// (it already has the read-write machineState mount it stages
// through).
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
// scope: the Cluster is one document but the machinery is
// per-machine, so every machine stages its own copy on its own
// schedule, machines can transiently run different cluster documents,
// and each Machine's ClusterConverged condition is where that
// disagreement is visible. Deliberately absent: any fleet
// orchestration. A Cluster edit is drift on every machine at once,
// and with rebootPolicy Auto everywhere that is a simultaneous fleet
// reboot — Manual (the default) leaves reboots visible and pending
// per machine, and rolling coordination is a later milestone's job.

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/chrisguidry/liken/machine"
)

// settleClusterLifecycle promotes whatever this boot proved. It runs
// every reconcile pass and is idempotent: once promoted (or when a
// newer document is staged for its own proving boot), there is
// nothing left to do. The facts identify exactly which bytes this
// boot ran; the operator promotes those bytes and nothing else.
// renderCluster produces the canonical bytes to stage: a complete
// Cluster document with no status, deterministic for the same reason
// renderManifest is (yaml marshals through JSON with sorted keys), so
// the hash of these bytes is the document's identity everywhere.
func renderCluster(name string, spec machine.ClusterSpec) ([]byte, string, error) {
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

func clusterConverged(reason, message string) machine.Condition {
	return machine.Condition{Type: "ClusterConverged", Status: "True", Reason: reason, Message: message}
}

func clusterNotConverged(reason, message string) machine.Condition {
	return machine.Condition{Type: "ClusterConverged", Status: "False", Reason: reason, Message: message}
}

// decideClusterConvergence mirrors decideConvergence's short-circuit
// order for the cluster document: no facts → Unknown; current →
// converged (withdrawing a stale staged copy and clearing a spent
// rejection); rejected-last-boot → hold; nowhere durable to stage →
// say so; drift → stage and reboot per the Machine's rebootPolicy,
// the one knob governing both kinds of staging.
//
// bootHash is the *canonical* hash of the document this boot ran
// (bootClusterHash below), never facts.Boot.ClusterManifestHash: the
// facts hash raw bytes, and a hand-written seed and the operator's
// rendering of the same spec are different bytes saying the same
// thing. Drift is a difference in meaning; rebooting a fleet over
// formatting would be absurd.
func decideClusterConvergence(cluster *machine.Cluster, m *machine.Machine, facts *machine.MachineStatus, rejection *machine.Rejection, bootHash, stagedHash string) convergence {
	if facts == nil || facts.Boot.ManifestSource == "" {
		return convergence{condition: machine.Condition{
			Type: "ClusterConverged", Status: "Unknown", Reason: "FactsIncomplete",
			Message: "the machine's facts carry no boot record yet",
		}}
	}
	if facts.Boot.ClusterManifestSource != "" && bootHash == "" {
		return convergence{condition: machine.Condition{
			Type: "ClusterConverged", Status: "Unknown", Reason: "FactsIncomplete",
			Message: "the boot ran a cluster document but its publication is unreadable",
		}}
	}

	manifest, hash, err := renderCluster(cluster.Metadata.Name, cluster.Spec)
	if err != nil {
		return convergence{condition: clusterNotConverged("StagingFailed", err.Error())}
	}

	if hash == bootHash {
		return convergence{
			condition:      clusterConverged("BootCurrent", "this boot ran the current cluster document"),
			withdraw:       stagedHash != "",
			clearRejection: rejection != nil,
		}
	}

	// The rejection comes from the durable record, not from facts:
	// facts are the boot's frozen memory, and a rejection cleared by
	// a revert must unblock a retry within the same boot.
	if r := rejection; r != nil && r.Hash == hash {
		return convergence{condition: clusterNotConverged("RejectedLastBoot",
			fmt.Sprintf("init rejected this exact cluster document at boot: %s; edit the cluster to something different", r.Reason))}
	}
	if facts.Storage.MachineState.Backing != machine.BackingPartition {
		return convergence{condition: clusterNotConverged("MachineStateEphemeral",
			"machineState is backed by memory; there is no durable filesystem to stage the cluster document into — declare machineState in the machine's manifest")}
	}

	c := convergence{
		manifest: manifest,
		hash:     hash,
		stage:    stagedHash != hash,
	}
	if m.Spec.RebootPolicyOrDefault() == machine.RebootAuto {
		c.requestReboot = true
		c.condition = clusterNotConverged("RebootRequested",
			fmt.Sprintf("reboot requested to apply the staged cluster document (%.12s)", hash))
	} else {
		c.condition = clusterNotConverged("RebootPending",
			fmt.Sprintf("cluster document staged for the next boot (%.12s); rebootPolicy is Manual, so reboot the machine (or set rebootPolicy: Auto) to apply", hash))
	}
	return c
}

// readStagedClusterHash is readStagedHash for the cluster document's
// store: the identity of whatever waits there, "" when nothing does.
func readStagedClusterHash() string {
	raw, _ := machine.ClusterManifests(machine.MachineStateDir).LoadStaged()
	if raw == nil {
		return ""
	}
	return machine.ManifestHash(raw)
}

// bootClusterHash canonicalizes the document this boot ran: read the
// bytes init published, parse them, re-render them the way the
// operator renders everything, and hash that. Both sides of the
// drift comparison pass through the same rendering, so only meaning
// can differ. "" means the publication is missing or unreadable.
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

func settleClusterLifecycle(root, seedPath string, facts *machine.MachineStatus) {
	if facts == nil || facts.Storage.MachineState.Backing != machine.BackingPartition {
		return // nothing durable to settle
	}
	store := machine.ClusterManifests(root)

	switch facts.Boot.ClusterManifestSource {
	case machine.ManifestSourceStaged:
		raw, err := store.LoadStaged()
		if err != nil || raw == nil {
			return // already promoted, or nothing to see
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
