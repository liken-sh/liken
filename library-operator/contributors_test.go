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
		{name: "a plain name", person: "Tom Hanks", ids: providerIDs{"tmdb": "31"}, want: ".contributors/t/tom-hanks"},
		{name: "an accent folds to ASCII", person: "Penélope Cruz", ids: nil, want: ".contributors/p/penelope-cruz"},
		{name: "a punctuated name", person: "Joseph Gordon-Levitt, Jr.", ids: nil,
			want: ".contributors/j/joseph-gordon-levitt-jr"},
		{name: "a name that folds away keeps the id", person: "宮崎 駿",
			ids: providerIDs{"tmdb": "608"}, want: ".contributors/t/tmdb-608"},
		{name: "a name that folds away with no id has no directory", person: "宮崎 駿", ids: nil, want: ""},
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

	work.writeCredits(folder, []creditedActor{
		{Name: "Tom Hanks", Role: "The Captain", Order: 0, IDs: providerIDs{"tmdb": "31"}},
		{Name: "Sigourney Weaver", Order: 1, IDs: providerIDs{"tmdb": "10205"}},
	})

	ledger := artLedger(t, folder, factCredits)
	want := []creditEntry{
		{Name: "Tom Hanks", Role: "The Captain", Order: 0, Contributor: ".contributors/t/tom-hanks"},
		{Name: "Sigourney Weaver", Order: 1, Contributor: ".contributors/s/sigourney-weaver"},
	}
	if len(ledger.Credits) != len(want) {
		t.Fatalf("credits = %+v, want one entry per credited person", ledger.Credits)
	}
	for at, entry := range want {
		if ledger.Credits[at] != entry {
			t.Errorf("credit %d = %+v, want %+v", at, ledger.Credits[at], entry)
		}
	}
	entry := readFileString(t, filepath.Join(root, ".contributors/t/tom-hanks", contributorFileName))
	if entry != "name: Tom Hanks\nids: {tmdb: 31}\n" {
		t.Errorf("contributor.yaml = %q, want the name and the ids the provider gave", entry)
	}
}

// Two people of one name are told apart by the id in the second one's slug,
// and the entry the first one wrote is left as it is.
func TestASecondPersonOfOneNameTakesTheIDSuffix(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, nil)

	work.writeCredits(titleFolder(t, root, "One Film (1999)"),
		[]creditedActor{{Name: "Tom Hanks", IDs: providerIDs{"tmdb": "31"}}})
	work.writeCredits(titleFolder(t, root, "Another Film (2001)"),
		[]creditedActor{{Name: "Tom Hanks", Role: "The Cook", IDs: providerIDs{"tmdb": "992"}}})

	second := artLedger(t, filepath.Join(root, "Another Film (2001)"), factCredits)
	if len(second.Credits) != 1 || second.Credits[0].Contributor != ".contributors/t/tom-hanks-tmdb-992" {
		t.Fatalf("credits = %+v, want the slug with the id of the second person", second.Credits)
	}
	first := readFileString(t, filepath.Join(root, ".contributors/t/tom-hanks", contributorFileName))
	if !strings.Contains(first, "tmdb: 31") {
		t.Errorf("contributor.yaml = %q, want the entry of the first person, unchanged", first)
	}
	other := readFileString(t, filepath.Join(root, ".contributors/t/tom-hanks-tmdb-992", contributorFileName))
	if !strings.Contains(other, "tmdb: 992") {
		t.Errorf("contributor.yaml = %q, want the entry of the second person", other)
	}
}

// A person credited on two titles is written once, and the second title's
// credits name the entry the first title created.
func TestOnePersonOnTwoTitlesIsWrittenOnce(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	entry := filepath.Join(root, ".contributors/t/tom-hanks", contributorFileName)

	work.writeCredits(titleFolder(t, root, "One Film (1999)"),
		[]creditedActor{{Name: "Tom Hanks", Role: "The Captain", IDs: providerIDs{"tmdb": "31"}}})
	first, err := os.Stat(entry)
	if err != nil {
		t.Fatal(err)
	}
	work.writeCredits(titleFolder(t, root, "Another Film (2001)"),
		[]creditedActor{{Name: "Tom Hanks", Role: "The Cook", IDs: providerIDs{"tmdb": "31"}}})

	second, err := os.Stat(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !second.ModTime().Equal(first.ModTime()) {
		t.Error("the second title wrote the entry again, want the one the first title created")
	}
	credits := artLedger(t, filepath.Join(root, "Another Film (2001)"), factCredits)
	if len(credits.Credits) != 1 || credits.Credits[0].Contributor != ".contributors/t/tom-hanks" {
		t.Errorf("credits = %+v, want the entry the first title created", credits.Credits)
	}
	people, err := os.ReadDir(filepath.Join(root, ".contributors/t"))
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
	writeFile(t, filepath.Join(root, ".contributors/t/tom-hanks", contributorFileName), "name: Tom Hanks\n")

	work.writeCredits(titleFolder(t, root, "One Film (1999)"),
		[]creditedActor{{Name: "Tom Hanks", IDs: providerIDs{"tmdb": "31"}}})

	if got := readFileString(t, filepath.Join(root, ".contributors/t/tom-hanks", contributorFileName)); got != "name: Tom Hanks\n" {
		t.Errorf("contributor.yaml = %q, want the file the person wrote", got)
	}
	credits := artLedger(t, filepath.Join(root, "One Film (1999)"), factCredits)
	if len(credits.Credits) != 1 || credits.Credits[0].Contributor != ".contributors/t/tom-hanks" {
		t.Errorf("credits = %+v, want the entry that was already there", credits.Credits)
	}
}

// A person the store cannot name still holds a credit with a name and a part,
// so the title's own list is whole.
func TestAPersonWithNoSlugStillHoldsACredit(t *testing.T) {
	root := t.TempDir()
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	folder := titleFolder(t, root, "One Film (1999)")

	work.writeCredits(folder, []creditedActor{{Name: "宮崎 駿", Role: "Himself"}})

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
		[]creditedActor{{Name: "Tom Hanks", IDs: providerIDs{"tmdb": "31"}}})

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
	writeFile(t, filepath.Join(root, ".contributors/t/tom-hanks", contributorFileName),
		"name: Tom Hanks\nids: {tmdb: 31}\n")
	writeFile(t, filepath.Join(root, ".contributors/t/tom-hanks-tmdb-992", contributorFileName),
		"name: Tom Hanks\nids: {tmdb: 1}\n")

	work.writeCredits(titleFolder(t, root, "One Film (1999)"),
		[]creditedActor{{Name: "Tom Hanks", IDs: providerIDs{"tmdb": "992"}}})

	credits := artLedger(t, filepath.Join(root, "One Film (1999)"), factCredits)
	if len(credits.Credits) != 1 || credits.Credits[0].Contributor != "" {
		t.Errorf("credits = %+v, want the credit with no directory", credits.Credits)
	}
}
