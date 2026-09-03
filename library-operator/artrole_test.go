package main

// What these tests read: the file each art fact writes beside a title, that a
// file another tool wrote is never opened, and what the ledger records when a
// provider has no image or refuses one.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One images answer with one image in the list a fact reads.
func imagesAnswer(list, filePath, language string) string {
	return `{"` + list + `":[{"file_path":"` + filePath +
		`","iso_639_1":` + languageJSON(language) + `,"vote_average":5.4,"vote_count":11}]}`
}

func languageJSON(language string) string {
	if language == "" {
		return "null"
	}
	return `"` + language + `"`
}

// The ledger one art fact left in a folder.
func artLedger(t *testing.T, folder, fact string) likenLedger {
	t.Helper()
	ledger, err := readLikenLedger(folder, fact)
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}

// Each fact writes its own file, under the name Kodi and Jellyfin read, from
// the endpoint and the size that fact asks for.
func TestEachArtFactWritesItsFileWhereNoneExists(t *testing.T) {
	movieFolder := "The Signal (2014)"
	seriesFolder := "Quiet Harbor (2008)"
	season := filepath.Join(seriesFolder, "Season 01")

	cases := []struct {
		fact     string
		kind     string
		seed     func(t *testing.T, catalog *Catalog)
		endpoint string
		list     string
		size     string
		want     string
	}{
		{
			fact: factPoster, kind: libraryKindMovies,
			seed:     func(t *testing.T, c *Catalog) { seedArtMovie(t, c, movieFolder) },
			endpoint: "/3/movie/603/images", list: tmdbPosters, size: "w780",
			want: filepath.Join(movieFolder, "poster.jpg"),
		},
		{
			fact: factBackdrop, kind: libraryKindMovies,
			seed:     func(t *testing.T, c *Catalog) { seedArtMovie(t, c, movieFolder) },
			endpoint: "/3/movie/603/images", list: tmdbBackdrops, size: "w1280",
			want: filepath.Join(movieFolder, "fanart.jpg"),
		},
		{
			fact: factLogo, kind: libraryKindSeries,
			seed:     func(t *testing.T, c *Catalog) { seedArtSeries(t, c, seriesFolder, []int{1}) },
			endpoint: "/3/tv/1396/images", list: tmdbLogos, size: "w500",
			want: filepath.Join(seriesFolder, "clearlogo.png"),
		},
		{
			fact: factSeasonPoster, kind: libraryKindSeries,
			seed:     func(t *testing.T, c *Catalog) { seedArtSeries(t, c, seriesFolder, []int{1}) },
			endpoint: "/3/tv/1396/season/1/images", list: tmdbPosters, size: "w780",
			want: filepath.Join(seriesFolder, "season01-poster.jpg"),
		},
		{
			fact: factEpisodeThumb, kind: libraryKindSeries,
			seed:     func(t *testing.T, c *Catalog) { seedArtSeries(t, c, seriesFolder, []int{1}) },
			endpoint: "/3/tv/1396/season/1/episode/5/images", list: tmdbStills, size: "w300",
			want: filepath.Join(season, "Quiet Harbor - S01E05-thumb.jpg"),
		},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			writeFile(t, filepath.Join(root, season, "Quiet Harbor - S01E05.mkv"), "video")
			writeFile(t, filepath.Join(root, movieFolder, "The Signal (2014).mkv"), "video")
			test.seed(t, catalog)
			work, log := testEnricher(t, test.kind, root, catalog)
			client, _ := newArtTMDb(t, map[string]string{
				tmdbKey(test.endpoint, "", ""):                  imagesAnswer(test.list, "/quiet.jpg", artLanguage),
				tmdbKey("/t/p/"+test.size+"/quiet.jpg", "", ""): testImage,
			})

			if err := work.artGap(t.Context(), test.fact, client); err != nil {
				t.Fatal(err)
			}

			if got := readFileString(t, filepath.Join(root, test.want)); got != testImage {
				t.Errorf("%s holds %d bytes, want the image", test.want, len(got))
			}
			if !strings.Contains(log.String(), "wrote the "+test.fact) {
				t.Errorf("log = %q, want the line that names the file", log.String())
			}
			ledger := artLedger(t, filepath.Join(root, filepath.Dir(test.want)), test.fact)
			if len(ledger.Items) != 1 || ledger.Items[0].Provider != providerTMDb {
				t.Errorf("ledger items = %+v, want the provider that answered", ledger.Items)
			}
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
				t.Errorf("ledger attempts = %+v, want one that found the image", ledger.Attempts)
			}
		})
	}
}

// A file another tool wrote is never opened and never asked about, and the
// ledger records that it was already there.
func TestAnArtFileThatExistsIsLeftAsItIs(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Signal (2014)"
	poster := filepath.Join(root, folder, "poster.jpg")
	writeFile(t, poster, "the poster a person kept")
	seedArtMovie(t, catalog, folder)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	client, fake := newArtTMDb(t, map[string]string{
		tmdbKey("/3/movie/603/images", "", ""): imagesAnswer(tmdbPosters, "/quiet.jpg", artLanguage),
	})

	if err := work.artGap(t.Context(), factPoster, client); err != nil {
		t.Fatal(err)
	}

	if got := readFileString(t, poster); got != "the poster a person kept" {
		t.Errorf("poster = %q, want the bytes the other tool wrote", got)
	}
	if fake.served[tmdbKey("/3/movie/603/images", "", "")] != 0 {
		t.Error("the fact asked the provider about a title whose file is already there")
	}
	ledger := artLedger(t, filepath.Join(root, folder), factPoster)
	if len(ledger.Items) != 1 || ledger.Items[0].Provider != artProviderExisting {
		t.Errorf("ledger items = %+v, want the entry that says the file was already there", ledger.Items)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("ledger attempts = %+v, want one that found the file", ledger.Attempts)
	}
}

// A provider that has no image and a provider that refuses one leave
// different marks, so the next run knows which of them to ask again.
func TestWhatAnArtFactRecordsWhenItWritesNothing(t *testing.T) {
	cases := []struct {
		name       string
		answers    map[string]string
		statuses   map[string]int
		wantResult string
	}{
		{
			name:       "the provider has no image",
			answers:    map[string]string{tmdbKey("/3/movie/603/images", "", ""): `{"posters":[]}`},
			wantResult: attemptNothing,
		},
		{
			name:       "the provider refuses the list",
			answers:    map[string]string{},
			statuses:   map[string]int{tmdbKey("/3/movie/603/images", "", ""): http.StatusUnauthorized},
			wantResult: attemptError,
		},
		{
			name: "the download fails",
			answers: map[string]string{
				tmdbKey("/3/movie/603/images", "", ""): imagesAnswer(tmdbPosters, "/quiet.jpg", artLanguage),
			},
			statuses:   map[string]int{tmdbKey("/t/p/w780/quiet.jpg", "", ""): http.StatusInternalServerError},
			wantResult: attemptError,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			folder := "The Signal (2014)"
			writeFile(t, filepath.Join(root, folder, "The Signal (2014).mkv"), "video")
			seedArtMovie(t, catalog, folder)
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			client, fake := newArtTMDb(t, test.answers)
			for key, status := range test.statuses {
				fake.statuses[key] = status
			}

			if err := work.artGap(t.Context(), factPoster, client); err != nil {
				t.Fatal(err)
			}

			if _, err := os.Stat(filepath.Join(root, folder, "poster.jpg")); err == nil {
				t.Error("the fact wrote a file, want none")
			}
			ledger := artLedger(t, filepath.Join(root, folder), factPoster)
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != test.wantResult {
				t.Errorf("ledger attempts = %+v, want %s", ledger.Attempts, test.wantResult)
			}
			if len(ledger.Items) != 0 {
				t.Errorf("ledger items = %+v, want none, because nothing was written", ledger.Items)
			}
		})
	}
}

// A library with every file in place asks the provider nothing at all, not
// even for its settings.
func TestAnArtFactWithNoGapMakesNoCall(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	client, fake := newArtTMDb(t, map[string]string{})

	if err := work.artGap(t.Context(), factPoster, client); err != nil {
		t.Fatal(err)
	}

	if len(fake.requestPath) != 0 {
		t.Errorf("the fact made %v, want no call", fake.requestPath)
	}
}

// A container with no key fails before it writes anything.
func TestTheArtFactNeedsAProviderKey(t *testing.T) {
	t.Setenv(tmdbTokenVariable, "")
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), nil)

	if err := work.artFact(t.Context(), factPoster); err == nil {
		t.Error("the fact reported no error, want one")
	}
}

// A Job narrowed to one folder leaves the art of every other folder as it is.
func TestAnArtFactOutsideTheScopeIsLeftAlone(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Signal (2014)"
	writeFile(t, filepath.Join(root, folder, "The Signal (2014).mkv"), "video")
	seedArtMovie(t, catalog, folder)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.scope = "Another Film (2001)"
	client, _ := newArtTMDb(t, map[string]string{
		tmdbKey("/3/movie/603/images", "", ""): imagesAnswer(tmdbPosters, "/quiet.jpg", artLanguage),
		tmdbKey("/t/p/w780/quiet.jpg", "", ""): testImage,
	})

	if err := work.artGap(t.Context(), factPoster, client); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(root, folder, "poster.jpg")); err == nil {
		t.Error("the fact wrote a file outside its own folder")
	}
}

// A catalog that cannot be read ends the container, because the gap list is
// the work.
func TestAnArtFactEndsWhenTheCatalogFails(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), NewCatalog("http://127.0.0.1:1", http.DefaultClient))
	client, _ := newArtTMDb(t, map[string]string{})

	if err := work.artGap(t.Context(), factPoster, client); err == nil {
		t.Error("the fact reported no error, want one")
	}
}

// The write door never lands on a file that exists, so a file that arrived
// between the read and the write is kept and recorded as it was found.
func TestTheWriteDoorKeepsAFileThatArrivedFirst(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "poster.jpg")
	writer := newVolumeWriter("movies-enrich")
	writeFile(t, target, "the poster a person kept")

	written, err := writer.createOnce(target, []byte(testImage))
	if err != nil {
		t.Fatal(err)
	}
	if written {
		t.Error("the door reported a write, want none")
	}
	if got := readFileString(t, target); got != "the poster a person kept" {
		t.Errorf("poster = %q, want the bytes that were there", got)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the folder holds %d files, want the one, with no temporary left", len(entries))
	}
}

func TestTheWriteDoorCreatesAFileThatIsNotThere(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "poster.jpg")

	written, err := newVolumeWriter("movies-enrich").createOnce(target, []byte(testImage))
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Error("the door reported no write, want one")
	}
	if got := readFileString(t, target); got != testImage {
		t.Errorf("poster = %q, want the image", got)
	}
}

func TestTheWriteDoorAnswersAFolderThatIsNotThere(t *testing.T) {
	target := filepath.Join(t.TempDir(), "no-folder", "poster.jpg")

	if _, err := newVolumeWriter("movies-enrich").createOnce(target, []byte(testImage)); err == nil {
		t.Error("the door reported no error, want one")
	}
}

// The fact map runs the art fact the container names, with the key the pod
// carries, and a library with no gap ends without a call.
func TestTheFactMapRunsTheArtFact(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	t.Setenv(tmdbTokenVariable, "the-key")

	if err := factRuns[factPoster](t.Context(), work); err != nil {
		t.Fatal(err)
	}
}

// A file that arrived between the read and the write is kept, and the ledger
// records that it was already there.
func TestAnArtFactKeepsAFileThatArrivedBeforeTheWrite(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := filepath.Join(root, "The Signal (2014)")
	target := filepath.Join(folder, "poster.jpg")
	writeFile(t, target, "the poster a person kept")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	client, _ := newArtTMDb(t, map[string]string{
		tmdbKey("/t/p/w780/quiet.jpg", "", ""): testImage,
	})
	configuration, err := client.configuration(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	gap := artGap{key: "The Signal (2014)/poster.jpg", tmdb: "603"}
	if work.writeArt(t.Context(), client, configuration, artTypes[factPoster],
		gap, folder, target, tmdbImage{FilePath: "/quiet.jpg"}) {
		t.Error("the fact reported a write, want none")
	}

	if got := readFileString(t, target); got != "the poster a person kept" {
		t.Errorf("poster = %q, want the bytes that were there", got)
	}
	ledger := artLedger(t, folder, factPoster)
	if len(ledger.Items) != 1 || ledger.Items[0].Provider != artProviderExisting {
		t.Errorf("ledger items = %+v, want the entry that says the file was already there", ledger.Items)
	}
}

// A folder the write door cannot reach is an error attempt, and the next run
// tries again.
func TestAWriteTheVolumeRefusesIsAnErrorAttempt(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := filepath.Join(root, "The Signal (2014)")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	client, _ := newArtTMDb(t, map[string]string{
		tmdbKey("/t/p/w780/quiet.jpg", "", ""): testImage,
	})
	configuration, err := client.configuration(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	gap := artGap{key: "The Signal (2014)/poster.jpg", tmdb: "603"}
	if work.writeArt(t.Context(), client, configuration, artTypes[factPoster],
		gap, folder, filepath.Join(folder, "poster.jpg"), tmdbImage{FilePath: "/quiet.jpg"}) {
		t.Error("the fact reported a write into a folder that is not there")
	}

	ledger := artLedger(t, folder, factPoster)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
		t.Errorf("ledger attempts = %+v, want an error", ledger.Attempts)
	}
}

// A path the volume cannot answer for is an error attempt and never a gap the
// fact fills, so a mount that is not there writes nothing.
func TestAPathTheVolumeCannotAnswerForIsAnErrorAttempt(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Signal (2014)"), "a file where the folder should be")
	seedArtMovie(t, catalog, "The Signal (2014)")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	client, _ := newArtTMDb(t, map[string]string{})

	if err := work.artGap(t.Context(), factPoster, client); err != nil {
		t.Fatal(err)
	}

	if fileExistsInTest(t, filepath.Join(root, "The Signal (2014)", "poster.jpg")) {
		t.Error("the fact wrote a file, want none")
	}
}

func fileExistsInTest(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

// A Library whose sources serve no art fact runs no art container.
func TestNoProviderServesArt(t *testing.T) {
	cluster := newFakeCluster()
	identityOnly := seedProvider(cluster, "tmdb", "house", factIdentity)
	identityOnly.Status.Conditions = []Condition{
		{Type: conditionReady, Status: ConditionTrue, Reason: reasonReachable},
	}
	set := providerSet{"house/tmdb": identityOnly}

	if provider := set.servingArt("house", []string{"tmdb"}); provider != nil {
		t.Errorf("provider = %+v, want none, because it serves no art fact", provider)
	}
	identityOnly.Spec.Facts = []string{factIdentity, factLogo}
	if provider := set.servingArt("house", []string{"tmdb"}); provider == nil {
		t.Error("provider = none, want the one that serves the logo")
	}
}
