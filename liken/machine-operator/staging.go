package main

// converge.go makes each convergence decision in a pure function, so a
// test can check it without files. This file carries out a decision: it
// writes the staged document, the rejection record, and the intent files
// that init reads.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// carryOutConvergence writes the files that a convergence decision
// requires, to the document's store and to init's intent directory
// runDir, and returns the condition to publish. When staging or an
// intent fails to write, it returns a StagingFailed condition of the
// decision's type, so the condition never reports a disruption that
// will not happen. When the staged document cannot be deleted or the
// rejection record cannot be cleared, it returns the decided condition.
// Every failure goes to out, so the loop retries it.
func carryOutConvergence(conv convergence, store machine.ManifestStore, runDir, what string, now time.Time, out *passOutcome) api.Condition {
	step := "carrying out the " + what + "'s convergence"
	failed := func(err error) api.Condition {
		out.fail(step, err)
		return api.Condition{Type: conv.condition.Type, Status: api.ConditionFalse, Reason: "StagingFailed", Message: "The convergence of the " + what + " is not carried out: " + err.Error()}
	}
	if conv.withdraw {
		if err := store.WithdrawStaged(); err != nil {
			fmt.Printf("withdrawing the staged %s: %v\n", what, err)
			out.fail("withdrawing the staged "+what, err)
		} else {
			fmt.Printf("withdrew the staged %s, because this boot already runs the document that the pass would stage\n", what)
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
			KeepBootNext: conv.keepBootNext,
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

// readStagedHash returns the hash of the staged document, or "" when
// nothing is staged. It hashes the bytes even when they do not parse,
// because the deciders compare this hash with the hash of the rendered
// bytes (stagedHash != hash in converge.go) to skip writing a document
// that is staged already.
func readStagedHash(store machine.ManifestStore) string {
	raw, _ := store.LoadStaged()
	if raw == nil {
		return ""
	}
	return machine.ManifestHash(raw)
}
