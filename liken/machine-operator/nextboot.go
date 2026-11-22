package main

// This file reports which copy of the cluster document the next boot
// would run, as status.nextBoot and the ClusterProven condition.
//
// status.boot records the document that this machine runs, and it
// cannot show what the next boot runs, because the operator stages,
// promotes, and withdraws copies in the store during the boot. A
// machine whose proven copy is older than the running document runs
// that older copy at its next boot, and for the seconds until the
// operator restarts k3s under the current document, k3s removes the
// manifests of the features that the older copy turns off. The pass
// repairs that state in settleClusterLifecycle (cluster.go), and this
// condition shows it while the repair cannot write.

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

const clusterProvenCondition = "ClusterProven"

// nextClusterBoot returns the copy of the cluster document that the
// next boot would choose, and its bytes. It applies the rule of
// chooseCluster in init/cluster.go without its writes: a staged copy
// that parses and whose hash is not in the attempted marker, then a
// proven copy that parses, then the seed at seedPath. Init rejects a
// staged copy whose hash is in the marker, because the boot or restart
// that tried it was never proven. A memory-backed machineState
// (durable false) has no store, and init reads only the seed. It
// returns no source and nil bytes when the seed does not exist either.
func nextClusterBoot(store machine.ManifestStore, durable bool, seedPath string) (machine.ManifestSource, []byte, error) {
	if durable {
		staged, err := store.LoadStaged()
		if err != nil {
			return "", nil, err
		}
		if staged != nil {
			attempted, err := store.LoadAttempted()
			if err != nil {
				return "", nil, err
			}
			if _, perr := cluster.ParseCluster(staged); perr == nil && attempted != machine.ManifestHash(staged) {
				return machine.ManifestSourceStaged, staged, nil
			}
		}
		proven, err := store.LoadProven()
		if err != nil {
			return "", nil, err
		}
		if proven != nil {
			if _, perr := cluster.ParseCluster(proven); perr == nil {
				return machine.ManifestSourceProven, proven, nil
			}
		}
	}
	seed, err := os.ReadFile(seedPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	return machine.ManifestSourceSeed, seed, nil
}

// nextBootOfCluster returns status.nextBoot and the ClusterProven
// condition. The pass calls it after it carried out the cluster
// document's convergence, so both describe the store as the pass left
// it. running is the pass's one read of the running document.
//
// ChangeStaged is False because the next boot runs a document that
// this machine does not run yet. ClusterConverged is False for the same
// staged copy and reports how it is applied, so ChangeStaged gives the
// same phase that ClusterConverged gives (phase.go).
func nextBootOfCluster(store machine.ManifestStore, seedPath string, facts *machine.MachineStatus, running runningCluster) (machine.NextBootStatus, api.Condition) {
	condition := func(status api.ConditionStatus, reason, message string) api.Condition {
		return api.Condition{Type: clusterProvenCondition, Status: status, Reason: reason, Message: message}
	}
	if facts == nil || facts.Boot.ManifestSource == "" {
		return machine.NextBootStatus{}, condition(api.ConditionUnknown, "FactsIncomplete",
			"The machine's facts have no boot record yet")
	}
	if running.raw == nil {
		return machine.NextBootStatus{}, condition(api.ConditionUnknown, "FactsIncomplete",
			"init has not published the cluster document that this machine runs")
	}

	durable := facts.Storage.MachineState.Backing == machine.BackingPartition
	source, raw, err := nextClusterBoot(store, durable, seedPath)
	if err != nil {
		return machine.NextBootStatus{}, condition(api.ConditionUnknown, "StoreUnreadable",
			"The copies of the cluster document cannot be read: "+err.Error())
	}
	next := machine.NextBootStatus{ClusterManifestSource: source}
	if raw != nil {
		next.ClusterManifestHash = machine.ManifestHash(raw)
	}
	runningHash := machine.ManifestHash(running.raw)

	switch {
	case bytes.Equal(raw, running.raw):
		return next, condition(api.ConditionTrue, "Current",
			fmt.Sprintf("The next boot runs the %s copy of the cluster document that this machine runs (%.12s)", copyName(source), runningHash))
	case source == machine.ManifestSourceStaged:
		return next, condition(api.ConditionFalse, "ChangeStaged",
			fmt.Sprintf("The next boot runs the staged cluster document %.12s, and this machine runs %.12s. ClusterConverged reports how the staged document is applied", next.ClusterManifestHash, runningHash))
	case raw == nil:
		return next, condition(api.ConditionFalse, "NextBootDiffers",
			fmt.Sprintf("The next boot finds no cluster document, and this machine runs %.12s", runningHash))
	default:
		return next, condition(api.ConditionFalse, "NextBootDiffers",
			fmt.Sprintf("The next boot would run the %s cluster document %.12s, and this machine runs %.12s. The operator writes the running document as the proven copy, and it reports each failure of that write in its log", copyName(source), next.ClusterManifestHash, runningHash))
	}
}

// copyName names a copy of a document for a message.
func copyName(source machine.ManifestSource) string {
	switch source {
	case machine.ManifestSourceStaged:
		return "staged"
	case machine.ManifestSourceSeed:
		return "image's"
	default:
		return "proven"
	}
}
