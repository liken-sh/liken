package main

// These tests cover the reboot a person requests: the annotation
// round-trip, the two policies, and the two things a plain reboot
// must never do, which are stage a document and skip the cluster's
// turn.

import (
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

var requestBootTime = time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)

// requestFacts is a boot record with a time, which is all the reboot
// request reads from the facts.
func requestFacts(at time.Time) *machine.MachineStatus {
	return &machine.MachineStatus{Boot: machine.BootStatus{Time: &at}}
}

// requestingMachine is a Machine annotated the way the CLI annotates
// one: the running boot's identity, in the 12-character short form
// every condition message shows.
func requestingMachine(policy machine.RebootPolicy, annotations map[string]string) *machine.Machine {
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "liken-dev", Annotations: annotations}}
	m.Spec.RebootPolicy = policy
	return m
}

func shortBootID(at time.Time) string {
	return machine.BootID(machine.BootStatus{Time: &at})[:12]
}

func TestNoAnnotationRequestsNothing(t *testing.T) {
	conv := decideRebootRequest(requestingMachine(machine.RebootAuto, nil), requestFacts(requestBootTime), turnGranted)
	if conv.condition.Status != api.ConditionTrue || conv.condition.Reason != "NothingRequested" {
		t.Fatalf("got %+v", conv.condition)
	}
	if conv.requestReboot || conv.pending != nil {
		t.Fatalf("an unasked machine reboots nothing: %+v", conv)
	}
	// The value an annotation has to carry, where a person reading
	// kubectl describe machine will find it.
	if !strings.Contains(conv.condition.Message, shortBootID(requestBootTime)) {
		t.Fatalf("the message must name this boot's identity: %s", conv.condition.Message)
	}
}

func TestNoAnnotationAndNoBootRecordStillReadsAsSettled(t *testing.T) {
	conv := decideRebootRequest(requestingMachine(machine.RebootAuto, nil), nil, turnGranted)
	if conv.condition.Status != api.ConditionTrue || conv.condition.Reason != "NothingRequested" {
		t.Fatalf("a machine nobody asked anything of is settled, boot record or not: %+v", conv.condition)
	}
}

func TestARequestWithNoBootRecordHoldsAtUnknown(t *testing.T) {
	m := requestingMachine(machine.RebootAuto, map[string]string{
		machine.RequestRebootAnnotation: shortBootID(requestBootTime)})
	for _, facts := range []*machine.MachineStatus{nil, {}} {
		conv := decideRebootRequest(m, facts, turnGranted)
		if conv.condition.Status != api.ConditionUnknown || conv.condition.Reason != "FactsIncomplete" {
			t.Fatalf("got %+v", conv.condition)
		}
		if conv.requestReboot {
			t.Fatal("a machine that published no boot identity must not reboot on a guess")
		}
	}
}

func TestAutoWaitsForTheClusterToGrantTheTurn(t *testing.T) {
	m := requestingMachine(machine.RebootAuto, map[string]string{
		machine.RequestRebootAnnotation: shortBootID(requestBootTime)})
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnAwaiting)
	if conv.condition.Reason != "AwaitingTurn" {
		t.Fatalf("a request takes its turn like every other reboot: %+v", conv.condition)
	}
	if conv.requestReboot {
		t.Fatal("no reboot before the conductor grants a turn")
	}
}

func TestAutoRebootsOnItsGrantedTurnWithNoApproval(t *testing.T) {
	m := requestingMachine(machine.RebootAuto, map[string]string{
		machine.RequestRebootAnnotation: shortBootID(requestBootTime)})
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot {
		t.Fatalf("Auto needs no second step once the turn is granted: %+v", conv.condition)
	}
}

func TestManualWaitsForAPersonEvenWithATurn(t *testing.T) {
	m := requestingMachine(machine.RebootManual, map[string]string{
		machine.RequestRebootAnnotation: shortBootID(requestBootTime)})
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted)
	if conv.condition.Reason != "RebootPending" || conv.requestReboot {
		t.Fatalf("Manual parks where a staged change parks: %+v", conv.condition)
	}
}

func TestManualReleasesOnTheApprovalOfTheSameBoot(t *testing.T) {
	short := shortBootID(requestBootTime)
	m := requestingMachine(machine.RebootManual, map[string]string{
		machine.RequestRebootAnnotation:     short,
		machine.ApproveDisruptionAnnotation: short,
	})
	awaiting := decideRebootRequest(m, requestFacts(requestBootTime), turnAwaiting)
	if awaiting.condition.Reason != "AwaitingTurn" || awaiting.requestReboot {
		t.Fatalf("an approved Manual machine joins the queue, it does not jump it: %+v", awaiting.condition)
	}
	granted := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted)
	if granted.condition.Reason != "RebootRequested" || !granted.requestReboot {
		t.Fatalf("an approved, granted machine reboots: %+v", granted.condition)
	}
}

func TestAnApprovalOfAnotherHashDoesNotRelease(t *testing.T) {
	m := requestingMachine(machine.RebootManual, map[string]string{
		machine.RequestRebootAnnotation:     shortBootID(requestBootTime),
		machine.ApproveDisruptionAnnotation: "deadbeefdead",
	})
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted)
	if conv.condition.Reason != "RebootPending" || conv.requestReboot {
		t.Fatalf("got %+v", conv.condition)
	}
	if !strings.Contains(conv.condition.Message, "deadbeefdead") {
		t.Fatalf("the message must show what the approval names: %s", conv.condition.Message)
	}
}

func TestTheRequestSpendsItselfOnTheBootThatComesBack(t *testing.T) {
	m := requestingMachine(machine.RebootAuto, map[string]string{
		machine.RequestRebootAnnotation: shortBootID(requestBootTime)})
	rebooted := requestBootTime.Add(3 * time.Minute)
	conv := decideRebootRequest(m, requestFacts(rebooted), turnGranted)
	if conv.condition.Status != api.ConditionTrue || conv.condition.Reason != "RequestSpent" {
		t.Fatalf("the annotation names a boot that is gone: %+v", conv.condition)
	}
	if conv.requestReboot || conv.pending != nil {
		t.Fatal("a spent request must never reboot the machine again")
	}
}

func TestASecondRequestNamesTheNewBootAndRebootsAgain(t *testing.T) {
	rebooted := requestBootTime.Add(3 * time.Minute)
	m := requestingMachine(machine.RebootAuto, map[string]string{
		machine.RequestRebootAnnotation: shortBootID(rebooted)})
	conv := decideRebootRequest(m, requestFacts(rebooted), turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot {
		t.Fatalf("a later request works with no cleanup in between: %+v", conv.condition)
	}
}

func TestTheRequestReportsItselfInPending(t *testing.T) {
	m := requestingMachine(machine.RebootManual, map[string]string{
		machine.RequestRebootAnnotation: shortBootID(requestBootTime)})
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnAwaiting)
	if conv.pending == nil {
		t.Fatal("liken approve-reboot reads status.pending to find what to grant")
	}
	if conv.pending.Kind != machine.DisruptionReboot {
		t.Errorf("got kind %q", conv.pending.Kind)
	}
	if conv.pending.Condition != rebootRequestCondition {
		t.Errorf("got condition %q", conv.pending.Condition)
	}
	if conv.pending.Hash != machine.BootID(machine.BootStatus{Time: &requestBootTime}) {
		t.Errorf("the entry must carry the boot's whole identity: %q", conv.pending.Hash)
	}
}

func TestTheRequestStagesNothing(t *testing.T) {
	m := requestingMachine(machine.RebootAuto, map[string]string{
		machine.RequestRebootAnnotation: shortBootID(requestBootTime)})
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted)
	if conv.stage || conv.manifest != nil || conv.withdraw || conv.clearRejection {
		t.Fatalf("a plain reboot writes no document, so the boot promotes no slot: %+v", conv)
	}
	if conv.requestRestart || conv.requestLoad {
		t.Fatalf("a reboot request asks for a reboot and nothing else: %+v", conv)
	}
}

func TestCarryOutRebootRequestWritesAnIntentNamingNoManifest(t *testing.T) {
	dir := t.TempDir()
	m := requestingMachine(machine.RebootAuto, map[string]string{
		machine.RequestRebootAnnotation: shortBootID(requestBootTime)})
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted)

	if got := carryOutRebootRequest(dir, conv, testNow); got.Reason != "RebootRequested" {
		t.Fatalf("the decision's condition comes back: %+v", got)
	}
	intent, err := machine.ReadRebootIntent(dir)
	if err != nil || intent == nil {
		t.Fatalf("init reads the intent from this directory: %v, %v", intent, err)
	}
	if intent.ManifestHash != "" {
		t.Errorf("the intent must name no staged manifest: %q", intent.ManifestHash)
	}
	if !strings.Contains(intent.Reason, shortBootID(requestBootTime)) {
		t.Errorf("the console line should say which boot was asked to end: %q", intent.Reason)
	}
}

func TestCarryOutRebootRequestWritesNothingWhileItWaits(t *testing.T) {
	dir := t.TempDir()
	m := requestingMachine(machine.RebootManual, map[string]string{
		machine.RequestRebootAnnotation: shortBootID(requestBootTime)})
	conv := decideRebootRequest(m, requestFacts(requestBootTime), turnAwaiting)

	carryOutRebootRequest(dir, conv, testNow)
	intent, err := machine.ReadRebootIntent(dir)
	if err != nil {
		t.Fatal(err)
	}
	if intent != nil {
		t.Fatalf("a waiting request must leave no intent behind: %+v", intent)
	}
}

func TestARequestedRebootReadsAsUpdatePendingUntilItRuns(t *testing.T) {
	m := requestingMachine(machine.RebootManual, map[string]string{
		machine.RequestRebootAnnotation: shortBootID(requestBootTime)})
	waiting := decideRebootRequest(m, requestFacts(requestBootTime), turnAwaiting)
	if got := decidePhase([]api.Condition{waiting.condition}); got != api.PhaseUpdatePending {
		t.Errorf("a machine waiting on a requested reboot is UpdatePending, got %s", got)
	}
	m.Spec.RebootPolicy = machine.RebootAuto
	running := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted)
	if got := decidePhase([]api.Condition{running.condition}); got != api.PhaseUpdating {
		t.Errorf("a machine taking its requested reboot is Updating, got %s", got)
	}
}
