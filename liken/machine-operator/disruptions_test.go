package main

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// When the pass failed to read the Node, a granted reboot skips the
// drain and goes ahead, because during a demotion or while the API
// server is down there is no Node to cordon.
func TestAGrantedRebootWithNoNodeSkipsTheDrain(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the gate sent %s %s, want nothing", r.Method, r.URL.Path)
	}))
	var d disruptions
	conv := d.gate(&reader{client: client}, nil, errors.New("the API server is down"), turnGranted, drainNow, rebootingConvergence())
	if !conv.requestReboot || d.draining {
		t.Errorf("requestReboot is %v and draining is %v, want the reboot and no drain", conv.requestReboot, d.draining)
	}
}

// When the pass read the Node, a granted reboot goes through the drain
// first, and a drain that cannot list the Node's pods holds the reboot.
func TestAGrantedRebootWithANodeWaitsForTheDrain(t *testing.T) {
	fake := &drainAPI{listFail: true}
	var d disruptions

	conv := d.gate(&reader{client: testClient(t, fake.handler())}, drainNode(true, true, drainNow.Format(time.RFC3339)), nil, turnGranted, drainNow, rebootingConvergence())

	if conv.requestReboot || !d.draining || d.rebooting {
		t.Errorf("requestReboot is %v, draining is %v, and rebooting is %v, want the drain to hold the reboot", conv.requestReboot, d.draining, d.rebooting)
	}
}

// A reboot that an earlier document requested drops the restart of a
// later document, and a restart does not drop a later reboot.
func TestAnEarlierRebootSilencesALaterRestart(t *testing.T) {
	var d disruptions

	first := d.gate(nil, nil, errors.New("no Node"), turnStandalone, drainNow, convergence{requestReboot: true})
	second := d.gate(nil, nil, errors.New("no Node"), turnStandalone, drainNow, convergence{requestRestart: true})

	if !first.requestReboot || second.requestRestart {
		t.Errorf("the first requestReboot is %v and the second requestRestart is %v, want the reboot and no restart", first.requestReboot, second.requestRestart)
	}
}
