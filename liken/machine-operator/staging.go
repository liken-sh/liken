package main

// Staging: the side effects of one convergence decision. The decisions
// themselves are pure (converge.go), so a test can judge them without
// a disk. This file is where a decision touches the machine: the
// staged document, the rejection record, and the intent files that
// init acts on.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// carryOutConvergence performs one convergence decision's side
// effects against one document's store and init's intent directory,
// runDir, and returns the condition to publish. An I/O failure
// downgrades the condition to StagingFailed on the same condition
// type, so the report stays attached to the right document.
func carryOutConvergence(conv convergence, store machine.ManifestStore, runDir, what string, now time.Time, out *passOutcome) api.Condition {
	step := "carrying out the " + what + "'s convergence"
	failed := func(err error) api.Condition {
		out.fail(step, err)
		return api.Condition{Type: conv.condition.Type, Status: api.ConditionFalse, Reason: "StagingFailed", Message: err.Error()}
	}
	if conv.withdraw {
		if err := store.WithdrawStaged(); err != nil {
			fmt.Printf("withdrawing the staged %s: %v\n", what, err)
			out.fail("withdrawing the staged "+what, err)
		} else {
			fmt.Printf("withdrew the staged %s; the cluster's copy matches this boot again\n", what)
			out.wrote("withdrawing the staged " + what)
		}
	}
	if conv.clearRejection {
		if err := store.ClearRejection(); err != nil {
			fmt.Printf("clearing the %s rejection record: %v\n", what, err)
			out.fail("clearing the "+what+" rejection record", err)
		} else {
			out.wrote("clearing the " + what + " rejection record")
		}
	}
	if conv.stage {
		if err := store.WriteStaged(conv.manifest); err != nil {
			return failed(err)
		}
		out.wrote(step)
		fmt.Printf("staged %s %.12s for the next boot\n", what, conv.hash)
	}
	if conv.requestReboot {
		intent := &machine.RebootIntent{
			Reason:       "applying the staged " + what,
			ManifestHash: conv.hash,
			RequestedAt:  now,
		}
		if err := machine.WriteRebootIntent(runDir, intent); err != nil {
			return failed(err)
		}
		out.wrote(step)
		fmt.Printf("requested a reboot to apply %s %.12s\n", what, conv.hash)
	}
	if conv.requestRestart {
		intent := &machine.RestartIntent{
			Reason:      "applying the staged " + what,
			RequestedAt: now,
		}
		if err := machine.WriteRestartIntent(runDir, intent); err != nil {
			return failed(err)
		}
		out.wrote(step)
		fmt.Printf("requested a k3s restart to apply %s %.12s\n", what, conv.hash)
	}
	if conv.requestLoad {
		intent := &machine.ModulesIntent{
			Reason:       "loading the staged " + what + "'s added modules",
			ManifestHash: conv.hash,
			RequestedAt:  now,
		}
		if err := machine.WriteModulesIntent(runDir, intent); err != nil {
			return failed(err)
		}
		out.wrote(step)
		fmt.Printf("requested a live module load to apply %s %.12s\n", what, conv.hash)
	}
	return conv.condition
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
