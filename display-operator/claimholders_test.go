package main

import (
	"fmt"
	"net/http"
	"testing"
)

// preparedClaim writes the CDI spec that prepareClaim writes for a
// claim on this node.
func preparedClaim(t *testing.T, uid string) {
	t.Helper()
	cdiDir = t.TempDir()
	edits := outputEdits(defaultSocketDir, waylandSocketPrefix+uid, "hdmi-a-1")
	if err := writeCDISpec(uid, []cdiDevice{{Name: uid + "-hdmi-a-1", ContainerEdits: edits}}); err != nil {
		t.Fatalf("writing the CDI spec: %v", err)
	}
}

func TestTheIndexReturnsTheClaimThatAPrepareRecorded(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the index read %s, and a prepare recorded the claim", r.URL.Path)
	}))
	index := newClaimIndex(client)
	index.remember(filmClaimUID, "media", "film")

	held, err := index.claim(filmClaimUID)
	if err != nil {
		t.Fatalf("reading the claim: %v", err)
	}
	if held.key() != "media/film" {
		t.Errorf("the index returned claim %q, want media/film", held.key())
	}
}

// A UID with no claim belongs to a claim that was deleted while its
// client stayed connected. The index stores the miss, so it does not
// list all claims again on every pass.
func TestAUIDWithNoClaimIsStoredAsGone(t *testing.T) {
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
			t.Fatalf("reading the claim: %v", err)
		}
		if held.key() != "" {
			t.Errorf("the index returned claim %q, want none", held.key())
		}
	}
	if listings != 1 {
		t.Errorf("the index listed the claims %d times, want once", listings)
	}
}

// An operator container that restarted while the compositor kept
// running finds its claims through the refill, because the prepare
// calls ran in the previous process.
func TestTheRefillAddsThisNodesClaims(t *testing.T) {
	preparedClaim(t, filmClaimUID)
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"items":[
		  {"metadata":{"name":"film","namespace":"media","uid":%q}},
		  {"metadata":{"name":"other","namespace":"elsewhere","uid":"other-claim-9999"}}
		]}`, filmClaimUID)
	}))
	index := newClaimIndex(client)

	held, err := index.claim(filmClaimUID)
	if err != nil {
		t.Fatalf("reading the claim: %v", err)
	}
	if held.key() != "media/film" {
		t.Errorf("the index returned claim %q, want media/film", held.key())
	}
	if _, known := index.lookup("other-claim-9999"); known {
		t.Error("the index returned a claim that no prepare on this node recorded, want none")
	}
}

// The placement pass reports the error and leaves the screen
// unchanged, so a surface whose claim could not be read does not lose
// its labels.
func TestAFailedListReturnsAnError(t *testing.T) {
	preparedClaim(t, filmClaimUID)
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	index := newClaimIndex(client)

	if _, err := index.claim(filmClaimUID); err == nil {
		t.Fatal("index.claim returned no error after the claim listing returned 500, want an error")
	}
}

// The operator always creates an index. Tests that prepare claims
// without a placement pass leave it nil.
func TestANilIndexRecordsNothing(t *testing.T) {
	var index *claimIndex

	index.remember(filmClaimUID, "media", "film")
	held, err := index.claim(filmClaimUID)
	if err != nil || held.key() != "" {
		t.Errorf("a nil index returned claim %q and error %v, want no claim and no error", held.key(), err)
	}
}

func TestTheSharedLabelsAreTheOnesBothHoldersCarry(t *testing.T) {
	for _, row := range []struct {
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
			name: "a label one holder does not have",
			held: map[string]string{"panel": "notices", "pod": "first"},
			also: map[string]string{"panel": "notices"},
			want: map[string]string{"panel": "notices"},
		},
		{
			name: "a label with an empty value",
			held: map[string]string{"panel": ""},
			also: map[string]string{"panel": ""},
			want: map[string]string{"panel": ""},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			got := sharedLabels(row.held, row.also)
			if len(got) != len(row.want) {
				t.Fatalf("sharedLabels = %v, want %v", got, row.want)
			}
			for key, value := range row.want {
				if got[key] != value {
					t.Errorf("sharedLabels[%q] = %q, want %q", key, got[key], value)
				}
			}
		})
	}
}
