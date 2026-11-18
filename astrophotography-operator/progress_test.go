package main

import (
	"slices"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// A frame's keywords come in this order: the INDI_CLEAR keyword, the
// session's, the target's, OBJECT, and FRAMEID. A later keyword
// replaces an earlier one of the same name in the earlier one's
// position.
func TestTheKeywordsOfAFrame(t *testing.T) {
	session := []observatory.FITSKeyword{{Name: "OBSERVER", Value: "Ada"}, {Name: "PROGRAM", Value: "Survey"}}
	declared := []observatory.FITSKeyword{{Name: "PROGRAM", Value: "Orion"}}
	var got []string
	for _, k := range frameKeywords(session, declared, "M42", "0199d3a0-0000-7000-8000-000000000001") {
		got = append(got, k.Name+"="+k.Value)
	}
	want := []string{"INDI_CLEAR=", "OBSERVER=Ada", "PROGRAM=Orion", "OBJECT=M42", "FRAMEID=0199d3a0-0000-7000-8000-000000000001"}
	if !slices.Equal(got, want) {
		t.Errorf("frameKeywords = %q, want %q", got, want)
	}
}

// The runner exposes the first sequence with work left, and pending
// frames in an outbox count against it.
func TestTheNextSequenceIsTheFirstWithWorkLeft(t *testing.T) {
	x := target("m42", lights(2), astrophotography.Sequence{Filter: "Red", Exposure: "10s", Count: 2})
	x.Status.Sequences = []astrophotography.SequenceProgress{{Sequence: lights(2), Archived: 1, Pending: 1}}
	if i, ok := nextSequence(aligned(x)); !ok || i != 1 {
		t.Errorf("nextSequence = %d, %v, want 1, true", i, ok)
	}
	if len(x.Status.Sequences) != 1 {
		t.Error("aligned changed the target's status.sequences, want it unchanged")
	}
}

// A sequence whose exposure is not a valid duration has no settings,
// and a target with no creationTimestamp has the zero time.
func TestASequenceWithAnExposureThatDoesNotRead(t *testing.T) {
	if _, err := settingsOf(astrophotography.Sequence{Exposure: "five minutes"}); err == nil {
		t.Error("settingsOf accepted the exposure \"five minutes\", want an error")
	}
	if !created(astrophotography.Target{}).IsZero() {
		t.Error("created returned a time for a Target with no creationTimestamp, want the zero time")
	}
}
