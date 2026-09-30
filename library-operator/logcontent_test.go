package main

// These tests read the lines the Jobs and the operator write about library
// content, and check the rule logline.go states: a line names a title, a
// folder, or a person only by an opaque id, and keeps its counts and the
// cause of an error.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// wantOpaque checks one log against the rule: every word in want is in it,
// and no word in hidden is.
func wantOpaque(t *testing.T, logged string, want, hidden []string) {
	t.Helper()
	for _, word := range want {
		if !strings.Contains(logged, word) {
			t.Errorf("log = %q, want %q", logged, word)
		}
	}
	for _, word := range hidden {
		if strings.Contains(logged, word) {
			t.Errorf("log = %q, want no %q", logged, word)
		}
	}
}

// The identity fact names a title by its catalog id, which for a title with
// no provider id is the hash of its folder key, and keeps the provider's
// answer as the cause.
func TestAnIdentityLineNamesTheTitleByItsID(t *testing.T) {
	search := tmdbKey("/3/search/movie", "The Long Survey", "1982")
	cases := []struct {
		name    string
		answers map[string]string
		refuse  bool
		want    string
	}{
		{name: "an answer",
			answers: map[string]string{search: `{"results":[` + tmdbResultJSON(1101, "The Long Survey", "1982-05-14") + `]}`},
			want:    "identified " + opaqueID("movie:path:x") + " as tmdb 1101"},
		{name: "a refusal", refuse: true,
			want: "could not identify " + opaqueID("movie:path:x") + ": tmdb /3/search/movie: 401"},
		{name: "no answer", want: "no provider named " + opaqueID("movie:path:x")},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			folder := "The Long Survey (1982)"
			writeFile(t, filepath.Join(root, folder, "survey.mkv"), "video")
			seedIdentityGap(t, catalog, libraryKindMovies, folder, "1982", 0)
			work, logged := testEnricher(t, libraryKindMovies, root, catalog)
			client, fake := newFakeTMDb(t, test.answers)
			if test.refuse {
				fake.statuses[search] = http.StatusUnauthorized
			}

			if err := work.identityGap(t.Context(), client); err != nil {
				t.Fatal(err)
			}

			wantOpaque(t, logged.String(), []string{test.want}, []string{"Long Survey", root})
		})
	}
}

// A file the volume refuses names the file by the hash of its place under
// the root, in the line and in the cause the os error gives.
func TestAnArtLineNamesTheFileByItsPath(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := filepath.Join(root, "Some Film (2014)")
	work, logged := testEnricher(t, libraryKindMovies, root, catalog)
	client, _ := newArtTMDb(t, map[string]string{tmdbKey("/t/p/w780/quiet.jpg", "", ""): testImage})
	gap := artGap{key: "Some Film (2014)/poster.jpg", tmdb: "1001"}
	image := artCandidate{URL: client.base + "/t/p/w780/quiet.jpg"}

	work.writeArt(t.Context(), newTMDbArtAnswerer(client), artTypes[factPoster],
		gap, folder, filepath.Join(folder, "poster.jpg"), image, 0)

	named := opaquePath(root, "Some Film (2014)/poster.jpg")
	wantOpaque(t, logged.String(),
		[]string{"could not write " + named + ": ", "no such file or directory"},
		[]string{"Some Film", root})
}

// The merge names each entry by the hash of its path, which is the person's
// name, and keeps the parser's cause.
func TestAMergeLineNamesAnEntryByItsHash(t *testing.T) {
	work, _, root := twoEntriesOfOnePerson(t)
	logged := &bytes.Buffer{}
	work.log = logged
	other := contributorDirectory(otherSlug)
	writeFile(t, filepath.Join(root, other, contributorFileName), "name: [Oren Tally\n")

	if err := work.mergeContributors(t.Context()); err != nil {
		t.Fatal(err)
	}

	wantOpaque(t, logged.String(),
		[]string{
			"could not read " + entryNamed(other) + ": reading " +
				opaquePath(root, filepath.Join(other, contributorFileName)) + ": yaml: line 1",
			"merged 0 groups of entries",
		},
		[]string{"Oren Tally", otherSlug, root})
}

// A reason the merge keeps names the entries by path in the ledger, where a
// person reads it on the volume, and by hash in the log.
func TestAReasonNamesTheEntriesOfItsGroupByHash(t *testing.T) {
	group := []groupEntry{{path: ".contributors/ir/iris-kell"}, {path: ".contributors/ir/iris-kell-2"}}
	got := entriesNamed(".contributors/ir/iris-kell holds tmdb 1 and .contributors/ir/iris-kell-2 holds tmdb 2", group)

	want := entryNamed(".contributors/ir/iris-kell") + " holds tmdb 1 and " +
		entryNamed(".contributors/ir/iris-kell-2") + " holds tmdb 2"
	if got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}
}

// The walk's summary names the folders it could not identify by hash and
// keeps the counts.
func TestTheWalkNamesItsUnidentifiedFoldersByHash(t *testing.T) {
	scan, _ := testScanner(t, "/srv/movies", libraryKindMovies)
	logged := &bytes.Buffer{}
	scan.log = logged

	scan.logWalkComplete(3, 3, 3, 0, []string{"Some Film (2001)", "Another Film (2002)"}, 0)

	wantOpaque(t, logged.String(),
		[]string{
			"walk complete: 3 titles from 3 folders, 3 unidentified",
			"unidentified folders: " + opaquePath("/srv/movies", "Some Film (2001)") + ", " +
				opaquePath("/srv/movies", "Another Film (2002)") + ", and 1 more",
		},
		[]string{"Some Film", "Another Film"})
}

// A rescan names its folder by hash, and the webhook's line reads the same
// whether the folder changed or not.
func TestARescanNamesItsFolderByHash(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Some Film (2001)", "Some Film (2001).mkv"), "video")
	scan, _ := testScanner(t, root, libraryKindMovies)
	logged := &bytes.Buffer{}
	scan.log = logged

	if err := scan.rescan(context.Background(), filepath.Join(root, "Some Film (2001)")); err != nil {
		t.Fatal(err)
	}

	wantOpaque(t, logged.String(),
		[]string{"rescanned " + opaquePath(root, "Some Film (2001)") + ": wrote"},
		[]string{"Some Film", root})
}

// A worker's failure reaches the operator's line word for word, with a path
// in it made opaque, so an older worker's text keeps no title either.
func TestTheOperatorRepeatsAFailureWithItsPathsOpaque(t *testing.T) {
	operator, logged := loggingOperator(t, newFakeCluster())
	failed := runFailed
	failed.Failure = "the art: open /library/Some Film (2001)/poster.jpg: permission denied"

	publishRuns(operator, runStarted)
	publishRuns(operator, runStarted, failed)

	wantOpaque(t, logged.String(),
		[]string{"with a failure: the art: open " + opaquePath(libraryMountPath, "Some Film (2001)/poster.jpg") +
			": permission denied"},
		[]string{"Some Film"})
}

// The progress role names a Play by the hash of its name, because the API
// server mints the name from the title's slug.
func TestAProgressLineNamesThePlayByHash(t *testing.T) {
	logged := &bytes.Buffer{}
	role := &progress{namespace: "house", log: logged}

	role.recordStatus(t.Context(), "den-some-film-x7k2p", []byte("not json"))

	wantOpaque(t, logged.String(),
		[]string{"the status of the Play house/" + hashed("den-some-film-x7k2p") + " reads as no report: "},
		[]string{"some-film"})
}

// A Play's API error names the Play in its request, and the line carries the
// hash of that name.
func TestAPlayErrorCarriesTheHashOfTheName(t *testing.T) {
	err := errors.New("PATCH /apis/media.liken.sh/v1alpha1/namespaces/house/plays/den-some-film: " +
		"500 Internal Server Error: the API server is unwell")

	got := playError(err, "den-some-film")

	want := "PATCH /apis/media.liken.sh/v1alpha1/namespaces/house/plays/" + hashed("den-some-film") +
		": 500 Internal Server Error: the API server is unwell"
	if got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}
