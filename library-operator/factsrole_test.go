package main

import (
	"strings"
	"testing"
)

// what these tests read: the facts one container runs, in the order its
// LIBRARY_FACTS names them, and what it does with a list it cannot run.

func TestTheFactsOfAContainerComeOffItsList(t *testing.T) {
	cases := []struct {
		name string
		list string
		want []string
	}{
		{name: "one fact", list: "probe", want: []string{factProbe}},
		{name: "several facts in order", list: "identity,probe",
			want: []string{factIdentity, factProbe}},
		{name: "a list with spaces and a trailing comma", list: " probe , identity ,",
			want: []string{factProbe, factIdentity}},
		{name: "an empty list", list: ""},
		{name: "a list of separators alone", list: ",,"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := namedFacts(one.list)
			if len(got) != len(one.want) {
				t.Fatalf("facts = %v, want %v", got, one.want)
			}
			for index, name := range one.want {
				if got[index] != name {
					t.Errorf("fact %d = %q, want %q", index, got[index], name)
				}
			}
		})
	}
}

// a container fails before it waits on anything where its list is empty or
// names a fact this image does not run, so the Job says what to repair.
func TestAContainerRefusesAListItCannotRun(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)

	cases := []struct {
		name  string
		facts []string
		says  string
	}{
		{name: "a list with no fact in it", says: "names no fact"},
		{name: "a fact this image does not run", facts: []string{"trickplay"},
			says: "does not run"},
		{name: "one name this image does not run among ones it does",
			facts: []string{factProbe, factClearart}, says: "does not run"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			err := work.runFacts(t.Context(), one.facts)
			if err == nil {
				t.Fatalf("the container ran %v, want a refusal", one.facts)
			}
			if !strings.Contains(err.Error(), one.says) {
				t.Errorf("error = %q, want it to say %q", err, one.says)
			}
		})
	}
}
