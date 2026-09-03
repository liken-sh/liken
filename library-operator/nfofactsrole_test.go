package main

// what these tests read: the nfo container fills one fact of one title from the
// providers in order, records who answered, and leaves a group another writer
// changed.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// a provider block a test scripts: what it serves, what it answers per fact,
// and the error it gives instead.
type fakeAnswerer struct {
	name    string
	facts   []string
	answers map[string]factAnswer
	err     error
	asked   int
}

func (f *fakeAnswerer) providerBlock() string   { return f.name }
func (f *fakeAnswerer) serves(fact string) bool { return slices.Contains(f.facts, fact) }

func (f *fakeAnswerer) answer(_ context.Context, fact string, _ titleRef) (factAnswer, bool, error) {
	f.asked++
	if f.err != nil {
		return factAnswer{}, false, f.err
	}
	answer, held := f.answers[fact]
	return answer, held, nil
}

func lineOf(answerers ...answerer) *answerLine {
	return &answerLine{answerers: answerers, spent: map[string]bool{}}
}

// one title with a provider id and a sidecar, which is the shape of an nfo gap.
func seedNFOGap(t *testing.T, catalog *Catalog, root, folder, id string) string {
	t.Helper()
	sidecar := filepath.Join(root, folder, movieSidecarName)
	writeFile(t, sidecar, `<?xml version="1.0" encoding="utf-8"?>
<movie>
  <title>Winter Harbour</title>
  <year>2011</year>
  <uniqueid type="tmdb" default="true">4242</uniqueid>
</movie>
`)
	seed := &walkResult{movies: []movieRow{{
		Id: id, Library: "house/movies", Kind: libraryKindMovies,
		Path: folder, Title: "Winter Harbour", Released: "2011",
	}}}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	return sidecar
}

func harbourAnswers() map[string]factAnswer {
	return map[string]factAnswer{
		factOverview: {
			Plot: "A keeper watches the ice.", Tagline: "The ice remembers.",
			Genres: []string{"Drama"}, Studios: []string{"Harbour Pictures"},
			Premiered: "2011-05-06", RuntimeMinutes: 101,
		},
		factCertification:        {Certification: "PG-13"},
		factRatingTMDb:           {Rating: &titleRating{Value: 8.4, Votes: 1234}},
		factRatingIMDb:           {Rating: &titleRating{Value: 7.9, Votes: 5678}},
		factRatingRottenTomatoes: {Rating: &titleRating{Value: 91}},
		factRatingMetacritic:     {Rating: &titleRating{Value: 76}},
		factCredits:              {Cast: []creditedActor{{Name: "Nora Vance", Role: "Captain"}}},
	}
}

func TestAnNFOFactWritesItsGroupAndSaysWhoAnswered(t *testing.T) {
	for _, fact := range nfoFacts {
		t.Run(fact, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			folder := "Winter Harbour (2011)"
			sidecar := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}

			if err := work.nfoGap(t.Context(), fact, lineOf(fake)); err != nil {
				t.Fatal(err)
			}

			hash, err := groupHash([]byte(readFileString(t, sidecar)), nfoGroup(fact))
			if err != nil {
				t.Fatal(err)
			}
			ledger, err := readLikenLedger(filepath.Join(root, folder), fact)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.Items) != 1 || ledger.Items[0].Wrote != hash {
				t.Fatalf("items = %+v, want one with the hash of the group in the sidecar", ledger.Items)
			}
			if got := ledger.Items[0].Provider; len(got) != 1 || got[0] != "tmdb" {
				t.Errorf("provider = %v, want the block that answered", got)
			}
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
				t.Errorf("attempts = %+v, want one that found the fact", ledger.Attempts)
			}
		})
	}
}

// A single value takes the first provider that answers, and a set takes the
// union of every provider that answers, both in the order of the sources.
func TestTheMergeTakesTheFirstValueAndTheUnionOfASet(t *testing.T) {
	first := providerAnswer{block: "one", answer: factAnswer{
		Genres: []string{"Drama"}, Studios: []string{"Harbour Pictures"},
		Cast: []creditedActor{{Name: "Nora Vance", Role: "Captain"}},
	}}
	second := providerAnswer{block: "two", answer: factAnswer{
		Plot: "A keeper watches the ice.", Certification: "PG-13",
		Rating: &titleRating{Value: 8.4},
		Genres: []string{"Drama", "Thriller"},
		Cast:   []creditedActor{{Name: "Nora Vance"}, {Name: "Ada Ferris", Role: "Keeper"}},
	}}
	third := providerAnswer{block: "three", answer: factAnswer{
		Plot: "A plot no reader sees.", Certification: "R",
		Rating: &titleRating{Value: 1},
	}}

	cases := []struct {
		fact  string
		check func(*testing.T, factAnswer)
		names []string
	}{
		{
			fact:  factOverview,
			names: []string{"one", "two"},
			check: func(t *testing.T, merged factAnswer) {
				if merged.Plot != "A keeper watches the ice." {
					t.Errorf("plot = %q, want the first answer that held one", merged.Plot)
				}
				if !slices.Equal(merged.Genres, []string{"Drama", "Thriller"}) {
					t.Errorf("genres = %v, want the union in source order", merged.Genres)
				}
			},
		},
		{
			fact:  factCertification,
			names: []string{"two"},
			check: func(t *testing.T, merged factAnswer) {
				if merged.Certification != "PG-13" {
					t.Errorf("certification = %q, want the first answer", merged.Certification)
				}
			},
		},
		{
			fact:  factRatingTMDb,
			names: []string{"two"},
			check: func(t *testing.T, merged factAnswer) {
				if merged.Rating == nil || merged.Rating.Value != 8.4 {
					t.Errorf("rating = %+v, want the first answer", merged.Rating)
				}
			},
		},
		{
			fact:  factCredits,
			names: []string{"one", "two"},
			check: func(t *testing.T, merged factAnswer) {
				if len(merged.Cast) != 2 || merged.Cast[0].Name != "Nora Vance" || merged.Cast[1].Name != "Ada Ferris" {
					t.Fatalf("cast = %+v, want the union with one entry per person", merged.Cast)
				}
				if merged.Cast[0].Role != "Captain" || merged.Cast[1].Order != 1 {
					t.Errorf("cast = %+v, want the first role and the billing order of the union", merged.Cast)
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			merged, names := mergeAnswers(test.fact, []providerAnswer{first, second, third})

			test.check(t, merged)
			if !slices.Equal([]string(names), test.names) {
				t.Errorf("providers = %v, want %v", names, test.names)
			}
		})
	}
}

// Two providers of one fact: the plot comes from the first source in order
// that answers, and the genres are the union of both.
func TestTwoProvidersAnswerOneFactByTheTwoRules(t *testing.T) {
	tmdb := providerAnswer{block: providerBlockTMDb, answer: factAnswer{
		Plot: "A keeper watches the ice.", Genres: []string{"Drama"},
	}}
	omdb := providerAnswer{block: providerBlockOMDb, answer: factAnswer{
		Plot: "A plot no reader sees.", Certification: "PG-13",
		Genres: []string{"Drama", "Thriller"},
	}}

	overview, names := mergeAnswers(factOverview, []providerAnswer{tmdb, omdb})
	certification, whoRated := mergeAnswers(factCertification, []providerAnswer{tmdb, omdb})

	if overview.Plot != "A keeper watches the ice." {
		t.Errorf("plot = %q, want the plot of the first source that held one", overview.Plot)
	}
	if !slices.Equal(overview.Genres, []string{"Drama", "Thriller"}) {
		t.Errorf("genres = %v, want the union in source order", overview.Genres)
	}
	if !slices.Equal([]string(names), []string{providerBlockTMDb, providerBlockOMDb}) {
		t.Errorf("providers = %v, want both", names)
	}
	if certification.Certification != "PG-13" {
		t.Errorf("certification = %q, want the one the second source held", certification.Certification)
	}
	if !slices.Equal([]string(whoRated), []string{providerBlockOMDb}) {
		t.Errorf("providers = %v, want the source that answered", whoRated)
	}
}

// Each site's rating is a fact of its own, so the ratings of two sites from
// two providers sit side by side in one ratings block.
func TestEachSitesRatingMergesOnItsOwn(t *testing.T) {
	tmdb := providerAnswer{block: providerBlockTMDb, answer: factAnswer{
		Rating: &titleRating{Value: 8.4, Votes: 1234},
	}}
	omdb := providerAnswer{block: providerBlockOMDb, answer: factAnswer{
		Rating: &titleRating{Value: 7.9, Votes: 5678},
	}}

	for fact, answers := range map[string][]providerAnswer{
		factRatingTMDb:           {tmdb},
		factRatingIMDb:           {omdb},
		factRatingRottenTomatoes: {omdb},
		factRatingMetacritic:     {omdb},
	} {
		t.Run(fact, func(t *testing.T) {
			merged, names := mergeAnswers(fact, answers)

			if merged.Rating == nil || merged.Rating.Value != answers[0].answer.Rating.Value {
				t.Fatalf("rating = %+v, want the one the provider answered", merged.Rating)
			}
			if !slices.Equal([]string(names), []string{answers[0].block}) {
				t.Errorf("providers = %v, want %s", names, answers[0].block)
			}
		})
	}
}

// A group another writer changed since this fact wrote it stops the fact for
// that title, and the sidecar keeps what that writer left.
func TestAFactLeavesAGroupAnotherWriterHolds(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	sidecar := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	work, log := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}
	if err := work.nfoGap(t.Context(), factOverview, lineOf(fake)); err != nil {
		t.Fatal(err)
	}
	byHand := strings.Replace(readFileString(t, sidecar),
		"<plot>A keeper watches the ice.</plot>", "<plot>A plot a person wrote.</plot>", 1)
	writeFile(t, sidecar, byHand)

	if err := work.nfoGap(t.Context(), factOverview, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	if held := readFileString(t, sidecar); !strings.Contains(held, "A plot a person wrote.") {
		t.Errorf("the fact wrote over another writer's plot:\n%s", held)
	}
	ledger, err := readLikenLedger(filepath.Join(root, folder), factOverview)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFight {
		t.Fatalf("attempts = %+v, want one fight", ledger.Attempts)
	}
	if !strings.Contains(log.String(), "another writer holds") {
		t.Errorf("log = %q, want the line that names the fight", log.String())
	}
}

// A provider that states its day is spent answers no more titles in this run,
// and the titles it did not reach keep their gaps.
func TestADailyLimitStopsTheProviderForTheRestOfTheRun(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	for at := range 3 {
		folder := fmt.Sprintf("Winter Harbour %d (2011)", at)
		seedNFOGap(t, catalog, root, folder, fmt.Sprintf("movie:tmdb:%d", 4242+at))
	}
	work, log := testEnricher(t, libraryKindMovies, root, catalog)
	spent := &fakeAnswerer{name: "omdb", facts: nfoFacts, err: errDailyLimit}

	if err := work.nfoGap(t.Context(), factCertification, lineOf(spent)); err != nil {
		t.Fatal(err)
	}

	if spent.asked != 1 {
		t.Errorf("the provider was asked %d times, want the one that spent its day", spent.asked)
	}
	if !strings.Contains(log.String(), "left the certification of 3 titles") {
		t.Errorf("log = %q, want the count it left", log.String())
	}
	for at := range 3 {
		folder := fmt.Sprintf("Winter Harbour %d (2011)", at)
		ledger, err := readLikenLedger(filepath.Join(root, folder), factCertification)
		if err != nil {
			t.Fatal(err)
		}
		if len(ledger.Attempts) != 0 {
			t.Errorf("attempts = %+v, want the gap left as it was", ledger.Attempts)
		}
	}
}

// A provider that spends its day leaves the others to answer, and the ledger
// names the one that did.
func TestASpentProviderLeavesTheNextOneToAnswer(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	spent := &fakeAnswerer{name: "omdb", facts: nfoFacts, err: errDailyLimit}
	answering := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}

	if err := work.nfoGap(t.Context(), factCertification, lineOf(spent, answering)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factCertification)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Items) != 1 || len(ledger.Items[0].Provider) != 1 || ledger.Items[0].Provider[0] != "tmdb" {
		t.Fatalf("items = %+v, want the provider that answered", ledger.Items)
	}
}

// A title no provider holds the fact for is a miss with a date, so the fact
// asks again after the retry interval and not on the next run.
func TestAFactWithNoAnswerRecordsAMiss(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	silent := &fakeAnswerer{name: "tmdb", facts: nfoFacts}

	if err := work.nfoGap(t.Context(), factOverview, lineOf(silent)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factOverview)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptNothing {
		t.Errorf("attempts = %+v, want one miss", ledger.Attempts)
	}
	if len(ledger.Items) != 0 {
		t.Errorf("items = %+v, want none", ledger.Items)
	}
}

// A container the operator stood with no provider key fails before it writes
// anything, so the Job says what the pod is missing.
func TestAnNFOContainerWithNoProviderKeyFails(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), nil)
	work.providers = lineOf()

	err := work.nfoFact(t.Context(), factOverview)

	if err == nil {
		t.Fatal("the fact reported no error, want one")
	}
}

// A sidecar this container cannot read, and a provider that refuses, both
// record an error attempt, and the next run tries again.
func TestAFactRecordsAnErrorAndCarriesOn(t *testing.T) {
	cases := []struct {
		name    string
		sidecar string
		fail    error
	}{
		{name: "a sidecar that is not XML", sidecar: "this is not xml <<<"},
		{name: "a provider that refuses", fail: errRefused},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			folder := "Winter Harbour (2011)"
			sidecar := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
			if test.sidecar != "" {
				writeFile(t, sidecar, test.sidecar)
			}
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers(), err: test.fail}

			if err := work.nfoGap(t.Context(), factOverview, lineOf(fake)); err != nil {
				t.Fatal(err)
			}

			ledger, err := readLikenLedger(filepath.Join(root, folder), factOverview)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
				t.Fatalf("attempts = %+v, want one error", ledger.Attempts)
			}
			if held := readFileString(t, sidecar); strings.Contains(held, "A keeper watches the ice.") {
				t.Errorf("the fact wrote a plot into a sidecar it could not read:\n%s", held)
			}
		})
	}
}

// errRefused stands for any answer a provider gives that is not an answer.
var errRefused = fmt.Errorf("the provider refused the key")

// The container builds an answerer for every block whose key reached it, and
// none for a block with no key.
func TestTheAnswerLineHoldsTheBlocksWithAKey(t *testing.T) {
	cases := []struct {
		name   string
		blocks []string
		keys   map[string]string
		want   int
		block  string
	}{
		{
			name: "a key for TMDb", blocks: []string{providerBlockTMDb},
			keys: map[string]string{tmdbTokenVariable: "a-token"}, want: 1, block: providerBlockTMDb,
		},
		{
			name: "a key for OMDb", blocks: []string{providerBlockOMDb},
			keys: map[string]string{providerTokenVariable(providerBlockOMDb): "a-token"},
			want: 1, block: providerBlockOMDb,
		},
		{
			name: "TVmaze, which takes no key", blocks: []string{providerBlockTVmaze},
			want: 1, block: providerBlockTVmaze,
		},
		{name: "no key at all", blocks: []string{providerBlockTMDb, providerBlockOMDb}, want: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			line := newAnswerLine(test.blocks,
				func(name string) string { return test.keys[name] })

			if len(line.answerers) != test.want {
				t.Fatalf("answerers = %d, want %d", len(line.answerers), test.want)
			}
			if test.want > 0 && line.answerers[0].providerBlock() != test.block {
				t.Errorf("block = %q, want %q", line.answerers[0].providerBlock(), test.block)
			}
		})
	}
}

// A block a source names that this image cannot ask is skipped, and the run
// carries on with the blocks it can.
func TestAnUnknownBlockAddsNoAnswerer(t *testing.T) {
	line := newAnswerLine([]string{"fanart", providerBlockTMDb},
		func(name string) string { return map[string]string{tmdbTokenVariable: "a-token"}[name] })

	if len(line.answerers) != 1 || line.answerers[0].providerBlock() != providerBlockTMDb {
		t.Errorf("answerers = %+v, want the one block this image asks", line.answerers)
	}
}

// A provider that does not serve the fact is not asked, and a fact no live
// provider serves leaves every title in its gap.
func TestAProviderThatDoesNotServeAFactIsNotAsked(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	work, log := testEnricher(t, libraryKindMovies, root, catalog)
	elsewhere := &fakeAnswerer{name: "fanart", facts: []string{factOverview}, answers: harbourAnswers()}

	if err := work.nfoGap(t.Context(), factCertification, lineOf(elsewhere)); err != nil {
		t.Fatal(err)
	}

	if elsewhere.asked != 0 {
		t.Errorf("the provider was asked %d times, want none", elsewhere.asked)
	}
	if !strings.Contains(log.String(), "left the certification of 1 titles") {
		t.Errorf("log = %q, want the count it left", log.String())
	}
}

// A Job narrowed to one folder fills the facts of that folder alone.
func TestANarrowedJobFillsItsOwnFolderAlone(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedNFOGap(t, catalog, root, "Winter Harbour (2011)", "movie:tmdb:4242")
	seedNFOGap(t, catalog, root, "Summer Harbour (2012)", "movie:tmdb:4243")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.scope = "Winter Harbour (2011)"
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}

	if err := work.nfoGap(t.Context(), factOverview, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	if fake.asked != 1 {
		t.Errorf("the provider was asked %d times, want the one folder's", fake.asked)
	}
}

// A sidecar the container cannot open at all records an error attempt, and the
// next run tries again.
func TestASidecarTheContainerCannotOpenRecordsAnError(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	sidecar := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sidecar, 0o755); err != nil {
		t.Fatal(err)
	}
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}

	if err := work.nfoGap(t.Context(), factOverview, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factOverview)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
		t.Errorf("attempts = %+v, want one error", ledger.Attempts)
	}
}

// A ledger the container cannot read is an error attempt too, because the fight
// check is what stands between this fact and another writer's bytes.
func TestALedgerTheContainerCannotReadRecordsAnError(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	writeFile(t, filepath.Join(root, folder, likenDirectory, likenLedgerName(factOverview)), "items: [")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}

	if err := work.nfoGap(t.Context(), factOverview, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	if held := readFileString(t, filepath.Join(root, folder, movieSidecarName)); strings.Contains(held, "A keeper") {
		t.Errorf("the fact wrote without the fight check:\n%s", held)
	}
}

// A provider that answers with nothing in it for the fact asked is a miss.
func TestAnAnswerWithNothingInItIsAMiss(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	empty := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: map[string]factAnswer{factOverview: {}}}

	if err := work.nfoGap(t.Context(), factOverview, lineOf(empty)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factOverview)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptNothing {
		t.Errorf("attempts = %+v, want one miss", ledger.Attempts)
	}
}

// The container builds its providers from the source order and the keys the
// operator mounted, once for every fact it runs.
func TestTheNFOFactBuildsItsProvidersOnce(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	t.Setenv(librarySourcesVariable, providerBlockTMDb)
	t.Setenv(tmdbTokenVariable, "a-token")

	if err := work.nfoFact(t.Context(), factOverview); err != nil {
		t.Fatal(err)
	}

	first := work.providers
	if first == nil || len(first.answerers) != 1 {
		t.Fatalf("providers = %+v, want the one block with a key", first)
	}
	if err := work.nfoFact(t.Context(), factCredits); err != nil {
		t.Fatal(err)
	}
	if work.providers != first {
		t.Errorf("the second fact built its own providers, want the container's own")
	}
}

// A volume the container cannot write records an error attempt and leaves the
// sidecar as it was.
func TestAVolumeTheContainerCannotWriteRecordsAnError(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	sidecar := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	before := readFileString(t, sidecar)
	if err := os.Chmod(filepath.Join(root, folder), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, folder), 0o755) })
	work, log := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}

	if err := work.nfoGap(t.Context(), factOverview, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	if held := readFileString(t, sidecar); held != before {
		t.Errorf("the sidecar changed:\n%s", held)
	}
	if !strings.Contains(log.String(), "could not write the overview") {
		t.Errorf("log = %q, want the line that names the write it could not make", log.String())
	}
}

// The container asks its providers in the order the Library's sources name
// them, and skips a block this image has no answerer for.
func TestTheAnswerLineTakesTheOrderOfTheSources(t *testing.T) {
	cases := []struct {
		name    string
		sources string
		want    []string
	}{
		{name: "the one block this image asks", sources: "tmdb", want: []string{providerBlockTMDb}},
		{name: "a block with no answerer yet", sources: "omdb,fanart", want: []string{}},
		{name: "the blocks in the order they are named", sources: "omdb,tmdb", want: []string{providerBlockTMDb}},
		{
			name: "a block that takes no key", sources: "tvmaze,tmdb",
			want: []string{providerBlockTVmaze, providerBlockTMDb},
		},
		{name: "no sources at all", sources: "", want: []string{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			line := newAnswerLine(commaNames(test.sources),
				func(name string) string { return map[string]string{tmdbTokenVariable: "a-token"}[name] })

			blocks := []string{}
			for _, one := range line.answerers {
				blocks = append(blocks, one.providerBlock())
			}
			if !slices.Equal(blocks, test.want) {
				t.Errorf("blocks = %v, want %v", blocks, test.want)
			}
		})
	}
}

// Every nfo fact runs the same loop over its own gap, so the container's map
// holds one run per fact.
func TestEveryNFOFactRunsTheSameLoop(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	t.Setenv(librarySourcesVariable, providerBlockTMDb)
	t.Setenv(tmdbTokenVariable, "a-token")

	for _, fact := range nfoFacts {
		if err := nfoFactRun(fact)(t.Context(), work); err != nil {
			t.Errorf("the %s fact failed: %v", fact, err)
		}
	}
}

// A sidecar with the actors Jellyfin wrote, which is the shape of every title
// of a library it filled before this operator existed.
func writeJellyfinActors(t *testing.T, sidecar string) {
	t.Helper()
	writeFile(t, sidecar, `<?xml version="1.0" encoding="utf-8"?>
<movie>
  <title>Winter Harbour</title>
  <year>2011</year>
  <uniqueid type="tmdb" default="true">4242</uniqueid>
  <actor>
    <name>Nora Vance</name>
    <role>Captain</role>
    <order>0</order>
  </actor>
  <actor>
    <name>Ivo Brandt</name>
    <role>The Mate</role>
    <order>1</order>
  </actor>
  <actor>
    <name>Rea Solberg</name>
    <order>2</order>
  </actor>
</movie>
`)
}

// The union of the sidecar's actors and the provider's cast: the sidecar's
// people keep their place and their part, they gain the ids the provider gave,
// the provider's own people follow, and every one of them gets an entry in
// .contributors/.
func TestTheCreditsFactKeepsTheActorsTheSidecarHolds(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	sidecar := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	writeJellyfinActors(t, sidecar)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: map[string]factAnswer{
		factCredits: {Cast: []creditedActor{
			{Name: "Nora Vance", Role: "The Keeper", IDs: providerIDs{"tmdb": "31"}},
			{Name: "Ada Ferris", Role: "The Pilot", IDs: providerIDs{"tmdb": "77"}},
		}},
	}}

	if err := work.nfoGap(t.Context(), factCredits, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factCredits)
	if err != nil {
		t.Fatal(err)
	}
	want := []creditEntry{
		{Name: "Nora Vance", Role: "Captain", Order: 0, Contributor: ".contributors/n/nora-vance"},
		{Name: "Ivo Brandt", Role: "The Mate", Order: 1, Contributor: ".contributors/i/ivo-brandt"},
		{Name: "Rea Solberg", Order: 2, Contributor: ".contributors/r/rea-solberg"},
		{Name: "Ada Ferris", Role: "The Pilot", Order: 3, Contributor: ".contributors/a/ada-ferris"},
	}
	if !slices.Equal(ledger.Credits, want) {
		t.Fatalf("credits = %+v, want %+v", ledger.Credits, want)
	}
	if entry := readFileString(t, filepath.Join(root, ".contributors/n/nora-vance", contributorFileName)); entry !=
		"name: Nora Vance\nids: {tmdb: 31}\n" {
		t.Errorf("contributor.yaml = %q, want the ids the provider gave for the person the sidecar holds", entry)
	}
	if entry := readFileString(t, filepath.Join(root, ".contributors/i/ivo-brandt", contributorFileName)); entry !=
		"name: Ivo Brandt\n" {
		t.Errorf("contributor.yaml = %q, want the person the sidecar alone holds", entry)
	}
	if held := readFileString(t, sidecar); !strings.Contains(held, "<name>Ada Ferris</name>") ||
		!strings.Contains(held, "<name>Ivo Brandt</name>") {
		t.Errorf("the sidecar lost a person of the union:\n%s", held)
	}
}

// The actor group is another writer's until this fact writes it, so the first
// run over a sidecar Jellyfin filled takes the group over and is no fight.
func TestTheFirstCreditsWriteOverAnotherWritersActorsIsNoFight(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	sidecar := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	writeJellyfinActors(t, sidecar)
	work, log := testEnricher(t, libraryKindMovies, root, catalog)
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}

	if err := work.nfoGap(t.Context(), factCredits, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factCredits)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Fatalf("attempts = %+v, want the one that took the group over", ledger.Attempts)
	}
	if strings.Contains(log.String(), "another writer holds") {
		t.Errorf("log = %q, want no fight over a group this fact never wrote", log.String())
	}
}

// The actor elements are rewritten only where the union changed what a player
// reads. credits.yaml and the entries are written either way.
func TestTheCreditsFactRewritesTheActorsOnlyWhenTheCastDiffers(t *testing.T) {
	cases := []struct {
		name    string
		cast    []creditedActor
		changed bool
	}{
		{
			name: "a provider that names the people the sidecar holds",
			cast: []creditedActor{
				{Name: "Nora Vance", Role: "Captain", IDs: providerIDs{"tmdb": "31"}},
				{Name: "Ivo Brandt", Role: "The Mate"},
				{Name: "Rea Solberg"},
			},
		},
		{
			name: "a provider that names the part and the picture the sidecar left empty",
			cast: []creditedActor{
				{Name: "Nora Vance", Role: "Captain"},
				{Name: "Ivo Brandt", Role: "The Mate"},
				{Name: "Rea Solberg", Role: "The Cook", Thumb: "https://example.test/r.jpg"},
			},
			changed: true,
		},
		{
			name: "a provider that names one more person",
			cast: []creditedActor{
				{Name: "Nora Vance", Role: "Captain"},
				{Name: "Ivo Brandt", Role: "The Mate"},
				{Name: "Rea Solberg"},
				{Name: "Ada Ferris", Role: "The Pilot"},
			},
			changed: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			folder := "Winter Harbour (2011)"
			sidecar := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
			writeJellyfinActors(t, sidecar)
			before := readFileString(t, sidecar)
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts,
				answers: map[string]factAnswer{factCredits: {Cast: test.cast}}}

			if err := work.nfoGap(t.Context(), factCredits, lineOf(fake)); err != nil {
				t.Fatal(err)
			}

			if changed := readFileString(t, sidecar) != before; changed != test.changed {
				t.Errorf("the sidecar changed = %v, want %v:\n%s", changed, test.changed,
					readFileString(t, sidecar))
			}
			ledger, err := readLikenLedger(filepath.Join(root, folder), factCredits)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.Credits) != len(test.cast) {
				t.Errorf("credits = %+v, want one entry per person of the union", ledger.Credits)
			}
		})
	}
}

// A title whose providers named no cast is written as an empty credits.yaml
// with the attempt beside it, so the title is not a gap on every run that
// follows.
func TestACreditsAnswerWithNoCastWritesAnEmptyLedger(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	sidecar := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	empty := &fakeAnswerer{name: "tmdb", facts: nfoFacts,
		answers: map[string]factAnswer{factCredits: {}}}

	if err := work.nfoGap(t.Context(), factCredits, lineOf(empty)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, folder), factCredits)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Credits) != 0 {
		t.Errorf("credits = %+v, want none", ledger.Credits)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Fatalf("attempts = %+v, want the one that wrote the empty file", ledger.Attempts)
	}
	if held := readFileString(t, sidecar); strings.Contains(held, "<actor>") {
		t.Errorf("the fact wrote an actor no provider named:\n%s", held)
	}
}

// What the union starts from: the actors the sidecar holds, in its own order,
// with the people it names nothing left out.
func TestTheUnionStartsFromTheActorsTheSidecarHolds(t *testing.T) {
	cases := []struct {
		name     string
		document string
		want     []creditedActor
	}{
		{
			name:     "a sidecar with no actors",
			document: "<movie><title>One</title></movie>",
		},
		{
			name: "the actors another writer left",
			document: "<movie><actor><name>Nora Vance</name><role>Captain</role>" +
				"<thumb>https://example.test/a.jpg</thumb></actor>" +
				"<actor><name>Ivo Brandt</name></actor></movie>",
			want: []creditedActor{
				{Name: "Nora Vance", Role: "Captain", Thumb: "https://example.test/a.jpg", Order: 0},
				{Name: "Ivo Brandt", Order: 1},
			},
		},
		{
			name:     "an actor with no name at all",
			document: "<movie><actor><role>Captain</role></actor></movie>",
		},
		{
			name:     "a document that is not XML",
			document: "this is not xml <<<",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := sidecarCast([]byte(test.document)); !reflect.DeepEqual(got, test.want) {
				t.Errorf("cast = %+v, want %+v", got, test.want)
			}
		})
	}
}
