package main

// The handover: a request whose requester set BootNext for the turn's
// reboot, which the intent names so that init leaves BootNext alone.

import (
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/machine"
)

// capsuleRequest is firmware-operator's request after it staged a
// capsule and removed spec.hold: it set BootNext and asks to keep it.
func capsuleRequest(hash string) disruption.Disruption {
	d := firmwareRequest(hash, false)
	d.Metadata.Name = "node-4.firmware-bios"
	d.Spec.KeepBootNext = true
	return d
}

func TestTheHandoverRequestIsReleasedApprovedAndNotDone(t *testing.T) {
	boot := machine.BootID(machine.BootStatus{Time: &requestBootTime})
	holding := capsuleRequest(boot)
	holding.Spec.Hold = true
	plain := capsuleRequest(boot)
	plain.Spec.KeepBootNext = false
	cases := []struct {
		name     string
		policy   machine.RebootPolicy
		requests []disruption.Disruption
		want     string
	}{
		{"a released request that keeps BootNext", machine.RebootAuto, []disruption.Disruption{capsuleRequest(boot)}, "node-4.firmware-bios"},
		{"a request that still holds", machine.RebootAuto, []disruption.Disruption{holding}, ""},
		{"a request that does not keep BootNext", machine.RebootAuto, []disruption.Disruption{plain}, ""},
		{"a request for an earlier boot", machine.RebootAuto, []disruption.Disruption{capsuleRequest("51f0e2d8aa90aa")}, ""},
		{"a request not yet approved", machine.RebootManual, []disruption.Disruption{capsuleRequest(boot)}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := handoverRequest(c.requests, c.policy, boot); got != c.want {
				t.Errorf("handoverRequest = %q, want %q", got, c.want)
			}
		})
	}
}

// The turn's reboot carries the handover to the intent, whichever
// document asks for the reboot, once the drain is clear.
func TestTheGrantedRebootCarriesTheHandover(t *testing.T) {
	fake := &drainAPI{}
	client := testClient(t, fake.handler())
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	d := &disruptions{handover: "node-4.firmware-bios"}

	conv := d.gate(&reader{client: client}, drainNode(true, true, since), nil, turnGranted, drainNow, rebootingConvergence())

	if !conv.requestReboot || conv.keepBootNext != "node-4.firmware-bios" {
		t.Errorf("conv = %+v, want the reboot to keep BootNext for node-4.firmware-bios", conv)
	}
}

func TestNoHandoverWithoutTheTurn(t *testing.T) {
	d := &disruptions{handover: "node-4.firmware-bios"}

	conv := d.gate(nil, nil, nil, turnAwaiting, drainNow, rebootingConvergence())

	if conv.keepBootNext != "" {
		t.Errorf("keepBootNext = %q, want none before the turn is granted", conv.keepBootNext)
	}
}

func TestEachIntentCarriesTheHandover(t *testing.T) {
	conv := convergence{requestReboot: true, hash: "abc123", keepBootNext: "node-4.firmware-bios"}
	cases := []struct {
		name  string
		write func(dir string)
	}{
		{"a staged document's reboot", func(dir string) {
			carryOutConvergence(conv, machine.MachineManifests(t.TempDir()), dir, "spec", lifecycleNow, &passOutcome{})
		}},
		{"a requested reboot", func(dir string) {
			carryOutRebootRequest(dir, conv, testNow, &passOutcome{})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()

			c.write(dir)

			intent, err := machine.ReadRebootIntent(dir)
			if err != nil || intent == nil || intent.KeepBootNext != "node-4.firmware-bios" {
				t.Errorf("the intent is %+v, %v; want it to name node-4.firmware-bios", intent, err)
			}
		})
	}
}
