package main

// What these tests read: which blocks the trailer line asks, what the union
// of their answers leaves in the ledger and the trailers table, and which
// titles the gap holds.

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// One provider block that answers a trailer ask with what a test names.
type scriptedTrailers struct {
	block   string
	entries []trailerEntry
	err     error
}

func (s scriptedTrailers) providerBlock() string { return s.block }

func (s scriptedTrailers) trailers(context.Context, trailerTitle) ([]trailerEntry, error) {
	return s.entries, s.err
}

// One trailer as a provider names it.
func trailerEntryOf(provider, key string, score int) trailerEntry {
	return trailerEntry{
		Path: likenSelfPath, Provider: provider, Key: key, Site: trailerSiteYouTube,
		URL: "https://www.youtube.com/watch?v=" + key, Name: "Official Trailer",
		Kind: trailerKindTrailer, Language: "en", Official: true,
		Published: "2026-08-01", Score: score, Reason: "official trailer",
	}
}

// The line one test asks: the answerers it names and no others, in the order
// it names them.
func trailerLineOf(answerers ...trailerAnswerer) *trailerLine {
	return &trailerLine{answerers: answerers}
}

// The blocks a line reads out of the source order, and the two variables the
// providers travel in.
func TestTheTrailerLineTakesTheBlocksThatCanAnswer(t *testing.T) {
	cases := []struct {
		name   string
		blocks []string
		env    map[string]string
		want   []string
	}{
		{
			name: "a block with no key is skipped", blocks: []string{providerBlockTMDb},
			env: map[string]string{}, want: nil,
		},
		{
			name: "tmdb with its key", blocks: []string{providerBlockTMDb},
			env:  map[string]string{tmdbTokenVariable: "a-key"},
			want: []string{providerBlockTMDb},
		},
		{
			name: "peertube with its address", blocks: []string{providerBlockPeerTube},
			env:  map[string]string{peertubeEndpointVariable: "https://videos.example"},
			want: []string{providerBlockPeerTube},
		},
		{
			name: "peertube with no address is skipped", blocks: []string{providerBlockPeerTube},
			env: map[string]string{}, want: nil,
		},
		{
			name:   "the source order is the order of the line",
			blocks: []string{providerBlockPeerTube, providerBlockOMDb, providerBlockTMDb},
			env: map[string]string{
				tmdbTokenVariable:                        "a-key",
				peertubeEndpointVariable:                 "https://videos.example",
				providerTokenVariable(providerBlockOMDb): "another-key",
			},
			want: []string{providerBlockPeerTube, providerBlockTMDb},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			line := newTrailerLine(test.blocks, func(name string) string { return test.env[name] })

			var blocks []string
			for _, one := range line.answerers {
				blocks = append(blocks, one.providerBlock())
			}
			if !slices.Equal(blocks, test.want) {
				t.Errorf("the line asks %v, want %v", blocks, test.want)
			}
		})
	}
}

// Every block answers, because a trailer list is a set and not a single
// value.
func TestATrailerAskTakesTheAnswerOfEveryBlock(t *testing.T) {
	line := trailerLineOf(
		scriptedTrailers{block: providerBlockTMDb, entries: []trailerEntry{
			trailerEntryOf(providerBlockTMDb, "aaa", 90),
		}},
		scriptedTrailers{block: providerBlockPeerTube, entries: []trailerEntry{
			trailerEntryOf(providerBlockPeerTube, "bbb", 40),
		}},
	)

	entries, blocks, err := line.ask(t.Context(), trailerTitle{title: "The Signal", year: 2014})

	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Key != "aaa" || entries[1].Key != "bbb" {
		t.Errorf("entries = %+v, want both answers in the line's order", entries)
	}
	if !slices.Equal(blocks, []string{providerBlockTMDb, providerBlockPeerTube}) {
		t.Errorf("blocks = %v, want the two that answered", blocks)
	}
}

// A block that holds nothing is not a block that answered.
func TestATrailerAskNamesOnlyTheBlocksThatHeldATrailer(t *testing.T) {
	line := trailerLineOf(
		scriptedTrailers{block: providerBlockTMDb},
		scriptedTrailers{block: providerBlockPeerTube, entries: []trailerEntry{
			trailerEntryOf(providerBlockPeerTube, "bbb", 40),
		}},
	)

	entries, blocks, err := line.ask(t.Context(), trailerTitle{})

	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !slices.Equal(blocks, []string{providerBlockPeerTube}) {
		t.Errorf("ask = %+v from %v, want the one block that held a trailer", entries, blocks)
	}
}

// A block that is down leaves the other blocks their answer. The error stands
// only where no block answered.
func TestATrailerAskReportsAFailureOnlyWhereNoBlockAnswered(t *testing.T) {
	refused := errors.New("the provider refused the key")
	cases := []struct {
		name      string
		answerers []trailerAnswerer
		want      int
		wantError bool
	}{
		{
			name: "one block down and one that answered",
			answerers: []trailerAnswerer{
				scriptedTrailers{block: providerBlockTMDb, err: refused},
				scriptedTrailers{block: providerBlockPeerTube, entries: []trailerEntry{
					trailerEntryOf(providerBlockPeerTube, "bbb", 40),
				}},
			},
			want: 1,
		},
		{
			name: "one block down and one that held nothing",
			answerers: []trailerAnswerer{
				scriptedTrailers{block: providerBlockTMDb, err: refused},
				scriptedTrailers{block: providerBlockPeerTube},
			},
			wantError: true,
		},
		{
			name: "every block holding nothing",
			answerers: []trailerAnswerer{
				scriptedTrailers{block: providerBlockTMDb},
				scriptedTrailers{block: providerBlockPeerTube},
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			entries, _, err := trailerLineOf(test.answerers...).ask(t.Context(), trailerTitle{})

			if test.wantError && !errors.Is(err, refused) {
				t.Errorf("err = %v, want the failure the block reported", err)
			}
			if !test.wantError && err != nil {
				t.Errorf("err = %v, want none", err)
			}
			if len(entries) != test.want {
				t.Errorf("entries = %+v, want %d", entries, test.want)
			}
		})
	}
}

// One identified movie with a sidecar that carries its ids, which is the
// shape of every trailer gap.
func seedTrailerGap(t *testing.T, catalog *Catalog, root, folder string) {
	t.Helper()
	writeFile(t, filepath.Join(root, folder, folder+".mkv"), "video")
	writeFile(t, filepath.Join(root, folder, movieSidecarName),
		`<movie><title>The Signal</title><year>2014</year><uniqueid type="tmdb">603</uniqueid></movie>`)
	seed := &walkResult{
		movies: []movieRow{{
			Id: "movie:tmdb:603", Library: "house/movies", Kind: libraryKindMovies,
			Path: folder, Title: "The Signal", Released: "2014-06-13",
		}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

// The whole run of one title: the ledger holds every trailer, sorted, and the
// trailers table holds the same set.
func TestTheTrailerFactWritesTheLedgerAndTheRowsOfOneTitle(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Signal (2014)"
	seedTrailerGap(t, catalog, root, folder)
	work, log := testEnricher(t, libraryKindMovies, root, catalog)
	line := trailerLineOf(
		scriptedTrailers{block: providerBlockPeerTube, entries: []trailerEntry{
			trailerEntryOf(providerBlockPeerTube, "e6b1", 40),
		}},
		scriptedTrailers{block: providerBlockTMDb, entries: []trailerEntry{
			trailerEntryOf(providerBlockTMDb, "low", 20),
			trailerEntryOf(providerBlockTMDb, "high", 90),
		}},
	)

	if err := work.trailerGap(t.Context(), line); err != nil {
		t.Fatal(err)
	}

	ledger := artLedger(t, filepath.Join(root, folder), factTrailer)
	var keys []string
	for _, entry := range ledger.Trailers {
		keys = append(keys, entry.Provider+":"+entry.Key)
	}
	want := []string{"peertube:e6b1", "tmdb:high", "tmdb:low"}
	if !slices.Equal(keys, want) {
		t.Errorf("the ledger holds %v, want %v", keys, want)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Fatalf("attempts = %+v, want the one that found the trailers", ledger.Attempts)
	}
	if got := strings.Join(ledger.Attempts[0].Provider, ","); got != "peertube,tmdb" {
		t.Errorf("the attempt names %q, want both blocks that answered", got)
	}
	if ledger.Attempts[0].Path != likenSelfPath {
		t.Errorf("the attempt names %q, want the title's own entry", ledger.Attempts[0].Path)
	}
	rows, err := catalog.queryStrings(t.Context(),
		`SELECT provider || ':' || key FROM trailers WHERE library = ? AND item = ? ORDER BY provider, key`,
		[]any{"house/movies", "movie:tmdb:603"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rows, []string{"peertube:e6b1", "tmdb:high", "tmdb:low"}) {
		t.Errorf("the table holds %v, want one row per trailer", rows)
	}
	if !strings.Contains(log.String(), "the trailers of 1 of the 1 titles") {
		t.Errorf("log = %q, want the line that counts the run", log.String())
	}
}

// The ids the fact asks with come off the sidecar the identity fact wrote.
func TestATrailerAskCarriesTheIdsAndTheTitleOfTheFolder(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Signal (2014)"
	seedTrailerGap(t, catalog, root, folder)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	asked := &askedTrailerTitle{}

	if err := work.trailerGap(t.Context(), trailerLineOf(asked)); err != nil {
		t.Fatal(err)
	}

	if asked.title.ids["tmdb"] != "603" {
		t.Errorf("the ask carried %v, want the ids of the sidecar", asked.title.ids)
	}
	if asked.title.title != "The Signal" || asked.title.year != 2014 {
		t.Errorf("the ask carried %q of %d, want the title and the year", asked.title.title, asked.title.year)
	}
	if asked.title.kind != libraryKindMovies {
		t.Errorf("the ask carried the kind %q, want the Library's own", asked.title.kind)
	}
}

// The title of the last ask, so a test reads what the line was given.
type askedTrailerTitle struct {
	title trailerTitle
}

func (a *askedTrailerTitle) providerBlock() string { return providerBlockTMDb }

func (a *askedTrailerTitle) trailers(_ context.Context, title trailerTitle) ([]trailerEntry, error) {
	a.title = title
	return nil, nil
}

// The title the trailer gap holds, as the run reads it out of the catalog.
func trailerItem(t *testing.T, catalog *Catalog) identityItem {
	t.Helper()
	item, held, err := catalog.identityItem(t.Context(), "house/movies", "movie:tmdb:603")
	if err != nil {
		t.Fatal(err)
	}
	if !held {
		t.Fatal("the catalog holds no title to ask about")
	}
	return item
}

// A title no provider holds a trailer for keeps no list, because the list is
// what the providers hold now.
func TestATitleNoProviderHoldsATrailerForLosesTheListItHeld(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Signal (2014)"
	seedTrailerGap(t, catalog, root, folder)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	item := trailerItem(t, catalog)
	held := trailerLineOf(scriptedTrailers{block: providerBlockTMDb, entries: []trailerEntry{
		trailerEntryOf(providerBlockTMDb, "high", 90),
	}})
	work.trailerOne(t.Context(), held, item)

	work.trailerOne(t.Context(), trailerLineOf(scriptedTrailers{block: providerBlockTMDb}), item)

	ledger := artLedger(t, filepath.Join(root, folder), factTrailer)
	if len(ledger.Trailers) != 0 {
		t.Errorf("the ledger holds %+v, want no trailer", ledger.Trailers)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptNothing {
		t.Errorf("attempts = %+v, want the one that found nothing", ledger.Attempts)
	}
}

// A provider that is down does not say the title has no trailer, so the list
// stands.
func TestATrailerAskThatFailedKeepsTheListAndRecordsTheError(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Signal (2014)"
	seedTrailerGap(t, catalog, root, folder)
	work, log := testEnricher(t, libraryKindMovies, root, catalog)
	item := trailerItem(t, catalog)
	held := trailerLineOf(scriptedTrailers{block: providerBlockTMDb, entries: []trailerEntry{
		trailerEntryOf(providerBlockTMDb, "high", 90),
	}})
	work.trailerOne(t.Context(), held, item)

	down := trailerLineOf(scriptedTrailers{
		block: providerBlockTMDb, err: errors.New("the provider refused the key"),
	})
	work.trailerOne(t.Context(), down, item)

	ledger := artLedger(t, filepath.Join(root, folder), factTrailer)
	if len(ledger.Trailers) != 1 || ledger.Trailers[0].Key != "high" {
		t.Errorf("the ledger holds %+v, want the trailer the last answer named", ledger.Trailers)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
		t.Errorf("attempts = %+v, want the one that failed", ledger.Attempts)
	}
	if !strings.Contains(log.String(), "could not read the trailers of") {
		t.Errorf("log = %q, want the line that names the failure", log.String())
	}
}

// A Job narrowed to one folder asks about that folder alone.
func TestTheTrailerFactSkipsATitleOutsideTheJobsScope(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Signal (2014)"
	seedTrailerGap(t, catalog, root, folder)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.scope = "Another Film (2001)"

	if err := work.trailerGap(t.Context(), trailerLineOf(scriptedTrailers{
		block: providerBlockTMDb, entries: []trailerEntry{trailerEntryOf(providerBlockTMDb, "high", 90)},
	})); err != nil {
		t.Fatal(err)
	}

	ledger := artLedger(t, filepath.Join(root, folder), factTrailer)
	if len(ledger.Attempts) != 0 {
		t.Errorf("attempts = %+v, want none, because the title is out of scope", ledger.Attempts)
	}
}

// A container the operator created with no provider of this fact is a
// manifest to repair.
func TestTheTrailerFactNeedsAProviderItCanAsk(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), nil)
	t.Setenv(librarySourcesVariable, providerBlockOMDb)

	err := work.trailerFact(t.Context())

	if err == nil || !strings.Contains(err.Error(), factTrailer) {
		t.Errorf("err = %v, want the failure that names the fact", err)
	}
}

// The line is built once, out of the environment, the way the art line is.
func TestTheTrailerFactBuildsItsLineOutOfTheEnvironment(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	t.Setenv(librarySourcesVariable, providerBlockTMDb)
	t.Setenv(tmdbTokenVariable, "a-key")

	if err := work.trailerFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if work.trailers == nil || len(work.trailers.answerers) != 1 {
		t.Errorf("the line holds %+v, want the one block the environment names", work.trailers)
	}
}

// The gap is an identified title whose last trailer attempt has passed its
// own window.
func TestTheTrailerGapHoldsTheIdentifiedTitlesAgainstTheRealSchema(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name     string
		attempts []attemptRow
		want     []string
	}{
		{
			name: "every title a provider has named",
			want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"},
		},
		{
			name: "a title asked about inside the window",
			attempts: []attemptRow{{
				Library: "house/movies", Item: "movie:tmdb:1", Fact: factTrailer,
				At: now.Unix(), Result: attemptFound, Provider: providerBlockTMDb,
			}},
			want: []string{"movie:tmdb:2", "series:tvdb:9"},
		},
		{
			name: "a title asked about past the window",
			attempts: []attemptRow{{
				Library: "house/movies", Item: "movie:tmdb:1", Fact: factTrailer,
				At: now.Add(-2 * defaultRetryInterval).Unix(), Result: attemptNothing,
			}},
			want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"},
		},
		{
			name: "a title whose provider was down, inside the error window",
			attempts: []attemptRow{{
				Library: "house/movies", Item: "movie:tmdb:1", Fact: factTrailer,
				At: now.Unix(), Result: attemptError,
			}},
			want: []string{"movie:tmdb:2", "series:tvdb:9"},
		},
		{
			name: "a title whose provider was down, past the error window",
			attempts: []attemptRow{{
				Library: "house/movies", Item: "movie:tmdb:1", Fact: factTrailer,
				At: now.Add(-2 * errorRetryInterval).Unix(), Result: attemptError,
			}},
			want: []string{"movie:tmdb:1", "movie:tmdb:2", "series:tvdb:9"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seedNFOFactRows(t, catalog)
			if _, err := catalog.UpsertAttempts(t.Context(), test.attempts); err != nil {
				t.Fatal(err)
			}

			ids, err := catalog.queryStrings(t.Context(), gapQueries[factTrailer],
				gapParams(factTrailer, "house/movies", now, time.Time{}))
			if err != nil {
				t.Fatal(err)
			}

			slices.Sort(ids)
			if !slices.Equal(ids, test.want) {
				t.Errorf("gap = %v, want %v", ids, test.want)
			}
		})
	}
}

// An attempt made before the title's release date stands only until that
// date, the rule every title fact takes.
func TestATrailerAttemptFromBeforeTheReleaseDateReopensTheGap(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	released := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	seed := &walkResult{movies: []movieRow{{
		Id: "movie:tmdb:1", Library: "house/movies", Kind: libraryKindMovies,
		Path: "One (2026)", Title: "One", Released: released.Format(time.DateOnly),
	}}}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertAttempts(t.Context(), []attemptRow{{
		Library: "house/movies", Item: "movie:tmdb:1", Fact: factTrailer,
		At: released.Add(-24 * time.Hour).Unix(), Result: attemptNothing,
	}}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		now  time.Time
		want int
	}{
		{name: "before the release day", now: released.Add(-time.Hour), want: 0},
		{name: "on the release day", now: released.Add(time.Hour), want: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ids, err := catalog.queryStrings(t.Context(), gapQueries[factTrailer],
				gapParams(factTrailer, "house/movies", test.now, time.Time{}))
			if err != nil {
				t.Fatal(err)
			}
			if len(ids) != test.want {
				t.Errorf("gap = %v, want %d titles", ids, test.want)
			}
		})
	}
}
