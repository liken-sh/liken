package main

// What these tests read: the slug that names a person's directory, the two
// files the credits fact writes, and that a person credited on two titles is
// written once.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The folder of a title the credits fact has reached, made before the fact
// writes into it, the way the walk found it on the volume.
func titleFolder(t *testing.T, root, name string) string {
	t.Helper()
	folder := filepath.Join(root, name)
	if err := os.MkdirAll(folder, volumeDirectoryPerm); err != nil {
		t.Fatal(err)
	}
	return folder
}

func TestTheSlugThatNamesAPersonsDirectory(t *testing.T) {
	cases := []struct {
		name   string
		person string
		ids    providerIDs
		want   string
	}{
		{name: "a plain name", person: "Oren Tally", ids: providerIDs{"tmdb": "9031"}, want: ".contributors/or/oren-tally"},
		{name: "an accent folds to ASCII", person: "Renée Solberg", ids: nil, want: ".contributors/re/renee-solberg"},
		{name: "a punctuated name", person: "Tobias Arden-Wyle, Jr.", ids: nil,
			want: ".contributors/to/tobias-arden-wyle-jr"},
		{name: "a name that folds away keeps the id", person: "山田 花子",
			ids: providerIDs{"tmdb": "9608"}, want: ".contributors/tm/tmdb-9608"},
		{name: "a name that folds away with no id has no directory", person: "山田 花子", ids: nil, want: ""},
		{name: "a one-character slug is its own bucket", person: "Q", ids: nil, want: ".contributors/q/q"},
		{name: "a hyphen in the second place stays", person: "J Ro", ids: nil, want: ".contributors/j-/j-ro"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := contributorDirectory(contributorSlug(test.person, test.ids)); got != test.want {
				t.Errorf("directory = %q, want %q", got, test.want)
			}
		})
	}
}

// The credits fact writes the list of credits in its own ledger file and the
// entry of each person under .contributors/, in the shapes a person reads them
// in.
func TestTheCreditsFactWritesTheCreditsAndTheEntries(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	folder := titleFolder(t, root, "The Signal (2014)")

	work.writeCredits(folder, factAnswer{Cast: []creditedActor{
		{Name: "Oren Tally", Role: "The Captain", Order: 0, IDs: providerIDs{"tmdb": "9031"}},
		{Name: "Nora Vance", Order: 1, IDs: providerIDs{"tmdb": "9205"}},
	}})

	ledger := artLedger(t, folder, factCredits)
	want := []creditEntry{
		{Name: "Oren Tally", Part: creditPartActor, Role: "The Captain", Order: 0,
			Contributor: ".contributors/or/oren-tally"},
		{Name: "Nora Vance", Part: creditPartActor, Order: 1,
			Contributor: ".contributors/no/nora-vance"},
	}
	if len(ledger.Credits) != len(want) {
		t.Fatalf("credits = %+v, want one entry per credited person", ledger.Credits)
	}
	for at, entry := range want {
		if ledger.Credits[at] != entry {
			t.Errorf("credit %d = %+v, want %+v", at, ledger.Credits[at], entry)
		}
	}
	entry := readFileString(t, filepath.Join(root, ".contributors/or/oren-tally", contributorFileName))
	if entry != "name: Oren Tally\nids: {tmdb: 9031}\n" {
		t.Errorf("contributor.yaml = %q, want the name and the ids the provider gave", entry)
	}
}

// Two people of one name are told apart by the id in the second one's slug,
// and the entry the first one wrote is left as it is.
func TestASecondPersonOfOneNameTakesTheIDSuffix(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, nil)

	work.writeCredits(titleFolder(t, root, "One Film (1999)"),
		factAnswer{Cast: []creditedActor{{Name: "Oren Tally", IDs: providerIDs{"tmdb": "9031"}}}})
	work.writeCredits(titleFolder(t, root, "Another Film (2001)"),
		factAnswer{Cast: []creditedActor{{Name: "Oren Tally", Role: "The Cook", IDs: providerIDs{"tmdb": "992"}}}})

	second := artLedger(t, filepath.Join(root, "Another Film (2001)"), factCredits)
	if len(second.Credits) != 1 || second.Credits[0].Contributor != ".contributors/or/oren-tally-tmdb-992" {
		t.Fatalf("credits = %+v, want the slug with the id of the second person", second.Credits)
	}
	first := readFileString(t, filepath.Join(root, ".contributors/or/oren-tally", contributorFileName))
	if !strings.Contains(first, "tmdb: 9031") {
		t.Errorf("contributor.yaml = %q, want the entry of the first person, unchanged", first)
	}
	other := readFileString(t, filepath.Join(root, ".contributors/or/oren-tally-tmdb-992", contributorFileName))
	if !strings.Contains(other, "tmdb: 992") {
		t.Errorf("contributor.yaml = %q, want the entry of the second person", other)
	}
}

// A person credited on two titles is written once, and the second title's
// credits name the entry the first title created.
func TestOnePersonOnTwoTitlesIsWrittenOnce(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	entry := filepath.Join(root, ".contributors/or/oren-tally", contributorFileName)

	work.writeCredits(titleFolder(t, root, "One Film (1999)"),
		factAnswer{Cast: []creditedActor{{Name: "Oren Tally", Role: "The Captain", IDs: providerIDs{"tmdb": "9031"}}}})
	first, err := os.Stat(entry)
	if err != nil {
		t.Fatal(err)
	}
	work.writeCredits(titleFolder(t, root, "Another Film (2001)"),
		factAnswer{Cast: []creditedActor{{Name: "Oren Tally", Role: "The Cook", IDs: providerIDs{"tmdb": "9031"}}}})

	second, err := os.Stat(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !second.ModTime().Equal(first.ModTime()) {
		t.Error("the second title wrote the entry again, want the one the first title created")
	}
	credits := artLedger(t, filepath.Join(root, "Another Film (2001)"), factCredits)
	if len(credits.Credits) != 1 || credits.Credits[0].Contributor != ".contributors/or/oren-tally" {
		t.Errorf("credits = %+v, want the entry the first title created", credits.Credits)
	}
	people, err := os.ReadDir(filepath.Join(root, ".contributors/or"))
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 1 {
		t.Errorf("the store holds %d directories, want the one person", len(people))
	}
}

// An entry a person wrote by hand, with no id in it, is the person the credit
// names, so the store holds one directory and never two.
func TestAnEntryWithNoIDIsTheSamePerson(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	writeFile(t, filepath.Join(root, ".contributors/or/oren-tally", contributorFileName), "name: Oren Tally\n")

	work.writeCredits(titleFolder(t, root, "One Film (1999)"),
		factAnswer{Cast: []creditedActor{{Name: "Oren Tally", IDs: providerIDs{"tmdb": "9031"}}}})

	if got := readFileString(t, filepath.Join(root, ".contributors/or/oren-tally", contributorFileName)); got != "name: Oren Tally\n" {
		t.Errorf("contributor.yaml = %q, want the file the person wrote", got)
	}
	credits := artLedger(t, filepath.Join(root, "One Film (1999)"), factCredits)
	if len(credits.Credits) != 1 || credits.Credits[0].Contributor != ".contributors/or/oren-tally" {
		t.Errorf("credits = %+v, want the entry that was already there", credits.Credits)
	}
}

// A person the store cannot name still holds a credit with a name and a part,
// so the title's own list is whole.
func TestAPersonWithNoSlugStillHoldsACredit(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	folder := titleFolder(t, root, "One Film (1999)")

	work.writeCredits(folder, factAnswer{Cast: []creditedActor{{Name: "山田 花子", Role: "Himself"}}})

	credits := artLedger(t, folder, factCredits)
	if len(credits.Credits) != 1 || credits.Credits[0].Contributor != "" || credits.Credits[0].Role != "Himself" {
		t.Errorf("credits = %+v, want the credit with no entry", credits.Credits)
	}
	if _, err := os.Stat(filepath.Join(root, contributorsDirectory)); err == nil {
		t.Error("the fact created the store, want no directory for a person it cannot name")
	}
}

// A volume that refuses the entry leaves the credits with no directory and
// says so, because the credits of a title stand whether the store took the
// person or not.
func TestAStoreTheVolumeRefusesLeavesTheCreditsAlone(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-root")
	writeFile(t, root, "a file where the library root should be")
	work, log := testEnricher(t, libraryKindMovies, root, nil)

	work.writeCredits(filepath.Join(root, "One Film (1999)"),
		factAnswer{Cast: []creditedActor{{Name: "Oren Tally", IDs: providerIDs{"tmdb": "9031"}}}})

	if !strings.Contains(log.String(), "could not write the credits") {
		t.Errorf("log = %q, want the line that names the credits it could not write", log.String())
	}
}

// An entry a person edited into a conflict, where both the plain slug and the
// slug with the id name another person, leaves the credit with no directory
// rather than writing over either one.
func TestACreditWithNoFreeSlugHoldsNoDirectory(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	writeFile(t, filepath.Join(root, ".contributors/or/oren-tally", contributorFileName),
		"name: Oren Tally\nids: {tmdb: 9031}\n")
	writeFile(t, filepath.Join(root, ".contributors/or/oren-tally-tmdb-992", contributorFileName),
		"name: Oren Tally\nids: {tmdb: 1}\n")

	work.writeCredits(titleFolder(t, root, "One Film (1999)"),
		factAnswer{Cast: []creditedActor{{Name: "Oren Tally", IDs: providerIDs{"tmdb": "992"}}}})

	credits := artLedger(t, filepath.Join(root, "One Film (1999)"), factCredits)
	if len(credits.Credits) != 1 || credits.Credits[0].Contributor != "" {
		t.Errorf("credits = %+v, want the credit with no directory", credits.Credits)
	}
}
