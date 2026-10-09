package main

// Tests for the disruption gate, which every convergence decision
// passes through on its way to its side effects.

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// A pass whose Node read failed skips the drain and lets a granted
// reboot go ahead: during a demotion, or while the API server is down
// and the Node's store cannot answer, there is no Node to cordon.
func TestAGrantedRebootWithNoNodeSkipsTheDrain(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the gate sent %s %s, want nothing", r.Method, r.URL.Path)
	}))
	var d disruptions
	conv := d.gate(client, nil, errors.New("the API server is down"), turnGranted, drainNow, rebootingConvergence())
	if !conv.requestReboot || d.draining {
		t.Errorf("reboot = %v, draining = %v; want the reboot and no drain", conv.requestReboot, d.draining)
	}
}

// A granted reboot on a pass that read the Node goes through the drain
// first, and a drain that cannot list the Node's pods holds the reboot,
// because a slow API server must not let a reboot kill pods past their
// disruption budgets.
func TestAGrantedRebootWithANodeWaitsForTheDrain(t *testing.T) {
	fake := &drainAPI{listFail: true}
	var d disruptions

	conv := d.gate(testClient(t, fake.handler()), drainNode(true, true, drainNow.Format(time.RFC3339)), nil, turnGranted, drainNow, rebootingConvergence())

	if conv.requestReboot || !d.draining || d.rebooting {
		t.Errorf("reboot = %v, draining = %v, rebooting = %v; want the reboot held by the drain", conv.requestReboot, d.draining, d.rebooting)
	}
}

// A reboot that an earlier document asked for covers a later
// document's restart, because the boot applies everything the restart
// would. A restart never silences a later reboot.
func TestAnEarlierRebootSilencesALaterRestart(t *testing.T) {
	var d disruptions

	first := d.gate(nil, nil, errors.New("no Node"), turnStandalone, drainNow, convergence{requestReboot: true})
	second := d.gate(nil, nil, errors.New("no Node"), turnStandalone, drainNow, convergence{requestRestart: true})

	if !first.requestReboot || second.requestRestart {
		t.Errorf("first reboot = %v, second restart = %v; want the reboot and no restart", first.requestReboot, second.requestRestart)
	}
}
