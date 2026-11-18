package main

import "testing"

func TestOnlyFinishedIsFinishedWhileFinishedAndFailedAreTerminal(t *testing.T) {
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
