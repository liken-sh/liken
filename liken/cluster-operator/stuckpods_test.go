package main

// The report of an OS pod that stays terminating past its deadline.

import (
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/kubernetes"
)

var stuckNow = time.Date(2026, 9, 29, 6, 45, 0, 0, time.UTC)

// terminatingPod is an OS pod whose deletion deadline is the given
// time: the moment of the eviction plus its grace period.
func terminatingPod(name, uid string, deadline time.Time) kubernetes.Pod {
	p := operatorPod(name, "node-1", "0.1.0")
	p.Metadata.UID = uid
	p.Metadata.DeletionTimestamp = deadline.Format(time.RFC3339)
	return p
}

// sweepOnce runs one sweep's worth of the report over one listing.
func sweepOnce(steward *podSteward, pods []kubernetes.Pod, now time.Time) []string {
	names := refreshNames(steward.overdue(pods, now))
	steward.settle(true)
	return names
}

func TestAPodPastItsDeadlineIsReportedOnce(t *testing.T) {
	steward := &podSteward{}
	pods := []kubernetes.Pod{terminatingPod("op-1", "uid-1", stuckNow.Add(-time.Minute))}

	if got := sweepOnce(steward, pods, stuckNow); len(got) != 1 || got[0] != "op-1" {
		t.Errorf("the first sweep past the deadline reports %v, want [op-1]", got)
	}
	if got := sweepOnce(steward, pods, stuckNow.Add(time.Minute)); len(got) != 0 {
		t.Errorf("a later sweep reports %v again", got)
	}
}

func TestAPodInsideItsGracePeriodIsNotReported(t *testing.T) {
	steward := &podSteward{}
	pods := []kubernetes.Pod{terminatingPod("op-1", "uid-1", stuckNow.Add(time.Second))}

	if got := sweepOnce(steward, pods, stuckNow); len(got) != 0 {
		t.Errorf("a pod still inside its grace period is reported: %v", got)
	}
	if got := sweepOnce(steward, pods, stuckNow.Add(2*time.Second)); len(got) != 1 {
		t.Errorf("the pod past its deadline is reported %v, want once", got)
	}
}

func TestAPodThatIsNotTerminatingIsNotReported(t *testing.T) {
	steward := &podSteward{}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "0.1.0")}

	if got := sweepOnce(steward, pods, stuckNow); len(got) != 0 {
		t.Errorf("a running pod is reported as stuck: %v", got)
	}
}

// Each OS DaemonSet's listing is read on the same sweep, and a pod of
// the second listing does not make the steward forget a pod of the
// first.
func TestEveryListingOfOneSweepIsRemembered(t *testing.T) {
	steward := &podSteward{}
	operator := []kubernetes.Pod{terminatingPod("op-1", "uid-1", stuckNow.Add(-time.Minute))}
	relay := []kubernetes.Pod{terminatingPod("logs-1", "uid-2", stuckNow.Add(-time.Minute))}

	steward.overdue(operator, stuckNow)
	steward.overdue(relay, stuckNow)
	steward.settle(true)

	if got := refreshNames(steward.overdue(operator, stuckNow)); len(got) != 0 {
		t.Errorf("the operator's pod is reported again: %v", got)
	}
	if got := refreshNames(steward.overdue(relay, stuckNow)); len(got) != 0 {
		t.Errorf("the relay's pod is reported again: %v", got)
	}
}

// A sweep that could not read one listing forgets nothing, so the
// pod in that listing is not reported a second time.
func TestASweepThatMissedAListingForgetsNothing(t *testing.T) {
	steward := &podSteward{}
	pods := []kubernetes.Pod{terminatingPod("op-1", "uid-1", stuckNow.Add(-time.Minute))}
	sweepOnce(steward, pods, stuckNow)

	steward.settle(false)

	if got := sweepOnce(steward, pods, stuckNow); len(got) != 0 {
		t.Errorf("the pod is reported again after a sweep that missed its listing: %v", got)
	}
}
