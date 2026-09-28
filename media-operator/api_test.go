package main

// These tests cover the phase predicates the pass reads to decide
// whether a Play still needs work.

import "testing"

// Both endings are terminal, and only Finished is done. A Failed Play
// resumes, so the pass still reconciles it.
func TestThePhasePredicates(t *testing.T) {
	cases := []struct {
		phase    string
		terminal bool
		finished bool
	}{
		{phase: phasePending},
		{phase: phaseRunning},
		{phase: phaseFinished, terminal: true, finished: true},
		{phase: phaseFailed, terminal: true},
	}
	for _, each := range cases {
		t.Run(each.phase, func(t *testing.T) {
			mustMatch(t, terminalPhase(each.phase), each.terminal)
			mustMatch(t, finishedPhase(each.phase), each.finished)
		})
	}
}
