package main

// Tests for a reboot that a person requests with a Disruption. Such a
// reboot stages no document and waits for the cluster's turn.

import (
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/machine"
)

var requestBootTime = time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)

// requestFacts returns facts with only a boot time, which is all that
// decideRebootRequest reads.
func requestFacts(at time.Time) *machine.MachineStatus {
	return &machine.MachineStatus{Boot: machine.BootStatus{Time: &at}}
}

func requestingMachine(policy machine.RebootPolicy, annotations map[string]string) *machine.Machine {
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "liken-dev", Annotations: annotations}}
	m.Spec.RebootPolicy = policy
	return m
}

func shortBootID(at time.Time) string {
	return machine.BootID(machine.BootStatus{Time: &at})[:12]
}

func TestNoRequestMeansNothingRequested(t *testing.T) {
	conv := decideRebootRequest(requestingMachine(machine.RebootAuto, nil), requestFacts(requestBootTime), turnGranted, nil)
	if conv.condition.Status != api.ConditionTrue || conv.condition.Reason != "NothingRequested" {
		t.Fatalf("condition = %+v, want True NothingRequested", conv.condition)
	}
	if conv.requestReboot || conv.pending != nil {
		t.Fatalf("conv = %+v, want no reboot and no pending entry when no request exists", conv)
	}
	// A person who creates a Disruption by hand reads the boot ID here.
	if !strings.Contains(conv.condition.Message, shortBootID(requestBootTime)) {
		t.Fatalf("the message is %q, want it to contain the boot ID %s", conv.condition.Message, shortBootID(requestBootTime))
	}
}

func TestNoRequestAndNoBootRecordIsStillNothingRequested(t *testing.T) {
	conv := decideRebootRequest(requestingMachine(machine.RebootAuto, nil), nil, turnGranted, nil)
	if conv.condition.Status != api.ConditionTrue || conv.condition.Reason != "NothingRequested" {
		t.Fatalf("condition = %+v, want True NothingRequested because no request exists, even with no boot record", conv.condition)
	}
}

func TestARequestWithNoBootRecordGivesUnknown(t *testing.T) {
	m := requestingMachine(machine.RebootAuto, nil)
	requests := bootRequestFor(requestBootTime, false)
	for _, facts := range []*machine.MachineStatus{nil, {}} {
		conv := decideRebootRequest(m, facts, turnGranted, requests)
		if conv.condition.Status != api.ConditionUnknown || conv.condition.Reason != "FactsIncomplete" {
			t.Fatalf("condition = %+v, want Unknown FactsIncomplete", conv.condition)
		}
		if conv.requestReboot {
			t.Fatal("requestReboot is true, want false because a machine with no boot ID matches no request")
		}
	}
}

func TestAutoWaitsForTheClusterToGrantTheTurn(t *testing.T) {
	m := requestingMachine(machine.RebootAuto, nil)
	requests := bootRequestFor(requestBootTime, false)
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnAwaiting, requests)
	if conv.condition.Reason != "AwaitingTurn" {
		t.Fatalf("condition = %+v, want reason AwaitingTurn, because a request waits for its turn like every other reboot", conv.condition)
	}
	if conv.requestReboot {
		t.Fatal("requestReboot is true, want false before cluster-operator grants a turn")
	}
}

func TestAutoRebootsOnItsGrantedTurnWithNoApproval(t *testing.T) {
	m := requestingMachine(machine.RebootAuto, nil)
	requests := bootRequestFor(requestBootTime, false)
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted, requests)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot {
		t.Fatalf("condition = %+v, want RebootRequested, because on an Auto machine a granted turn is enough", conv.condition)
	}
}

func TestManualWaitsForApprovalEvenWithATurn(t *testing.T) {
	m := requestingMachine(machine.RebootManual, nil)
	requests := bootRequestFor(requestBootTime, false)
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted, requests)
	if conv.condition.Reason != "RebootPending" || conv.requestReboot {
		t.Fatalf("condition = %+v, want RebootPending, because a Manual machine waits for approval as it does for a staged change", conv.condition)
	}
}

func TestManualRebootsOnApprovalOfTheSameBoot(t *testing.T) {
	m := requestingMachine(machine.RebootManual, nil)
	requests := bootRequestFor(requestBootTime, true)
	awaiting := decideRebootRequest(m, requestFacts(requestBootTime), turnAwaiting, requests)
	if awaiting.condition.Reason != "AwaitingTurn" || awaiting.requestReboot {
		t.Fatalf("condition = %+v, want AwaitingTurn, because an approved Manual machine still waits for its turn", awaiting.condition)
	}
	granted := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted, requests)
	if granted.condition.Reason != "RebootRequested" || !granted.requestReboot {
		t.Fatalf("condition = %+v, want RebootRequested, because the request is approved and the turn is granted", granted.condition)
	}
}

func TestAnApprovalOfAnotherHashDoesNotRelease(t *testing.T) {
	m := requestingMachine(machine.RebootManual, nil)
	requests := bootRequestFor(requestBootTime, false)
	requests[0].Spec.ApprovedHash = "deadbeefdead"
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted, requests)
	if conv.condition.Reason != "RebootPending" || conv.requestReboot {
		t.Fatalf("condition = %+v, want RebootPending with no reboot, because the approval names another hash", conv.condition)
	}
}

// A request is a Disruption, so these annotations on the Machine
// request and approve nothing.
func TestRequestAnnotationsOnTheMachineRequestNothing(t *testing.T) {
	short := shortBootID(requestBootTime)
	m := requestingMachine(machine.RebootAuto, map[string]string{
		"liken.sh/request-reboot":     short,
		"liken.sh/approve-disruption": short,
	})
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted, nil)
	if conv.condition.Reason != "NothingRequested" || conv.requestReboot {
		t.Fatalf("condition = %+v, want NothingRequested with no reboot", conv.condition)
	}
}

func TestARequestIsDoneAfterTheRebootItAskedFor(t *testing.T) {
	m := requestingMachine(machine.RebootAuto, nil)
	requests := bootRequestFor(requestBootTime, false)
	rebooted := requestBootTime.Add(3 * time.Minute)
	conv := decideRebootRequest(m, requestFacts(rebooted), turnGranted, requests)
	if conv.condition.Status != api.ConditionTrue || conv.condition.Reason != "NothingRequested" {
		t.Fatalf("condition = %+v, want True NothingRequested, because the request names a boot that has ended", conv.condition)
	}
	if conv.requestReboot || conv.pending != nil {
		t.Fatal("conv requests a reboot or lists a pending entry, want neither for a request that names an earlier boot")
	}
}

func TestARequestForTheNewBootRebootsAgain(t *testing.T) {
	rebooted := requestBootTime.Add(3 * time.Minute)
	m := requestingMachine(machine.RebootAuto, nil)
	requests := bootRequestFor(rebooted, false)
	conv := decideRebootRequest(m, requestFacts(rebooted), turnGranted, requests)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot {
		t.Fatalf("condition = %+v, want RebootRequested, because a request for the new boot works without deleting the old one", conv.condition)
	}
}

func TestTheRequestIsListedInPending(t *testing.T) {
	m := requestingMachine(machine.RebootManual, nil)
	requests := bootRequestFor(requestBootTime, false)
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnAwaiting, requests)
	if conv.pending == nil {
		t.Fatal("conv.pending is nil, want an entry because liken approve-reboot reads status.pending to find what to grant")
	}
	if conv.pending.Kind != machine.DisruptionReboot {
		t.Errorf("pending kind = %q, want %q", conv.pending.Kind, machine.DisruptionReboot)
	}
	if conv.pending.Condition != rebootRequestCondition {
		t.Errorf("pending condition = %q, want %q", conv.pending.Condition, rebootRequestCondition)
	}
	if conv.pending.Hash != machine.BootID(machine.BootStatus{Time: &requestBootTime}) {
		t.Errorf("pending hash = %q, want the whole boot ID", conv.pending.Hash)
	}
}

func TestTheRequestStagesNothing(t *testing.T) {
	m := requestingMachine(machine.RebootAuto, nil)
	requests := bootRequestFor(requestBootTime, false)
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted, requests)
	if conv.stage || conv.manifest != nil || conv.withdraw || conv.clearRejection {
		t.Fatalf("conv = %+v, want nothing staged, because a requested reboot writes no document and the boot promotes no slot", conv)
	}
	if conv.requestRestart || conv.requestLoad {
		t.Fatalf("conv = %+v, want no restart and no load, because a reboot request asks only for a reboot", conv)
	}
}

func TestCarryOutRebootRequestWritesAnIntentNamingNoManifest(t *testing.T) {
	dir := t.TempDir()
	m := requestingMachine(machine.RebootAuto, nil)
	requests := bootRequestFor(requestBootTime, false)
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted, requests)

	if got := carryOutRebootRequest(dir, conv, testNow, nil); got.Reason != "RebootRequested" {
		t.Fatalf("condition = %+v, want the decision's condition RebootRequested", got)
	}
	intent, err := machine.ReadRebootIntent(dir)
	if err != nil || intent == nil {
		t.Fatalf("reading the reboot intent: intent %v, error %v, want an intent", intent, err)
	}
	if intent.ManifestHash != "" {
		t.Errorf("intent manifest hash = %q, want empty because nothing is staged", intent.ManifestHash)
	}
	if !strings.Contains(intent.Reason, shortBootID(requestBootTime)) {
		t.Errorf("intent reason = %q, want it to contain the ended boot %s", intent.Reason, shortBootID(requestBootTime))
	}
}

func TestCarryOutRebootRequestWritesNothingWhileTheRequestWaits(t *testing.T) {
	dir := t.TempDir()
	m := requestingMachine(machine.RebootManual, nil)
	requests := bootRequestFor(requestBootTime, false)
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnAwaiting, requests)

	carryOutRebootRequest(dir, conv, testNow, nil)
	intent, err := machine.ReadRebootIntent(dir)
	if err != nil {
		t.Fatalf("reading the reboot intent: %v", err)
	}
	if intent != nil {
		t.Fatalf("intent = %+v, want none while the request waits", intent)
	}
}

func TestARequestedRebootIsUpdatePendingUntilItRuns(t *testing.T) {
	m := requestingMachine(machine.RebootManual, nil)
	requests := bootRequestFor(requestBootTime, false)
	waiting := decideRebootRequest(m, requestFacts(requestBootTime), turnAwaiting, requests)
	if got := decidePhase([]api.Condition{waiting.condition}); got != api.PhaseUpdatePending {
		t.Errorf("phase = %s, want UpdatePending while a requested reboot waits", got)
	}
	m.Spec.RebootPolicy = machine.RebootAuto
	running := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted, requests)
	if got := decidePhase([]api.Condition{running.condition}); got != api.PhaseUpdating {
		t.Errorf("phase = %s, want Updating while the machine takes its requested reboot", got)
	}
}

// bootRequestFor returns a person's request to reboot the boot that
// started at at.
func bootRequestFor(at time.Time, approved bool) []disruption.Disruption {
	boot := machine.BootID(machine.BootStatus{Time: &at})
	d := disruption.New(disruption.RebootRequestName("liken-dev", boot), &machine.Machine{Metadata: api.ObjectMeta{Name: "liken-dev"}},
		disruption.Spec{Machine: "liken-dev", Requester: "kubernetes-admin", Kind: disruption.KindReboot, Hash: boot})
	if approved {
		d.Spec.ApprovedHash = boot[:12]
	}
	return []disruption.Disruption{*d}
}
