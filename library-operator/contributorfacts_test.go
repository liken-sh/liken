package main

// What these tests read: what each contributor fact writes into a person's
// directory, that a file another writer left is never opened, the fight on an
// entry a person edited, the gap query of each fact against the shipped
// schema, and the container that runs the three.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const contributorLibrary = "house/movies"

// The answers the fake provider holds for one person: the person itself, the
// ids in the other databases, and the headshot.
func personAnswer(birthday, deathday, biography, profile string) string {
	return `{"name":"Tom Hanks","biography":"` + biography + `","birthday":"` + birthday +
		`","deathday":"` + deathday + `","profile_path":"` + profile + `"}`
}

// The client the contributor tests run against, with the image host pointed at
// the fake itself, so a headshot in a test reaches no other server.
func newPersonTMDb(t *testing.T, answers map[string]string) (*tmdbClient, *fakeTMDb) {
	t.Helper()
	client, fake := newFakeTMDb(t, answers)
	held := tmdbImageBase
	tmdbImageBase = client.base + "/t/p/"
	t.Cleanup(func() { tmdbImageBase = held })
	return client, fake
}

// One person's directory on the volume, with the entry the credits fact would
// have created.
func seedEntry(t *testing.T, root, slug, entry string) string {
	t.Helper()
	folder := filepath.Join(root, contributorDirectory(slug))
	writeFile(t, filepath.Join(folder, contributorFileName), entry)
	return folder
}

func TestTheIDsFactFillsTheEntry(t *testing.T) {
	root := t.TempDir()
	folder := seedEntry(t, root, "tom-hanks", "name: Tom Hanks\nids: {tmdb: 31}\n")
	work, log := testEnricher(t, libraryKindMovies, root, nil)
	client, _ := newPersonTMDb(t, map[string]string{
		tmdbKey("/3/person/31", "", ""):              personAnswer("1956-07-09", "", "An actor.", "/hanks.jpg"),
		tmdbKey("/3/person/31/external_ids", "", ""): `{"imdb_id":"nm0000158","wikidata_id":"Q2263"}`,
	})

	if !work.fillContributorIDs(t.Context(), client, folder, contributorGap{path: contributorDirectory("tom-hanks"), tmdb: "31"}) {
		t.Fatalf("the fact wrote nothing, log = %q", log.String())
	}

	entry := readFileString(t, filepath.Join(folder, contributorFileName))
	want := "name: Tom Hanks\nids: {imdb: nm0000158, tmdb: 31, wikidata: Q2263}\nborn: \"1956-07-09\"\n"
	if entry != want {
		t.Errorf("contributor.yaml = %q, want %q", entry, want)
	}
	ledger := artLedger(t, folder, factContributorIDs)
	if len(ledger.Items) != 1 || ledger.Items[0].Wrote != contentHash([]byte(entry)) {
		t.Errorf("ledger items = %+v, want the hash of the file the fact left", ledger.Items)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("ledger attempts = %+v, want one that found the ids", ledger.Attempts)
	}
}

// An entry a person edited by hand is a fight: the fact leaves the file and
// records it, so the next run of the reporter counts it.
func TestAnEditedEntryIsAFight(t *testing.T) {
	root := t.TempDir()
	folder := seedEntry(t, root, "tom-hanks", "name: Tom Hanks\nids: {tmdb: 31}\n")
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	gap := contributorGap{path: contributorDirectory("tom-hanks"), tmdb: "31"}
	client, fake := newPersonTMDb(t, map[string]string{
		tmdbKey("/3/person/31", "", ""):              personAnswer("1956-07-09", "", "An actor.", ""),
		tmdbKey("/3/person/31/external_ids", "", ""): `{"imdb_id":"nm0000158"}`,
	})
	if !work.fillContributorIDs(t.Context(), client, folder, gap) {
		t.Fatal("the first run wrote nothing, want the entry")
	}
	held := "name: Tom Hanks\nids: {tmdb: 31}\nborn: \"1900-01-01\"\n"
	writeFile(t, filepath.Join(folder, contributorFileName), held)
	served := fake.served[tmdbKey("/3/person/31", "", "")]

	if work.fillContributorIDs(t.Context(), client, folder, gap) {
		t.Error("the fact wrote over an entry a person edited")
	}

	if got := readFileString(t, filepath.Join(folder, contributorFileName)); got != held {
		t.Errorf("contributor.yaml = %q, want the bytes the person wrote", got)
	}
	if fake.served[tmdbKey("/3/person/31", "", "")] != served {
		t.Error("the fact asked the provider about a person it had already lost")
	}
	ledger := artLedger(t, folder, factContributorIDs)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFight {
		t.Errorf("ledger attempts = %+v, want the fight", ledger.Attempts)
	}
}

// The biography and the headshot each land where no file of that name exists,
// and each records the provider that answered.
func TestTheBiographyAndTheHeadshotLandWhereNoneExists(t *testing.T) {
	cases := []struct {
		fact string
		file string
		want string
		fill func(work *enricher, client *tmdbClient, folder string, gap contributorGap) bool
	}{
		{
			fact: factContributorBiography, file: contributorBiographyName, want: "An actor.\n",
			fill: func(work *enricher, client *tmdbClient, folder string, gap contributorGap) bool {
				return work.fillContributorBiography(t.Context(), client, folder, gap)
			},
		},
		{
			fact: factContributorHeadshot, file: contributorHeadshotName, want: testImage,
			fill: func(work *enricher, client *tmdbClient, folder string, gap contributorGap) bool {
				return work.fillContributorHeadshot(t.Context(), client, folder, gap)
			},
		},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			root := t.TempDir()
			folder := seedEntry(t, root, "tom-hanks", "name: Tom Hanks\n")
			work, log := testEnricher(t, libraryKindMovies, root, nil)
			client, _ := newPersonTMDb(t, map[string]string{
				tmdbKey("/3/person/31", "", ""):                        personAnswer("", "", "An actor.", "/hanks.jpg"),
				tmdbKey("/t/p/"+tmdbHeadshotSize+"/hanks.jpg", "", ""): testImage,
				tmdbKey("/3/person/31/external_ids", "", ""):           `{}`,
			})

			if !test.fill(work, client, folder, contributorGap{path: contributorDirectory("tom-hanks"), tmdb: "31"}) {
				t.Fatalf("the fact wrote nothing, log = %q", log.String())
			}

			if got := readFileString(t, filepath.Join(folder, test.file)); got != test.want {
				t.Errorf("%s = %q, want %q", test.file, got, test.want)
			}
			ledger := artLedger(t, folder, test.fact)
			if len(ledger.Items) != 1 || !ledger.Items[0].Provider.is(providerBlockTMDb) {
				t.Errorf("ledger items = %+v, want the provider that answered", ledger.Items)
			}
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
				t.Errorf("ledger attempts = %+v, want one that found the file", ledger.Attempts)
			}
		})
	}
}

// A file another writer left is never opened and never asked about, and the
// ledger records that it was already there.
func TestAFileBesideAnEntryIsLeftAsItIs(t *testing.T) {
	cases := []struct {
		fact string
		file string
		fill func(work *enricher, client *tmdbClient, folder string, gap contributorGap) bool
	}{
		{
			fact: factContributorBiography, file: contributorBiographyName,
			fill: func(work *enricher, client *tmdbClient, folder string, gap contributorGap) bool {
				return work.fillContributorBiography(t.Context(), client, folder, gap)
			},
		},
		{
			fact: factContributorHeadshot, file: contributorHeadshotName,
			fill: func(work *enricher, client *tmdbClient, folder string, gap contributorGap) bool {
				return work.fillContributorHeadshot(t.Context(), client, folder, gap)
			},
		},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			root := t.TempDir()
			folder := seedEntry(t, root, "tom-hanks", "name: Tom Hanks\n")
			writeFile(t, filepath.Join(folder, test.file), "the file a person kept")
			work, _ := testEnricher(t, libraryKindMovies, root, nil)
			client, fake := newPersonTMDb(t, map[string]string{
				tmdbKey("/3/person/31", "", ""): personAnswer("", "", "An actor.", "/hanks.jpg"),
			})

			if test.fill(work, client, folder, contributorGap{path: contributorDirectory("tom-hanks"), tmdb: "31"}) {
				t.Error("the fact reported a write, want none")
			}

			if got := readFileString(t, filepath.Join(folder, test.file)); got != "the file a person kept" {
				t.Errorf("%s = %q, want the bytes that were there", test.file, got)
			}
			if fake.served[tmdbKey("/3/person/31", "", "")] != 0 {
				t.Error("the fact asked the provider about a person whose file is already there")
			}
			ledger := artLedger(t, folder, test.fact)
			if len(ledger.Items) != 1 || !ledger.Items[0].Provider.is(artProviderExisting) {
				t.Errorf("ledger items = %+v, want the entry that says the file was already there", ledger.Items)
			}
		})
	}
}

// A provider that holds nothing for a person leaves a miss with a date, so the
// fact asks again only after the retry interval.
func TestAPersonTheProviderHoldsNothingForIsAMiss(t *testing.T) {
	root := t.TempDir()
	folder := seedEntry(t, root, "tom-hanks", "name: Tom Hanks\n")
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	client, _ := newPersonTMDb(t, map[string]string{
		tmdbKey("/3/person/31", "", ""):              personAnswer("", "", "", ""),
		tmdbKey("/3/person/31/external_ids", "", ""): `{}`,
	})
	gap := contributorGap{path: contributorDirectory("tom-hanks"), tmdb: "31"}

	for _, fact := range contributorFactNames {
		if work.fillContributor(t.Context(), client, fact, gap) {
			t.Errorf("the %s fact reported a write, want none", fact)
		}
		ledger := artLedger(t, folder, fact)
		if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptNothing {
			t.Errorf("%s attempts = %+v, want the miss", fact, ledger.Attempts)
		}
	}
}

// One person with the two files beside the entry, and one with neither, which
// is the shape of every contributor gap.
func seedContributors(t *testing.T, catalog *Catalog, rows ...contributorRow) {
	t.Helper()
	seed := &walkResult{contributors: rows}
	for _, row := range rows {
		seed.contributorAliases = append(seed.contributorAliases, contributorAliasRow{
			Library: row.Library, Scheme: contributorTMDbScheme,
			ID: strings.TrimPrefix(filepath.Base(row.Path), "person-"), Path: row.Path,
		})
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

// Each fact reads its own gap out of the catalog: a person with no birth date
// and no id but TMDb's own, a person with no biography file, and a person with
// no headshot file.
func TestEachContributorFactReadsItsOwnGap(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	filled := contributorRow{
		Library: contributorLibrary, Path: ".contributors/p/person-1", Name: "One",
		Born: "1956-07-09", Biography: true, Headshot: true,
	}
	empty := contributorRow{Library: contributorLibrary, Path: ".contributors/p/person-2", Name: "Two"}
	seedContributors(t, catalog, filled, empty)
	if _, err := catalog.UpsertContributorAliases(t.Context(), []contributorAliasRow{{
		Library: contributorLibrary, Scheme: "imdb", ID: "nm0000158", Path: filled.Path,
	}}); err != nil {
		t.Fatal(err)
	}

	for _, fact := range contributorFactNames {
		gaps, err := catalog.contributorGaps(t.Context(), contributorLibrary, fact, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if len(gaps) != 1 || gaps[0].path != empty.Path || gaps[0].tmdb != "2" {
			t.Errorf("%s gaps = %+v, want the person who lacks it", fact, gaps)
		}
	}
}

// A person tried inside the retry window is no gap, so a miss is a fact with a
// date and never a person the enricher opens every night.
func TestAPersonTriedInsideTheWindowIsNoGap(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	person := contributorRow{Library: contributorLibrary, Path: ".contributors/p/person-2", Name: "Two"}
	seedContributors(t, catalog, person)
	if _, err := catalog.UpsertAttempts(t.Context(), []attemptRow{{
		Library: contributorLibrary, Item: person.Path, Fact: factContributorHeadshot,
		At: time.Now().UTC().Unix(), Result: attemptNothing,
	}}); err != nil {
		t.Fatal(err)
	}

	gaps, err := catalog.contributorGaps(t.Context(), contributorLibrary, factContributorHeadshot, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 {
		t.Errorf("gaps = %+v, want none inside the window", gaps)
	}
}

// The whole loop of one fact, from the gap in the catalog to the file on the
// volume, and a container with no key that fails before it writes.
func TestTheContributorFactRunsItsWholeGap(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedContributors(t, catalog, contributorRow{
		Library: contributorLibrary, Path: ".contributors/p/person-31", Name: "Tom Hanks",
	})
	folder := filepath.Join(root, ".contributors/p/person-31")
	writeFile(t, filepath.Join(folder, contributorFileName), "name: Tom Hanks\n")
	work, log := testEnricher(t, libraryKindMovies, root, catalog)
	client, _ := newPersonTMDb(t, map[string]string{
		tmdbKey("/3/person/31", "", ""): personAnswer("", "", "An actor.", ""),
	})

	if err := work.contributorGap(t.Context(), factContributorBiography, client); err != nil {
		t.Fatal(err)
	}

	if got := readFileString(t, filepath.Join(folder, contributorBiographyName)); got != "An actor.\n" {
		t.Errorf("biography.txt = %q, want the text the provider holds", got)
	}
	if !strings.Contains(log.String(), "wrote the "+factContributorBiography) {
		t.Errorf("log = %q, want the line that counts the people", log.String())
	}
}

func TestTheContributorFactNeedsAProviderKey(t *testing.T) {
	t.Setenv(tmdbTokenVariable, "")
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), nil)

	if err := work.contributorFact(t.Context(), factContributorIDs); err == nil {
		t.Error("the fact reported no error, want one")
	}
}

// The contributors container stands only where the Library's sources serve one
// of the people facts, and it names all three.
func TestTheContributorsContainerStandsWhereASourceServesAPeopleFact(t *testing.T) {
	cases := []struct {
		name  string
		facts []string
		want  string
	}{
		{name: "a provider that serves the art alone", facts: []string{factPoster}},
		{
			name:  "a provider that serves the people",
			facts: nil,
			want:  "contributor.ids,contributor.biography,contributor.headshot",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house", test.facts...))

			var people *Container
			for at, container := range job.Spec.Template.Spec.InitContainers {
				if container.Name == contributorsContainerName {
					people = &job.Spec.Template.Spec.InitContainers[at]
				}
			}
			if test.want == "" {
				if people != nil {
					t.Fatal("the pod holds a contributors container, want none")
				}
				return
			}
			if people == nil {
				t.Fatalf("the pod holds no contributors container, initContainers = %+v",
					job.Spec.Template.Spec.InitContainers)
			}
			if got := containerEnvironment(*people)[libraryFactsVariable]; got != test.want {
				t.Errorf("%s = %q, want %q", libraryFactsVariable, got, test.want)
			}
		})
	}
}

// A provider that refuses a person, and an entry that left the volume between
// the walk and the run, leave an error attempt, so the next run tries again.
func TestWhatAContributorFactRecordsWhenItWritesNothing(t *testing.T) {
	person := tmdbKey("/3/person/31", "", "")
	ids := tmdbKey("/3/person/31/external_ids", "", "")
	headshot := tmdbKey("/t/p/"+tmdbHeadshotSize+"/hanks.jpg", "", "")
	cases := []struct {
		name     string
		fact     string
		entry    string
		statuses map[string]int
	}{
		{
			name: "the provider refuses the person", fact: factContributorIDs,
			entry: "name: Tom Hanks\n", statuses: map[string]int{person: http.StatusUnauthorized},
		},
		{
			name: "the provider refuses the ids", fact: factContributorIDs,
			entry: "name: Tom Hanks\n", statuses: map[string]int{ids: http.StatusUnauthorized},
		},
		{
			name: "the entry cannot be read", fact: factContributorIDs, entry: "name: [Tom Hanks\n",
		},
		{
			name: "the provider refuses the biography", fact: factContributorBiography,
			entry: "name: Tom Hanks\n", statuses: map[string]int{person: http.StatusInternalServerError},
		},
		{
			name: "the provider refuses the headshot", fact: factContributorHeadshot,
			entry: "name: Tom Hanks\n", statuses: map[string]int{person: http.StatusInternalServerError},
		},
		{
			name: "the download fails", fact: factContributorHeadshot,
			entry: "name: Tom Hanks\n", statuses: map[string]int{headshot: http.StatusInternalServerError},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			folder := seedEntry(t, root, "tom-hanks", test.entry)
			work, _ := testEnricher(t, libraryKindMovies, root, nil)
			client, fake := newPersonTMDb(t, map[string]string{
				person:   personAnswer("1956-07-09", "", "An actor.", "/hanks.jpg"),
				ids:      `{"imdb_id":"nm0000158"}`,
				headshot: testImage,
			})
			for key, status := range test.statuses {
				fake.statuses[key] = status
			}

			if work.fillContributor(t.Context(), client, test.fact, contributorGap{
				path: contributorDirectory("tom-hanks"), tmdb: "31",
			}) {
				t.Error("the fact reported a write, want none")
			}

			ledger := artLedger(t, folder, test.fact)
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
				t.Errorf("ledger attempts = %+v, want an error", ledger.Attempts)
			}
		})
	}
}

// An entry the provider's answer does not change is left as it is, and the
// ledger still records the provider that answered.
func TestAnUnchangedEntryIsNotWrittenAgain(t *testing.T) {
	root := t.TempDir()
	folder := seedEntry(t, root, "tom-hanks", "name: Tom Hanks\n")
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	gap := contributorGap{path: contributorDirectory("tom-hanks"), tmdb: "31"}
	client, _ := newPersonTMDb(t, map[string]string{
		tmdbKey("/3/person/31", "", ""):              personAnswer("1956-07-09", "", "", ""),
		tmdbKey("/3/person/31/external_ids", "", ""): `{"imdb_id":"nm0000158"}`,
	})
	if !work.fillContributorIDs(t.Context(), client, folder, gap) {
		t.Fatal("the first run wrote nothing, want the entry")
	}
	entry := filepath.Join(folder, contributorFileName)
	first, err := os.Stat(entry)
	if err != nil {
		t.Fatal(err)
	}

	if work.fillContributorIDs(t.Context(), client, folder, gap) {
		t.Error("the second run reported a write, want none")
	}

	second, err := os.Stat(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !second.ModTime().Equal(first.ModTime()) {
		t.Error("the fact wrote the entry again, want the file it had already left")
	}
	ledger := artLedger(t, folder, factContributorIDs)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("ledger attempts = %+v, want the answer it found", ledger.Attempts)
	}
}

// An entry that is not on the volume is an error attempt and never a write,
// because the row the gap read names a file a walk will remove.
func TestAnEntryThatLeftTheVolumeIsAnErrorAttempt(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, contributorDirectory("tom-hanks"))
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	client, fake := newPersonTMDb(t, map[string]string{})

	if work.fillContributorIDs(t.Context(), client, folder, contributorGap{
		path: contributorDirectory("tom-hanks"), tmdb: "31",
	}) {
		t.Error("the fact reported a write, want none")
	}

	if len(fake.requestPath) != 0 {
		t.Errorf("the fact made %v, want no call", fake.requestPath)
	}
}

// A Job narrowed to one folder leaves the people of the library alone, because
// the store sits at the root and no folder holds it.
func TestAContributorOutsideTheScopeIsLeftAlone(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedContributors(t, catalog, contributorRow{
		Library: contributorLibrary, Path: ".contributors/p/person-31", Name: "Tom Hanks",
	})
	writeFile(t, filepath.Join(root, ".contributors/p/person-31", contributorFileName), "name: Tom Hanks\n")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.scope = "The Signal (2014)"
	client, fake := newPersonTMDb(t, map[string]string{
		tmdbKey("/3/person/31", "", ""): personAnswer("", "", "An actor.", ""),
	})

	if err := work.contributorGap(t.Context(), factContributorBiography, client); err != nil {
		t.Fatal(err)
	}

	if len(fake.requestPath) != 0 {
		t.Errorf("the fact made %v, want no call outside its own folder", fake.requestPath)
	}
}

// A catalog that cannot be read ends the container, because the gap list is
// the work.
func TestAContributorFactEndsWhenTheCatalogFails(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(),
		NewCatalog("http://127.0.0.1:1", http.DefaultClient))
	client, _ := newPersonTMDb(t, map[string]string{})

	if err := work.contributorGap(t.Context(), factContributorIDs, client); err == nil {
		t.Error("the fact reported no error, want one")
	}
}

// The fact map runs each contributor fact the container names, with the key
// the pod carries, and a library with no gap ends with no call.
func TestTheFactMapRunsTheContributorFacts(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	t.Setenv(tmdbTokenVariable, "the-key")

	for _, fact := range contributorFactNames {
		if err := factRuns[fact](t.Context(), work); err != nil {
			t.Fatalf("the %s fact failed: %v", fact, err)
		}
	}
}

// A directory the write door cannot reach is an error attempt, and the next
// run tries again.
func TestAPersonsDirectoryTheVolumeRefusesIsAnErrorAttempt(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "not-a-directory")
	writeFile(t, folder, "a file where the directory should be")
	work, log := testEnricher(t, libraryKindMovies, root, nil)
	client, _ := newPersonTMDb(t, map[string]string{
		tmdbKey("/3/person/31", "", ""): personAnswer("", "", "An actor.", ""),
	})
	gap := contributorGap{path: "not-a-directory", tmdb: "31"}

	if work.fillContributorBiography(t.Context(), client, folder, gap) {
		t.Error("the fact reported a write into a path that is not a directory")
	}
	if work.createContributorFile(folder, contributorBiographyName, factContributorBiography, gap, []byte("An actor.")) {
		t.Error("the create reported a write into a path that is not a directory")
	}

	if !strings.Contains(log.String(), "could not") {
		t.Errorf("log = %q, want the lines that name what could not be written", log.String())
	}
}

// A file that arrived between the read and the write is kept, and the ledger
// records that it was already there.
func TestAFileThatArrivedBeforeTheWriteIsKept(t *testing.T) {
	root := t.TempDir()
	folder := seedEntry(t, root, "tom-hanks", "name: Tom Hanks\n")
	writeFile(t, filepath.Join(folder, contributorBiographyName), "the file a person kept")
	work, _ := testEnricher(t, libraryKindMovies, root, nil)

	if work.createContributorFile(folder, contributorBiographyName, factContributorBiography,
		contributorGap{path: contributorDirectory("tom-hanks"), tmdb: "31"}, []byte("An actor.")) {
		t.Error("the create reported a write, want none")
	}

	if got := readFileString(t, filepath.Join(folder, contributorBiographyName)); got != "the file a person kept" {
		t.Errorf("biography.txt = %q, want the bytes that were there", got)
	}
	ledger := artLedger(t, folder, factContributorBiography)
	if len(ledger.Items) != 1 || !ledger.Items[0].Provider.is(artProviderExisting) {
		t.Errorf("ledger items = %+v, want the entry that says the file was already there", ledger.Items)
	}
}

// A ledger the fact cannot read is an error attempt, so an unreadable ledger
// never reads as an entry no writer holds.
func TestALedgerThatCannotBeReadIsAnErrorAttempt(t *testing.T) {
	root := t.TempDir()
	folder := seedEntry(t, root, "tom-hanks", "name: Tom Hanks\n")
	writeFile(t, filepath.Join(folder, likenDirectory, likenLedgerName(factContributorIDs)), "items: [")
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	client, fake := newPersonTMDb(t, map[string]string{})

	if work.fillContributorIDs(t.Context(), client, folder,
		contributorGap{path: contributorDirectory("tom-hanks"), tmdb: "31"}) {
		t.Error("the fact reported a write, want none")
	}

	if len(fake.requestPath) != 0 {
		t.Errorf("the fact made %v, want no call", fake.requestPath)
	}
}

// The ids fact writes the death date the provider states, and a fact this
// image does not run writes nothing at all.
func TestTheIDsFactWritesTheDeathDate(t *testing.T) {
	root := t.TempDir()
	folder := seedEntry(t, root, "one-who-died", "name: One Who Died\n")
	work, _ := testEnricher(t, libraryKindMovies, root, nil)
	gap := contributorGap{path: contributorDirectory("one-who-died"), tmdb: "31"}
	client, _ := newPersonTMDb(t, map[string]string{
		tmdbKey("/3/person/31", "", ""):              personAnswer("1925-05-31", "2020-01-30", "", ""),
		tmdbKey("/3/person/31/external_ids", "", ""): `{"tvrage_id":4021}`,
	})

	if !work.fillContributorIDs(t.Context(), client, folder, gap) {
		t.Fatal("the fact wrote nothing, want the entry")
	}

	want := "name: One Who Died\nids: {tmdb: 31, tvrage: 4021}\nborn: \"1925-05-31\"\ndied: \"2020-01-30\"\n"
	if got := readFileString(t, filepath.Join(folder, contributorFileName)); got != want {
		t.Errorf("contributor.yaml = %q, want %q", got, want)
	}
	if work.fillContributor(t.Context(), client, factPoster, gap) {
		t.Error("the people phase ran a fact it does not hold")
	}
}
