package main

// Tests for spec.hold, which stops a turn between the drain and the
// reboot.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

func firmwareRequest(hash string, hold bool) disruption.Disruption {
	return *disruption.New("node-4.firmware-dbx", requestingNode4(), disruption.Spec{
		Machine: "node-4", Requester: "firmware.liken.sh/firmware-operator", Kind: disruption.KindReboot,
		Hash: hash, Hold: hold})
}

func heldOn(d disruption.Disruption) disruption.Disruption {
	d.Status.Conditions = []api.Condition{{Type: disruption.HeldCondition, Status: api.ConditionTrue, Reason: "Drained"}}
	return d
}

func TestHoldingRequestsAreApprovedAndNotDone(t *testing.T) {
	boot := machine.BootID(machine.BootStatus{Time: &requestBootTime})
	approved := firmwareRequest(boot, true)
	approved.Spec.ApprovedHash = boot
	cases := []struct {
		name    string
		policy  machine.RebootPolicy
		request disruption.Disruption
		holds   bool
	}{
		{"a hold on an Auto machine", machine.RebootAuto, firmwareRequest(boot, true), true},
		{"no hold", machine.RebootAuto, firmwareRequest(boot, false), false},
		{"a hold for an earlier boot", machine.RebootAuto, firmwareRequest("51f0e2d8aa90aa", true), false},
		{"a hold not yet approved", machine.RebootManual, firmwareRequest(boot, true), false},
		{"a hold approved on a Manual machine", machine.RebootManual, approved, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			holding := holdingRequests([]disruption.Disruption{c.request}, c.policy, boot)

			if (len(holding) == 1) != c.holds {
				t.Errorf("holding requests = %v, want a hold = %v", holding, c.holds)
			}
		})
	}
}

// With no pods left to drain, the gate cancels the reboot, keeps the
// cordon, and records the drain as done, so settleHolds sets Held.
func TestTheGateHoldsTheRebootAfterTheDrain(t *testing.T) {
	fake := &drainAPI{}
	client := testClient(t, fake.handler())
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	d := &disruptions{holding: []disruption.Disruption{firmwareRequest("b00t", true)}}

	conv := d.gate(&reader{client: client}, drainNode(true, true, since), nil, turnGranted, drainNow, rebootingConvergence())

	if conv.requestReboot || conv.condition.Reason != "Held" || !strings.Contains(conv.condition.Message, "node-4.firmware-dbx") {
		t.Errorf("conv = %+v, want the reboot held for node-4.firmware-dbx", conv)
	}
	if !d.drained || !d.draining {
		t.Errorf("drained = %v, draining = %v, want both true: the drain is done and the cordon stays", d.drained, d.draining)
	}
}

func TestTheHoldWaitsForTheDrain(t *testing.T) {
	fake := &drainAPI{pods: []kubernetes.Pod{pod("web", "default")}}
	client := testClient(t, fake.handler())
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	d := &disruptions{holding: []disruption.Disruption{firmwareRequest("b00t", true)}}

	conv := d.gate(&reader{client: client}, drainNode(true, true, since), nil, turnGranted, drainNow, rebootingConvergence())

	if conv.condition.Reason != "Draining" || d.drained || len(fake.evictions) != 1 {
		t.Errorf("reason = %s, drained = %v, evictions = %v, want Draining, not drained, and 1 eviction", conv.condition.Reason, d.drained, fake.evictions)
	}
}

// A drain after Held would evict the requester's pod.
func TestAHeldTurnDoesNotDrainAgain(t *testing.T) {
	fake := &drainAPI{pods: []kubernetes.Pod{pod("firmware-apply", "liken-system")}}
	client := testClient(t, fake.handler())
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	d := &disruptions{holding: []disruption.Disruption{heldOn(firmwareRequest("b00t", true))}}

	conv := d.gate(&reader{client: client}, drainNode(true, true, since), nil, turnGranted, drainNow, rebootingConvergence())

	if len(fake.evictions) != 0 || conv.requestReboot || conv.condition.Reason != "Held" {
		t.Errorf("evictions = %v, conv = %+v, want no eviction and reason Held", fake.evictions, conv)
	}
}

// The turn ends in a reboot, which makes a separate restart redundant.
func TestNoRestartRunsWhileARequestIsHeld(t *testing.T) {
	d := &disruptions{holding: []disruption.Disruption{firmwareRequest("b00t", true)}}
	restart := convergence{condition: notConverged("ClusterConverged", "RestartRequested", "restart"), requestRestart: true}

	conv := d.gate(&reader{}, drainNode(true, true, ""), nil, turnGranted, drainNow, restart)

	if conv.requestRestart {
		t.Error("a restart ran while a request held the turn")
	}
}

func TestTheHeldConditionFollowsTheHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		waiting := firmwareRequest("b00t", true)
		released := heldOn(firmwareRequest("b00t", false))
		released.Metadata.Name = "node-4.firmware-db"
		server := requestsAPI(waiting, released)
		client, recorder, recorded := recordingClient(t, server)
		r := &reader{client: client, recorder: recorder}

		settleHolds(r, storedRequests(t, client), []disruption.Disruption{waiting}, true, drainNow)

		held := storedRequests(t, client)
		byName := func(name string) *api.Condition {
			d := held[slices.IndexFunc(held, func(d disruption.Disruption) bool { return d.Metadata.Name == name })]
			return api.FindCondition(d.Status.Conditions, disruption.HeldCondition)
		}
		if c := byName("node-4.firmware-dbx"); c == nil || c.Status != api.ConditionTrue || c.Reason != "Drained" {
			t.Errorf("the waiting request's Held = %+v, want True Drained", c)
		}
		if c := byName("node-4.firmware-db"); c == nil || c.Status != api.ConditionFalse || c.Reason != "Released" {
			t.Errorf("the released request's Held = %+v, want False Released", c)
		}
		synctest.Wait()
		var reasons []string
		for _, e := range recorded.List() {
			reasons = append(reasons, e.InvolvedObject.Name+" "+e.Reason)
		}
		slices.Sort(reasons)
		if want := []string{"node-4.firmware-db Released", "node-4.firmware-dbx Drained"}; !slices.Equal(reasons, want) {
			t.Errorf("Events = %v, want %v", reasons, want)
		}
	})
}

func TestNoHeldConditionBeforeTheDrainIsDone(t *testing.T) {
	waiting := firmwareRequest("b00t", true)
	c := testClient(t, requestsAPI(waiting))

	settleHolds(&reader{client: c}, storedRequests(t, c), []disruption.Disruption{waiting}, false, drainNow)

	if d := storedRequests(t, c)[0]; d.Held() || len(d.Status.Conditions) != 0 {
		t.Errorf("conditions = %+v, want none before the drain is done", d.Status.Conditions)
	}
}
