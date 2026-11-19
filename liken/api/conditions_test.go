package api

import (
	"slices"
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
		t.Fatalf("there are %d conditions, want 1", len(conditions))
	}
	if !conditions[0].LastTransitionTime.Equal(t0) {
		t.Errorf("the transition time is %v, want %v", conditions[0].LastTransitionTime, t0)
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
		t.Fatalf("there are %d conditions, want 1", len(conditions))
	}
	if !conditions[0].LastTransitionTime.Equal(t0) {
		t.Errorf("the transition time is %v, want %v", conditions[0].LastTransitionTime, t0)
	}
	if conditions[0].Reason != "StillFine" {
		t.Errorf("the reason is %q, want StillFine", conditions[0].Reason)
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
		t.Errorf("the transition time is %v, want %v", conditions[0].LastTransitionTime, t1)
	}
}

func TestSetConditionKeepsDistinctTypesApart(t *testing.T) {
	conditions := SetCondition(nil, Condition{Type: "Ready", Status: "True"}, t0)
	conditions = SetCondition(conditions, Condition{Type: "SysctlsApplied", Status: "True"}, t1)
	if len(conditions) != 2 {
		t.Fatalf("there are %d conditions, want 2", len(conditions))
	}
}

func TestRemoveConditionDropsTheNamedType(t *testing.T) {
	conditions := SetCondition(nil, Condition{Type: "Ready", Status: "True"}, t0)
	conditions = SetCondition(conditions, Condition{Type: "RebootApproved", Status: "True"}, t0)
	conditions = RemoveCondition(conditions, "RebootApproved")
	if len(conditions) != 1 || conditions[0].Type != "Ready" {
		t.Errorf("the conditions are %+v, want only Ready", conditions)
	}
}

func TestRemoveConditionLeavesOthersAloneWhenTypeIsAbsent(t *testing.T) {
	conditions := SetCondition(nil, Condition{Type: "Ready", Status: "True"}, t0)
	conditions = RemoveCondition(conditions, "RebootApproved")
	if len(conditions) != 1 {
		t.Errorf("the conditions are %+v, want only Ready", conditions)
	}
}

func TestFindConditionReportsTheNamedType(t *testing.T) {
	conditions := SetCondition(nil, Condition{Type: "Ready", Status: "True"}, t0)
	if c := FindCondition(conditions, "Ready"); c == nil || c.Status != "True" {
		t.Errorf("the condition is %+v, want Ready with status True", c)
	}
}

func TestFindConditionReportsNilWhenAbsent(t *testing.T) {
	if c := FindCondition(nil, "Ready"); c != nil {
		t.Errorf("the condition is %+v, want nil", c)
	}
}

func TestTransitionsNameEachNewStatusReasonOrCondition(t *testing.T) {
	before := []Condition{
		{Type: "Ready", Status: ConditionTrue, Reason: "Reconciled"},
		{Type: "SpecConverged", Status: ConditionTrue, Reason: "Converged", Message: "old"},
		{Type: "StorageReady", Status: ConditionFalse, Reason: "RolesInMemory"},
		{Type: "SysctlsApplied", Status: ConditionTrue, Reason: "Applied"},
	}
	after := []Condition{
		{Type: "Ready", Status: ConditionFalse, Reason: "Blocked"},
		{Type: "SpecConverged", Status: ConditionTrue, Reason: "Converged", Message: "new"},
		{Type: "StorageReady", Status: ConditionFalse, Reason: "ApplyFailed"},
		{Type: "ModulesLoaded", Status: ConditionTrue, Reason: "NothingDeclared"},
	}
	var got []string
	for _, c := range Transitions(before, after) {
		got = append(got, c.Type+"/"+c.Reason)
	}
	want := []string{"Ready/Blocked", "StorageReady/ApplyFailed", "ModulesLoaded/NothingDeclared"}
	if !slices.Equal(got, want) {
		t.Errorf("the transitions are %q, want %q", got, want)
	}
}
