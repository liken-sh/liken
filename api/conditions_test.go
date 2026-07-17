package api

import (
	"testing"
	"time"
)

var (
	t0 = time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Minute)
)

func TestSetConditionAddsNew(t *testing.T) {
	conditions := SetCondition(nil, Condition{
		Type: "Ready", Status: "True", Reason: "BootComplete",
	}, t0)
	if len(conditions) != 1 {
		t.Fatalf("got %d conditions", len(conditions))
	}
	if !conditions[0].LastTransitionTime.Equal(t0) {
		t.Errorf("transition time: got %v", conditions[0].LastTransitionTime)
	}
}

func TestSetConditionKeepsTransitionTimeWhenStatusUnchanged(t *testing.T) {
	conditions := SetCondition(nil, Condition{
		Type: "Ready", Status: "True", Reason: "BootComplete",
	}, t0)
	conditions = SetCondition(conditions, Condition{
		Type: "Ready", Status: "True", Reason: "StillFine",
	}, t1)
	if len(conditions) != 1 {
		t.Fatalf("got %d conditions", len(conditions))
	}
	if !conditions[0].LastTransitionTime.Equal(t0) {
		t.Errorf("transition time moved to %v", conditions[0].LastTransitionTime)
	}
	if conditions[0].Reason != "StillFine" {
		t.Errorf("reason not updated: %q", conditions[0].Reason)
	}
}

func TestSetConditionMovesTransitionTimeWhenStatusChanges(t *testing.T) {
	conditions := SetCondition(nil, Condition{
		Type: "Ready", Status: "True",
	}, t0)
	conditions = SetCondition(conditions, Condition{
		Type: "Ready", Status: "False", Reason: "K3sDown",
	}, t1)
	if !conditions[0].LastTransitionTime.Equal(t1) {
		t.Errorf("transition time: got %v", conditions[0].LastTransitionTime)
	}
}

func TestSetConditionKeepsDistinctTypesApart(t *testing.T) {
	conditions := SetCondition(nil, Condition{Type: "Ready", Status: "True"}, t0)
	conditions = SetCondition(conditions, Condition{Type: "SysctlsApplied", Status: "True"}, t1)
	if len(conditions) != 2 {
		t.Fatalf("got %d conditions", len(conditions))
	}
}

func TestRemoveConditionDropsTheNamedType(t *testing.T) {
	conditions := SetCondition(nil, Condition{Type: "Ready", Status: "True"}, t0)
	conditions = SetCondition(conditions, Condition{Type: "RebootApproved", Status: "True"}, t0)
	conditions = RemoveCondition(conditions, "RebootApproved")
	if len(conditions) != 1 || conditions[0].Type != "Ready" {
		t.Errorf("got %+v", conditions)
	}
}

func TestRemoveConditionLeavesOthersAloneWhenTypeIsAbsent(t *testing.T) {
	conditions := SetCondition(nil, Condition{Type: "Ready", Status: "True"}, t0)
	conditions = RemoveCondition(conditions, "RebootApproved")
	if len(conditions) != 1 {
		t.Errorf("got %+v", conditions)
	}
}

func TestFindConditionReportsTheNamedType(t *testing.T) {
	conditions := SetCondition(nil, Condition{Type: "Ready", Status: "True"}, t0)
	if c := FindCondition(conditions, "Ready"); c == nil || c.Status != "True" {
		t.Errorf("got %+v", c)
	}
}

func TestFindConditionReportsNilWhenAbsent(t *testing.T) {
	if c := FindCondition(nil, "Ready"); c != nil {
		t.Errorf("got %+v", c)
	}
}
