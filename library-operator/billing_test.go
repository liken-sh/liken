package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
)

// TMDb's credits hold the whole cast and the whole crew, and the billing
// keeps every one of them, where the credits keep the directors and the
// writers of the crew.
func TestTheBillingOfTMDbIsTheWholeCastAndCrew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, _ := newFakeTMDb(t, map[string]string{
			tmdbKey("/3/movie/4242/credits", "", ""): `{"cast":[{"name":"Nora Vance","order":0}],"crew":[
				{"id":11,"name":"Iris Kell","job":"Director","department":"Directing"},
				{"id":11,"name":"Iris Kell","job":"Screenplay","department":"Writing"},
				{"id":15,"name":"","job":"Director","department":"Directing"},
				{"id":14,"name":"Bo Vance","job":"Director of Photography","department":"Camera"},
				{"id":14,"name":"Bo Vance","job":"Director of Photography","department":"Camera"}]}`,
			tmdbKey("/3/tv/99/aggregate_credits", "", ""): `{"cast":[],"crew":[
				{"id":22,"name":"Halvard Ness","department":"Production",
					"jobs":[{"job":"Executive Producer"},{"job":"Director"}]}]}`,
		})
		cases := []struct {
			kind string
			id   int
			want []billedPerson
		}{
			{libraryKindMovies, 4242, []billedPerson{
				{Name: "Nora Vance", Department: "Acting", Job: "Actor"},
				{Name: "Iris Kell", Department: "Directing", Job: "Director"},
				{Name: "Iris Kell", Department: "Writing", Job: "Screenplay"},
				{Name: "Bo Vance", Department: "Camera", Job: "Director of Photography"},
			}},
			{libraryKindSeries, 99, []billedPerson{
				{Name: "Halvard Ness", Department: "Production", Job: "Executive Producer"},
				{Name: "Halvard Ness", Department: "Production", Job: "Director"},
			}},
		}
		for _, test := range cases {
			answer, held, err := tmdbAnswerer{client: client}.credits(t.Context(), test.kind, test.id)
			if err != nil || !held {
				t.Fatalf("credits of %s = held %v, %v, want an answer", test.kind, held, err)
			}
			if !reflect.DeepEqual(answer.Billing, test.want) {
				t.Errorf("billing of %s = %+v, want %+v", test.kind, answer.Billing, test.want)
			}
		}
	})
}

// The credits fact writes the billing beside the credits, and the credits
// file holds none of the crew the billing adds.
func TestTheCreditsFactWritesTheBillingBesideTheCredits(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	folder := titleFolder(t, root, "The Signal (2014)")

	work.writeCredits(folder, factAnswer{
		Cast: []creditedActor{{Name: "Oren Tally", IDs: providerIDs{"tmdb": "9031"}}},
		Billing: []billedPerson{
			{Name: "Oren Tally", Department: "Acting", Job: "Actor"},
			{Name: "Bo Vance", Department: "Camera", Job: "Director of Photography"},
		},
	})

	billing := readFileString(t, filepath.Join(folder, likenDirectory, billingFileName))
	want := "billing:\n" +
		"    - name: Oren Tally\n      department: Acting\n      job: Actor\n" +
		"    - name: Bo Vance\n      department: Camera\n      job: Director of Photography\n"
	if billing != want {
		t.Errorf("billing.yaml = %q, want %q", billing, want)
	}
	credits := readFileString(t, filepath.Join(folder, likenDirectory, likenLedgerName(factCredits)))
	if strings.Contains(credits, "Bo Vance") {
		t.Errorf("credits.yaml = %q, want none of the crew the billing adds", credits)
	}
}

// A source that states no crew answers with no billing, and the billing a
// source wrote before it stays.
func TestAnAnswerWithNoBillingLeavesTheFile(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	folder := titleFolder(t, root, "The Signal (2014)")
	cast := []creditedActor{{Name: "Oren Tally"}}

	work.writeCredits(folder, factAnswer{Cast: cast, Billing: []billedPerson{{Name: "Bo Vance", Job: "Editor"}}})
	work.writeCredits(folder, factAnswer{Cast: cast})

	billing := readFileString(t, filepath.Join(folder, likenDirectory, billingFileName))
	if !strings.Contains(billing, "Bo Vance") {
		t.Errorf("billing.yaml = %q, want the billing of the first answer", billing)
	}
}

// Two sources that credit one person for one job make one line, and a
// second job of the person makes a second line.
func TestTheBillingOfTwoSourcesIsTheirUnion(t *testing.T) {
	merged := factAnswer{Billing: []billedPerson{{Name: "Bo Vance", Department: "Camera", Job: "Camera Operator"}}}
	second := providerAnswer{answer: factAnswer{Billing: []billedPerson{
		{Name: " Bo Vance ", Department: "Camera", Job: "Camera Operator"},
		{Name: "Bo Vance", Department: "Editing", Job: "Editor"},
		{Name: " ", Job: "Editor"},
	}}}

	mergeCredits(&merged, second, func(string) {})

	want := []billedPerson{
		{Name: "Bo Vance", Department: "Camera", Job: "Camera Operator"},
		{Name: "Bo Vance", Department: "Editing", Job: "Editor"},
	}
	if !reflect.DeepEqual(merged.Billing, want) {
		t.Errorf("billing = %+v, want %+v", merged.Billing, want)
	}
}

// A volume that refuses the write is logged, and the credits fact goes on.
func TestABillingTheVolumeRefusesIsLogged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	root := t.TempDir()
	work, log := testEnricher(t, libraryKindMovies, root, nil)
	folder := titleFolder(t, root, "The Signal (2014)")
	ledgers := filepath.Join(folder, likenDirectory)
	if err := os.MkdirAll(ledgers, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ledgers, 0o755) })

	work.writeBilling(folder, []billedPerson{{Name: "Bo Vance"}})

	if !strings.Contains(log.String(), "could not write the billing") {
		t.Errorf("log = %q, want the refused write", log.String())
	}
}
