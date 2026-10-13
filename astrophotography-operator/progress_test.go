package main

import (
	"slices"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The keywords of a frame: the clear first, then the session's, the
// target's, OBJECT, and FRAMEID, with a later level replacing an earlier
// one in its place.
func TestTheKeywordsOfAFrame(t *testing.T) {
	session := []observatory.FITSKeyword{{Name: "OBSERVER", Value: "Ada"}, {Name: "PROGRAM", Value: "Survey"}}
	declared := []observatory.FITSKeyword{{Name: "PROGRAM", Value: "Orion"}}
	var got []string
	for _, k := range frameKeywords(session, declared, "M42", "0199d3a0-0000-7000-8000-000000000001") {
		got = append(got, k.Name+"="+k.Value)
	}
	want := []string{"INDI_CLEAR=", "OBSERVER=Ada", "PROGRAM=Orion", "OBJECT=M42", "FRAMEID=0199d3a0-0000-7000-8000-000000000001"}
	if !slices.Equal(got, want) {
		t.Errorf("%q, want %q", got, want)
	}
}

// The runner exposes the first sequence with work left, and work left
// counts the frames pending in an outbox.
func TestTheNextSequenceIsTheFirstWithWorkLeft(t *testing.T) {
	x := target("m42", lights(2), astrophotography.Sequence{Filter: "Red", Exposure: "10s", Count: 2})
	x.Status.Sequences = []astrophotography.SequenceProgress{{Sequence: lights(2), Archived: 1, Pending: 1}}
	if i, ok := nextSequence(aligned(x)); !ok || i != 1 {
		t.Errorf("next %d, %v; want 1", i, ok)
	}
	if len(x.Status.Sequences) != 1 {
		t.Error("aligned changed the target")
	}
}

// A sequence whose exposure did not come through the API server's check
// has no settings, and a target with no creation time sorts first.
func TestASequenceWithAnExposureThatDoesNotRead(t *testing.T) {
	if _, err := settingsOf(astrophotography.Sequence{Exposure: "five minutes"}); err == nil {
		t.Error("five minutes read")
	}
	if !created(astrophotography.Target{}).IsZero() {
		t.Error("a target with no creationTimestamp has a time")
	}
}
