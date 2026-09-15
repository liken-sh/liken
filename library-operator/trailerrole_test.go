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

// One trailer a provider names, with the name, the score, and the published
// date a test states.
func namedTrailer(provider, key, name string, score int, published string) trailerEntry {
	entry := trailerEntryOf(provider, key, score)
	entry.Name = name
	entry.Published = published
	return entry
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
			env:  map[string]string{providerEndpointVariable(providerBlockPeerTube): "https://videos.example"},
			want: []string{providerBlockPeerTube},
		},
		{
			name: "peertube with no address is skipped", blocks: []string{providerBlockPeerTube},
			env: map[string]string{}, want: nil,
		},
		{
			name:   "archive, which needs no setting of its own",
			blocks: []string{providerBlockArchive}, env: map[string]string{},
			want: []string{providerBlockArchive},
		},
		{
			name: "the source order is the order of the line",
			blocks: []string{providerBlockPeerTube, providerBlockOMDb,
				providerBlockArchive, providerBlockTMDb},
			env: map[string]string{
				tmdbTokenVariable: "a-key",
				providerEndpointVariable(providerBlockPeerTube): "https://videos.example",
				providerTokenVariable(providerBlockOMDb):        "another-key",
			},
			want: []string{providerBlockPeerTube, providerBlockArchive, providerBlockTMDb},
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

// One block that reports it was asked, then waits for the gate.
type gatedTrailers struct {
	block   string
	entries []trailerEntry
	arrive  chan<- struct{}
	open    <-chan struct{}
}

func (g gatedTrailers) providerBlock() string { return g.block }

func (g gatedTrailers) trailers(ctx context.Context, _ trailerTitle) ([]trailerEntry, error) {
	g.arrive <- struct{}{}
	select {
	case <-g.open:
		return g.entries, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// The gate opens once every block has been asked. It never opens for a line
// that asks one block at a time, so that ask ends on its context instead of
// holding the test.
func trailerGate(ctx context.Context, blocks int) (chan struct{}, chan struct{}) {
	arrive := make(chan struct{}, blocks)
	open := make(chan struct{})
	go func() {
		for range blocks {
			select {
			case <-arrive:
			case <-ctx.Done():
				return
			}
		}
		close(open)
	}()
	return arrive, open
}

// Every block is asked at once, so one title costs the slowest provider and
// not the sum of them.
func TestATrailerAskAsksEveryBlockAtOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	arrive, open := trailerGate(ctx, 3)
	line := trailerLineOf(
		gatedTrailers{block: providerBlockTMDb, arrive: arrive, open: open,
			entries: []trailerEntry{trailerEntryOf(providerBlockTMDb, "aaa", 90)}},
		gatedTrailers{block: providerBlockPeerTube, arrive: arrive, open: open,
			entries: []trailerEntry{trailerEntryOf(providerBlockPeerTube, "bbb", 40)}},
		gatedTrailers{block: providerBlockArchive, arrive: arrive, open: open,
			entries: []trailerEntry{trailerEntryOf(providerBlockArchive, "ccc", 30)}},
	)

	entries, blocks, err := line.ask(ctx, trailerTitle{})

	if err != nil {
		t.Fatalf("err = %v, want the three blocks asked at once", err)
	}
	if len(entries) != 3 {
		t.Errorf("entries = %+v, want one of each block", entries)
	}
	if !slices.Equal(blocks, []string{providerBlockTMDb, providerBlockPeerTube, providerBlockArchive}) {
		t.Errorf("blocks = %v, want the three that answered", blocks)
	}
}

// One block that answers only after the block it names has answered.
type chainedTrailers struct {
	block   string
	entries []trailerEntry
	after   <-chan struct{}
	done    chan struct{}
}

func (c chainedTrailers) providerBlock() string { return c.block }

func (c chainedTrailers) trailers(ctx context.Context, _ trailerTitle) ([]trailerEntry, error) {
	defer close(c.done)
	select {
	case <-c.after:
		return c.entries, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// A channel that is already closed, which the block that answers first waits
// on.
func openedTrailerGate() chan struct{} {
	gate := make(chan struct{})
	close(gate)
	return gate
}

// The entries and the blocks come out in the line's order, whichever block
// answered first.
func TestATrailerAskAssemblesInTheOrderOfTheLine(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	last := chainedTrailers{block: providerBlockArchive, done: make(chan struct{}),
		after:   openedTrailerGate(),
		entries: []trailerEntry{trailerEntryOf(providerBlockArchive, "ccc", 30)}}
	middle := chainedTrailers{block: providerBlockPeerTube, done: make(chan struct{}),
		after:   last.done,
		entries: []trailerEntry{trailerEntryOf(providerBlockPeerTube, "bbb", 40)}}
	first := chainedTrailers{block: providerBlockTMDb, done: make(chan struct{}),
		after:   middle.done,
		entries: []trailerEntry{trailerEntryOf(providerBlockTMDb, "aaa", 90)}}

	entries, blocks, err := trailerLineOf(first, middle, last).ask(ctx, trailerTitle{})

	if err != nil {
		t.Fatalf("err = %v, want the answers of the three blocks", err)
	}
	keys := []string{}
	for _, entry := range entries {
		keys = append(keys, entry.Key)
	}
	if !slices.Equal(keys, []string{"aaa", "bbb", "ccc"}) {
		t.Errorf("entries = %v, want the line's order", keys)
	}
	if !slices.Equal(blocks, []string{providerBlockTMDb, providerBlockPeerTube, providerBlockArchive}) {
		t.Errorf("blocks = %v, want the line's order", blocks)
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
			namedTrailer(providerBlockTMDb, "low", "Teaser", 20, "2026-08-01"),
			namedTrailer(providerBlockTMDb, "high", "Official Trailer", 90, "2026-08-01"),
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

// What the trim leaves of one provider's answer, and what it never collapses.
func TestTheTrimLeavesOneEntryPerNameAndAtMostFivePerProvider(t *testing.T) {
	cases := []struct {
		name    string
		entries []trailerEntry
		want    []string
	}{
		{
			name: "nine uploads of one name collapse to the highest score",
			entries: []trailerEntry{
				namedTrailer(providerBlockArchive, "tcm1", "The Ghost Breakers", 40, "2019-03-02"),
				namedTrailer(providerBlockArchive, "tcm2", "THE GHOST BREAKERS", 45, "2019-03-03"),
				namedTrailer(providerBlockArchive, "tcm3", "The Ghost Breakers!", 30, "2019-03-04"),
				namedTrailer(providerBlockArchive, "tcm4", "The Ghost Breakers", 70, "2019-03-05"),
				namedTrailer(providerBlockArchive, "tcm5", "The Ghost Breakers", 55, "2019-03-06"),
				namedTrailer(providerBlockArchive, "tcm6", "The Ghost Breakers", 20, "2019-03-07"),
				namedTrailer(providerBlockArchive, "tcm7", "The Ghost Breakers", 65, "2019-03-08"),
				namedTrailer(providerBlockArchive, "tcm8", "The Ghost Breakers", 35, "2019-03-09"),
				namedTrailer(providerBlockArchive, "tcm9", "The Ghost Breakers", 50, "2019-03-10"),
			},
			want: []string{"tcm4"},
		},
		{
			name: "a tie on score keeps the earliest date",
			entries: []trailerEntry{
				namedTrailer(providerBlockArchive, "late", "Dune", 50, "2026-08-01"),
				namedTrailer(providerBlockArchive, "early", "Dune", 50, "2019-03-02"),
				namedTrailer(providerBlockArchive, "undated", "Dune", 50, ""),
			},
			want: []string{"early"},
		},
		{
			name: "a tie on score keeps a dated entry over one with no date",
			entries: []trailerEntry{
				namedTrailer(providerBlockArchive, "undated", "Dune", 50, ""),
				namedTrailer(providerBlockArchive, "dated", "Dune", 50, "2020-01-01"),
			},
			want: []string{"dated"},
		},
		{
			name: "a tie on score and date keeps the first seen",
			entries: []trailerEntry{
				namedTrailer(providerBlockArchive, "first", "Dune", 50, "2020-01-01"),
				namedTrailer(providerBlockArchive, "second", "Dune", 50, "2020-01-01"),
			},
			want: []string{"first"},
		},
		{
			name: "seven entries of one provider drop the two lowest scores",
			entries: []trailerEntry{
				namedTrailer(providerBlockArchive, "k1", "One", 10, "2020-01-01"),
				namedTrailer(providerBlockArchive, "k2", "Two", 20, "2020-01-01"),
				namedTrailer(providerBlockArchive, "k3", "Three", 30, "2020-01-01"),
				namedTrailer(providerBlockArchive, "k4", "Four", 40, "2020-01-01"),
				namedTrailer(providerBlockArchive, "k5", "Five", 50, "2020-01-01"),
				namedTrailer(providerBlockArchive, "k6", "Six", 60, "2020-01-01"),
				namedTrailer(providerBlockArchive, "k7", "Seven", 70, "2020-01-01"),
			},
			want: []string{"k3", "k4", "k5", "k6", "k7"},
		},
		{
			name: "the cap breaks a tie on score by the order they arrived",
			entries: []trailerEntry{
				namedTrailer(providerBlockArchive, "k1", "One", 50, "2020-01-01"),
				namedTrailer(providerBlockArchive, "k2", "Two", 50, "2020-01-01"),
				namedTrailer(providerBlockArchive, "k3", "Three", 50, "2020-01-01"),
				namedTrailer(providerBlockArchive, "k4", "Four", 50, "2020-01-01"),
				namedTrailer(providerBlockArchive, "k5", "Five", 50, "2020-01-01"),
				namedTrailer(providerBlockArchive, "k6", "Six", 50, "2020-01-01"),
			},
			want: []string{"k1", "k2", "k3", "k4", "k5"},
		},
		{
			name: "two providers that name one video keep both",
			entries: []trailerEntry{
				namedTrailer(providerBlockArchive, "arc", "Dune Official Trailer", 40, "2020-01-01"),
				namedTrailer(providerBlockPeerTube, "pt", "Dune Official Trailer", 90, "2021-01-01"),
			},
			want: []string{"arc", "pt"},
		},
		{
			name: "an empty list",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var keys []string
			for _, entry := range trimTrailers(test.entries) {
				keys = append(keys, entry.Key)
			}

			if !slices.Equal(keys, test.want) {
				t.Errorf("the trim leaves %v, want %v", keys, test.want)
			}
		})
	}
}

// The list one title records is the trimmed list.
func TestTheTrailerFactRecordsTheTrimmedList(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Signal (2014)"
	seedTrailerGap(t, catalog, root, folder)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	var held []trailerEntry
	for _, key := range []string{"tcm1", "tcm2", "tcm3", "tcm4", "tcm5", "tcm6"} {
		held = append(held, namedTrailer(providerBlockArchive, key, "The Signal", 40, "2019-03-02"))
	}
	line := trailerLineOf(scriptedTrailers{block: providerBlockArchive, entries: held})

	work.trailerOne(t.Context(), line, trailerItem(t, catalog))

	ledger := artLedger(t, filepath.Join(root, folder), factTrailer)
	if len(ledger.Trailers) != 1 || ledger.Trailers[0].Key != "tcm1" {
		t.Errorf("the ledger holds %+v, want the one entry the trim left", ledger.Trailers)
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

// The languages the household asked for reach the ask, because the score
// reads them and a container holds no API credential to read the Library
// itself.
func TestATrailerAskCarriesTheLanguagesOfTheLibrary(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerGap(t, catalog, root, "The Signal (2014)")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	asked := &askedTrailerTitle{}
	t.Setenv(libraryLanguagesVariable, "en-US, fr,")

	if err := work.trailerGap(t.Context(), trailerLineOf(asked)); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(asked.title.languages, []string{"en-US", "fr"}) {
		t.Errorf("the ask carried %v, want the languages the variable names", asked.title.languages)
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
