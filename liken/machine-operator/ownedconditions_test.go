package main

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// A Machine whose status holds a condition of a newer release, as a
// rollback leaves it, and the conductor's grant.
func rolledBackPass(t *testing.T) *machine.Machine {
	t.Helper()
	isolatePass(t)
	server := newPassAPI()
	client, _ := passClients(t, server)
	m, err := kubernetes.GetMachine(client, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	status := m.Status
	status.Conditions = []api.Condition{
		{Type: "FutureAttached", Status: api.ConditionFalse, Reason: "AdapterUnplugged"},
		{Type: machine.RebootApprovedCondition, Status: api.ConditionTrue, Reason: "DisruptionBudgetAllows"},
	}
	if _, err := kubernetes.PublishStatus(client, m, &status); err != nil {
		t.Fatal(err)
	}
	_, after := runPasses(t, server, &reader{client: client})
	return after
}

// A rollback leaves conditions of a newer release on the Machine. The
// operator drops each type it does not own, so a stale False of a
// type this release never writes cannot hold the machine Degraded,
// and the conductor's grant stays.
func TestAPassDropsTheConditionsOfANewerRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		after := rolledBackPass(t)

		if c := api.FindCondition(after.Status.Conditions, "FutureAttached"); c != nil {
			t.Errorf("the newer release's condition stayed: %+v", c)
		}
		if c := api.FindCondition(after.Status.Conditions, machine.RebootApprovedCondition); c == nil {
			t.Error("the conductor's grant was dropped")
		}
	})
}

// Every condition type a pass writes is one the operator owns, so no
// condition of this release is dropped at the start of a pass.
func TestEveryConditionAPassWritesIsOwned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		after := rolledBackPass(t)

		for _, c := range after.Status.Conditions {
			if !ownsCondition(c.Type) {
				t.Errorf("the pass wrote %s, which ownedConditionTypes does not name", c.Type)
			}
		}
	})
}
