package main

// What these tests read: what a file's attempt records when some of the
// Library's marks providers answered and some did not, and when that file
// comes back into the gap. A file every provider answered waits the dated
// window. A file one provider was not asked about, or failed on, keeps the
// spans the others answered and comes back on the error window.

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// One provider block that answers each ask in turn from a script, and counts
// how often it was asked. An ask past the end of the script takes its last
// answer.
type sequencedMarks struct {
	block   string
	answers []scriptedMarks
	asked   *int
}

func (s sequencedMarks) providerBlock() string { return s.block }

func (s sequencedMarks) marks(ctx context.Context, file markFile) ([]markEntry, error) {
	answer := s.answers[min(*s.asked, len(s.answers)-1)]
	*s.asked++
	return answer.marks(ctx, file)
}

// TheIntroDB's answer once its allowance for the day is spent.
var spentAllowance = providerStatusError{provider: providerBlockTheIntroDB, path: theintrodbMediaPath,
	status: http.StatusTooManyRequests, body: `{"error":"Usage limit exceeded"}`}

// The three films every case of the run seeds, in the order the gap names
// them.
var partialFilms = []string{"Alien (1979)", "Aliens (1986)", "Alien 3 (1992)"}

// The last attempt and the sources of the spans one film's ledger holds.
func filmRecord(t *testing.T, root, folder string) (likenAttempt, []string) {
	t.Helper()
	ledger := artLedger(t, filepath.Join(root, folder), factMarks)
	if len(ledger.Attempts) != 1 {
		t.Fatalf("%s holds %d attempts, want one", folder, len(ledger.Attempts))
	}
	sources := []string{}
	for _, entry := range ledger.Marks {
		sources = append(sources, entry.Source)
	}
	return ledger.Attempts[0], sources
}

// A run where TheIntroDB answers the first film, spends its allowance on the
// second, and is not asked about the third. IntroDB answers every film. The
// first film holds both answers and waits the dated window. The other two
// hold IntroDB's spans, record a partial attempt, and come back the next
// day, so the next run spends TheIntroDB's allowance on them and not again on
// the first.
func TestASpentAllowanceBringsTheFilesItMissedBackTheNextDay(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	for at, folder := range partialFilms {
		seedMarkedMovie(t, catalog, root, folder, "movie:tmdb:"+strconv.Itoa(600+at), 7000000)
	}
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.library = marksLibrary
	theintrodbAsked, introdbAsked := 0, 0
	line := markLineOf(
		sequencedMarks{block: providerBlockTheIntroDB, asked: &theintrodbAsked, answers: []scriptedMarks{
			{entries: []markEntry{answeredSpan(providerBlockTheIntroDB, markKindCredits, 6500000, 7000000)}},
			{err: spentAllowance},
		}},
		sequencedMarks{block: providerBlockIntroDB, asked: &introdbAsked, answers: []scriptedMarks{
			{entries: []markEntry{answeredSpan(providerBlockIntroDB, markKindCredits, 6490000, 7000000)}},
		}},
	)

	if err := work.marksGap(t.Context(), line); err != nil {
		t.Fatal(err)
	}

	if theintrodbAsked != 2 || introdbAsked != 3 {
		t.Errorf("TheIntroDB was asked %d times and IntroDB %d, want 2 and 3", theintrodbAsked, introdbAsked)
	}
	cases := []struct {
		folder  string
		result  string
		sources []string
		back    bool
	}{
		{folder: partialFilms[0], result: attemptFound,
			sources: []string{providerBlockTheIntroDB, providerBlockIntroDB}},
		{folder: partialFilms[1], result: attemptPartial, sources: []string{providerBlockIntroDB}, back: true},
		{folder: partialFilms[2], result: attemptPartial, sources: []string{providerBlockIntroDB}, back: true},
	}
	nextDay, err := catalog.queryStrings(t.Context(), gapQueries[factMarks],
		gapParams(factMarks, marksLibrary, time.Now().UTC().Add(25*time.Hour), time.Time{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.folder, func(t *testing.T) {
			attempt, sources := filmRecord(t, root, test.folder)
			if attempt.Result != test.result {
				t.Errorf("the attempt is %s, want %s", attempt.Result, test.result)
			}
			if !slices.Equal(sources, test.sources) {
				t.Errorf("the ledger holds spans of %v, want %v", sources, test.sources)
			}
			path := filepath.Join(test.folder, test.folder+".mkv")
			if got := slices.Contains(nextDay, path); got != test.back {
				t.Errorf("the gap the next day holds the film: %v, want %v", got, test.back)
			}
		})
	}
}

// Which answers make an attempt partial: a provider that failed or was not
// asked makes it so, and a provider that answered with no span does not.
func TestAnAttemptIsPartialWhereAProviderGaveNoAnswer(t *testing.T) {
	down := errors.New(`introdb /segments: 502: <html>bad gateway</html>`)
	span := answeredSpan(providerBlockTheIntroDB, markKindIntro, 0, 60000)
	cases := []struct {
		name       string
		introdb    scriptedMarks
		wantResult string
		wantLog    string
	}{
		{name: "both answered", wantResult: attemptFound,
			introdb: scriptedMarks{block: providerBlockIntroDB}},
		{name: "one provider failed", wantResult: attemptPartial, wantLog: "bad gateway",
			introdb: scriptedMarks{block: providerBlockIntroDB, err: down}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			work, log := testEnricher(t, libraryKindMovies, root, nil)
			line := markLineOf(scriptedMarks{block: providerBlockTheIntroDB, entries: []markEntry{span}}, test.introdb)

			work.marksOne(t.Context(), line, "Alien (1979)/Alien (1979).mkv", markFile{episodes: 1})

			attempt, sources := filmRecord(t, root, "Alien (1979)")
			if attempt.Result != test.wantResult {
				t.Errorf("the attempt is %s, want %s", attempt.Result, test.wantResult)
			}
			if !slices.Equal(sources, []string{providerBlockTheIntroDB}) {
				t.Errorf("the ledger holds spans of %v, want TheIntroDB's", sources)
			}
			if !strings.Contains(log.String(), test.wantLog) {
				t.Errorf("log = %q, want the provider's own words", log.String())
			}
		})
	}
}

// A partial attempt holds a file out of the gap for the error window, and a
// whole attempt for the dated one.
func TestAPartialAttemptTakesTheErrorWindow(t *testing.T) {
	cases := []struct {
		name   string
		result string
		ago    time.Duration
		gap    bool
	}{
		{name: "partial, 12 hours ago", result: attemptPartial, ago: 12 * time.Hour},
		{name: "partial, 2 days ago", result: attemptPartial, ago: 48 * time.Hour, gap: true},
		{name: "found, 2 days ago", result: attemptFound, ago: 48 * time.Hour},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			now := time.Now().UTC()
			seedMarksAttempt(t, catalog, []string{""}, test.result, now.Add(-test.ago))

			paths, err := catalog.queryStrings(t.Context(), gapQueries[factMarks],
				gapParams(factMarks, marksLibrary, now, time.Time{}))

			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Contains(paths, marksGapPath); got != test.gap {
				t.Errorf("the gap holds the file: %v, want %v", got, test.gap)
			}
		})
	}
}

// An answer replaces the spans of the sources that answered, and keeps the
// spans of a source that was not asked or failed, because that source's
// silence says nothing about where the credits are. The other files keep
// their places.
func TestReplacedMarksReplacesOnlyTheSourcesThatAnswered(t *testing.T) {
	held := []markEntry{
		{Path: "a.mkv", Kind: markKindIntro, Source: providerBlockTheIntroDB},
		{Path: "b.mkv", Kind: markKindIntro, Source: providerBlockTheIntroDB},
		{Path: "b.mkv", Kind: markKindCredits, Source: providerBlockIntroDB},
		{Path: "c.mkv", Kind: markKindIntro, Source: providerBlockIntroDB},
	}
	cases := []struct {
		name     string
		entry    string
		answered []string
		entries  []markEntry
		want     []string
	}{
		{name: "every source of a file the list holds", entry: "b.mkv",
			answered: []string{providerBlockTheIntroDB, providerBlockIntroDB},
			entries:  []markEntry{{Kind: markKindRecap, Source: providerBlockIntroDB}},
			want:     []string{"a.mkv intro", "b.mkv recap", "c.mkv intro"}},
		{name: "one source of two", entry: "b.mkv",
			answered: []string{providerBlockIntroDB},
			entries:  []markEntry{{Kind: markKindRecap, Source: providerBlockIntroDB}},
			want:     []string{"a.mkv intro", "b.mkv intro", "b.mkv recap", "c.mkv intro"}},
		{name: "a file the list does not hold", entry: "d.mkv",
			answered: []string{providerBlockTheIntroDB},
			entries:  []markEntry{{Kind: markKindPreview, Source: providerBlockTheIntroDB}},
			want:     []string{"a.mkv intro", "b.mkv intro", "b.mkv credits", "c.mkv intro", "d.mkv preview"}},
		{name: "every source answered with no span", entry: "b.mkv",
			answered: []string{providerBlockTheIntroDB, providerBlockIntroDB},
			want:     []string{"a.mkv intro", "c.mkv intro"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := []string{}
			for _, one := range replacedMarks(held, test.entry, test.answered, test.entries) {
				got = append(got, one.Path+" "+one.Kind)
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("marks = %v, want %v", got, test.want)
			}
		})
	}
}
