package main

// These tests cover the index from a claim's UID to the claim: what a
// prepare records, what one listing fills in, and what a UID no claim
// carries answers.

import (
	"fmt"
	"net/http"
	"testing"
)

// One claim the kubelet prepared here, as the spec on disk records
// it. The spec is what holds the refill to this node's own claims.
func preparedClaim(t *testing.T, uid string) {
	t.Helper()
	cdiDir = t.TempDir()
	edits := outputEdits(defaultSocketDir, waylandSocketPrefix+uid, "hdmi-a-1")
	if err := writeCDISpec(uid, []cdiDevice{{Name: uid + "-hdmi-a-1", ContainerEdits: edits}}); err != nil {
		t.Fatal(err)
	}
}

func TestAPrepareIsWhatTheIndexRemembers(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the index read %s, and a prepare recorded the claim", r.URL.Path)
	}))
	index := newClaimIndex(client)
	index.remember(filmClaimUID, "living-room", "film")

	held, err := index.claim(filmClaimUID)
	if err != nil {
		t.Fatal(err)
	}
	if held.key() != "living-room/film" {
		t.Errorf("the index holds %q, want living-room/film", held.key())
	}
}

// A UID no claim carries is a claim that was given back while its
// client kept drawing. The answer is remembered, because a miss that
// was not would list the claims again on every pass for as long as
// the client stayed connected.
func TestAUIDNoClaimCarriesIsRememberedAsGone(t *testing.T) {
	preparedClaim(t, filmClaimUID)
	listings := 0
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ResourceClaimsPath {
			t.Errorf("the index read %s, want the claim collection", r.URL.Path)
		}
		listings++
		fmt.Fprint(w, `{"items":[]}`)
	}))
	index := newClaimIndex(client)

	for range 2 {
		held, err := index.claim(filmClaimUID)
		if err != nil {
			t.Fatal(err)
		}
		if held.key() != "" {
			t.Errorf("the index holds %q, want no claim", held.key())
		}
	}
	if listings != 1 {
		t.Errorf("the index listed the claims %d times, want once", listings)
	}
}

// The listing fills in the claims of this node, and no others. An
// operator container that restarted under a running compositor has
// no prepare of its own to read.
func TestTheListingFillsInThisNodesClaims(t *testing.T) {
	preparedClaim(t, filmClaimUID)
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"items":[
		  {"metadata":{"name":"film","namespace":"living-room","uid":%q}},
		  {"metadata":{"name":"other","namespace":"elsewhere","uid":"other-claim-9999"}}
		]}`, filmClaimUID)
	}))
	index := newClaimIndex(client)

	held, err := index.claim(filmClaimUID)
	if err != nil {
		t.Fatal(err)
	}
	if held.key() != "living-room/film" {
		t.Errorf("the index holds %q, want living-room/film", held.key())
	}
	if _, known := index.held("other-claim-9999"); known {
		t.Error("the index holds a claim no prepare on this node named")
	}
}

// A listing that failed answers no claim and an error. The pass
// reports it and leaves the screen as it is, because a surface whose
// claim it could not read must not lose its labels.
func TestAListingThatFailedAnswersAnError(t *testing.T) {
	preparedClaim(t, filmClaimUID)
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	index := newClaimIndex(client)

	if _, err := index.claim(filmClaimUID); err == nil {
		t.Fatal("a listing that failed answered no error")
	}
}

// The operator wires one index. A plugin built without one records
// nothing and answers no claim, which is every test that drives a
// prepare with no placement pass behind it.
func TestNoClaimIndexHoldsNothing(t *testing.T) {
	var index *claimIndex

	index.remember(filmClaimUID, "living-room", "film")
	held, err := index.claim(filmClaimUID)
	if err != nil || held.key() != "" {
		t.Errorf("an index that is not there answered %q, %v", held.key(), err)
	}
}

func TestTheSharedLabelsAreTheOnesBothHoldersCarry(t *testing.T) {
	for _, drill := range []struct {
		name string
		held map[string]string
		also map[string]string
		want map[string]string
	}{
		{
			name: "one label with two values",
			held: map[string]string{"panel": "notices"},
			also: map[string]string{"panel": "lot"},
			want: map[string]string{},
		},
		{
			name: "a label one holder does not carry",
			held: map[string]string{"panel": "notices", "pod": "first"},
			also: map[string]string{"panel": "notices"},
			want: map[string]string{"panel": "notices"},
		},
		{
			// A label with an empty value is a label, so the key both
			// holders carry with no value is shared.
			name: "a label with an empty value",
			held: map[string]string{"panel": ""},
			also: map[string]string{"panel": ""},
			want: map[string]string{"panel": ""},
		},
	} {
		t.Run(drill.name, func(t *testing.T) {
			got := sharedLabels(drill.held, drill.also)
			if len(got) != len(drill.want) {
				t.Fatalf("sharedLabels = %v, want %v", got, drill.want)
			}
			for key, value := range drill.want {
				if got[key] != value {
					t.Errorf("sharedLabels[%q] = %q, want %q", key, got[key], value)
				}
			}
		})
	}
}
