package main

import (
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/kubernetes"
)

var stuckNow = time.Date(2026, 9, 29, 6, 45, 0, 0, time.UTC)

// terminatingPod is an OS pod being deleted, with deletionTimestamp set
// to deadline (the eviction time plus the grace period).
func terminatingPod(name, uid string, deadline time.Time) kubernetes.Pod {
	p := operatorPod(name, "node-1", "0.1.0")
	p.Metadata.UID = uid
	p.Metadata.DeletionTimestamp = deadline.Format(time.RFC3339)
	return p
}

// onePass runs a complete pass over one pod listing and returns the
// names of the pods it reports.
func onePass(overduePods *overduePodReports, pods []kubernetes.Pod, now time.Time) []string {
	names := refreshNames(overduePods.overdue(pods, now))
	overduePods.settle(true)
	return names
}

func TestAPodPastItsDeadlineIsReportedOnce(t *testing.T) {
	overduePods := &overduePodReports{}
	pods := []kubernetes.Pod{terminatingPod("op-1", "uid-1", stuckNow.Add(-time.Minute))}

	if got := onePass(overduePods, pods, stuckNow); len(got) != 1 || got[0] != "op-1" {
		t.Errorf("the first pass past the deadline reports %v, want [op-1]", got)
	}
	if got := onePass(overduePods, pods, stuckNow.Add(time.Minute)); len(got) != 0 {
		t.Errorf("a later pass reports %v, want none", got)
	}
}

func TestAPodInsideItsGracePeriodIsNotReported(t *testing.T) {
	overduePods := &overduePodReports{}
	pods := []kubernetes.Pod{terminatingPod("op-1", "uid-1", stuckNow.Add(time.Second))}

	if got := onePass(overduePods, pods, stuckNow); len(got) != 0 {
		t.Errorf("a pod inside its grace period is reported as %v, want none", got)
	}
	if got := onePass(overduePods, pods, stuckNow.Add(2*time.Second)); len(got) != 1 {
		t.Errorf("the pod past its deadline is reported as %v, want once", got)
	}
}

func TestAPodThatIsNotTerminatingIsNotReported(t *testing.T) {
	overduePods := &overduePodReports{}
	pods := []kubernetes.Pod{operatorPod("op-1", "node-1", "0.1.0")}

	if got := onePass(overduePods, pods, stuckNow); len(got) != 0 {
		t.Errorf("a running pod is reported as %v, want none", got)
	}
}

// A pass reads the listing of each OS DaemonSet, and reading the
// second listing does not make overduePodReports forget the pods of the
// first.
func TestEveryListingOfOnePassIsRemembered(t *testing.T) {
	overduePods := &overduePodReports{}
	operator := []kubernetes.Pod{terminatingPod("op-1", "uid-1", stuckNow.Add(-time.Minute))}
	relay := []kubernetes.Pod{terminatingPod("logs-1", "uid-2", stuckNow.Add(-time.Minute))}

	overduePods.overdue(operator, stuckNow)
	overduePods.overdue(relay, stuckNow)
	overduePods.settle(true)

	if got := refreshNames(overduePods.overdue(operator, stuckNow)); len(got) != 0 {
		t.Errorf("the operator's pod is reported as %v, want none", got)
	}
	if got := refreshNames(overduePods.overdue(relay, stuckNow)); len(got) != 0 {
		t.Errorf("the relay's pod is reported as %v, want none", got)
	}
}

// A pass that could not read one listing keeps every earlier UID, so
// a pod in the missed listing is not reported a second time.
func TestAPassThatMissedAListingForgetsNothing(t *testing.T) {
	overduePods := &overduePodReports{}
	pods := []kubernetes.Pod{terminatingPod("op-1", "uid-1", stuckNow.Add(-time.Minute))}
	onePass(overduePods, pods, stuckNow)

	overduePods.settle(false)

	if got := onePass(overduePods, pods, stuckNow); len(got) != 0 {
		t.Errorf("after a pass that missed the pod, it is reported as %v, want none", got)
	}
}
