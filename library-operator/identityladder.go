package main

// identityladder.go is the identity ladder: the exact tests a title climbs
// before liken writes an id. No rung carries a score. A score is a number
// nobody can check, and a reason is a sentence a person reads in the ledger
// next to the id.

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// How far a provider's runtime may sit from the file's and still count as the
// same work. The plan leaves the number for the drill to settle.
const runtimeMargin = 5 * time.Minute

// The name of each test the ladder runs. The ledger records the tests an
// answer passed as its reason, so a person reads what the answer rested on.
const (
	testTitle    = "title"
	testYear     = "year"
	testNearYear = "a year on either side"
	testCountry  = "country"
	testRuntime  = "runtime"
)

// The reason is built from the tests the answer passed, so a new rung adds
// one word and every reason still reads as a sentence.
func reasonFrom(tests ...string) string {
	if len(tests) < 3 {
		return strings.Join(tests, " and ")
	}
	return strings.Join(tests[:len(tests)-1], ", ") + ", and " + tests[len(tests)-1]
}

// What the ladder is asked about: the kind, the clues the name gave, and the
// runtime the probe measured, which is zero where none was read.
type identitySearch struct {
	kind     string
	title    string
	year     int
	duration time.Duration
}

// What the ladder answers: an id with the reason for it, or the candidates a
// person chooses from.
type identityAnswer struct {
	id         int
	reason     string
	candidates []likenCandidate
}

// One result the title test kept, with the runtime where the ladder read it.
type identityMatch struct {
	result  tmdbResult
	runtime time.Duration
}

// The ladder itself, rung by rung. A name with no year climbs on the title
// alone, because such a folder is exactly the sidecar-less case the ladder
// exists for. One survivor is written with its reason. Several survivors go
// to the runtime rung when the probe measured one, and anything else is a
// candidate list.
func climbIdentityLadder(ctx context.Context, client *tmdbClient, search identitySearch) (identityAnswer, error) {
	search, country := readQualifier(search)
	matched, err := searchOnYear(ctx, client, search, search.year)
	if err != nil {
		return identityAnswer{}, err
	}
	tests := []string{testTitle, testYear}
	if search.year == 0 {
		tests = []string{testTitle}
	}

	if len(matched) == 0 && search.year > 0 {
		matched, err = searchNeighbouringYears(ctx, client, search)
		if err != nil {
			return identityAnswer{}, err
		}
		tests = []string{testTitle, testNearYear}
	}
	if kept := fromCountry(matched, country); len(kept) > 0 {
		matched, tests = kept, append(tests, testCountry)
	}
	if len(matched) == 1 {
		return identityAnswer{id: matched[0].result.ID, reason: reasonFrom(tests...)}, nil
	}
	if len(matched) > 1 && search.duration > 0 {
		matched, err = readRuntimes(ctx, client, search.kind, matched)
		if err != nil {
			return identityAnswer{}, err
		}
		if near := withinRuntime(matched, search.duration); len(near) == 1 {
			return identityAnswer{id: near[0].result.ID, reason: reasonFrom(append(tests, testRuntime)...)}, nil
		}
	}
	return identityAnswer{candidates: candidatesFrom(matched, search)}, nil
}

// The qualifier a namer writes after a title to part it from another show of
// the same name: a country, as in Shameless (US), or a year, as in The Office
// (2011). The title reaches the provider without it, because the provider
// names the show Shameless. A country is a test only for a series, where TMDb
// states origin_country.
func readQualifier(search identitySearch) (identitySearch, string) {
	base, qualifier := partTitle(search.title)
	search.title = base
	if year := qualifiedYear(qualifier); year > 0 {
		if search.year == 0 {
			search.year = year
		}
		return search, ""
	}
	if search.kind != libraryKindSeries {
		return search, ""
	}
	return search, countryCode(qualifier)
}

// partTitle cuts a trailing parenthesized qualifier off a title and answers
// with both halves. A title that is one parenthesized group keeps it, because
// the group is the whole name and not a qualifier.
func partTitle(title string) (string, string) {
	trimmed := strings.TrimSpace(title)
	if !strings.HasSuffix(trimmed, ")") {
		return trimmed, ""
	}
	open := strings.LastIndexByte(trimmed, '(')
	if open <= 0 {
		return trimmed, ""
	}
	return strings.TrimSpace(trimmed[:open]), strings.TrimSpace(trimmed[open+1 : len(trimmed)-1])
}

// qualifiedYear reads a four-digit qualifier as a year, and anything else as
// no year.
func qualifiedYear(qualifier string) int {
	if len(qualifier) != 4 {
		return 0
	}
	return leadingYear(qualifier)
}

// countryCode reads a two-letter qualifier as a country code, in the upper
// case TMDb states origin_country in, and anything else as no country.
func countryCode(qualifier string) string {
	if len(qualifier) != 2 {
		return ""
	}
	upper := strings.ToUpper(qualifier)
	for _, letter := range upper {
		if letter < 'A' || letter > 'Z' {
			return ""
		}
	}
	return upper
}

// The country test keeps the results the provider states that origin for. It
// runs only where it keeps one, because a provider that states another origin
// is a fact for a person to read on the candidate, and not a reason to answer
// with nothing.
func fromCountry(matched []identityMatch, country string) []identityMatch {
	if country == "" {
		return nil
	}
	var kept []identityMatch
	for _, match := range matched {
		if slices.Contains(match.result.OriginCountry, country) {
			kept = append(kept, match)
		}
	}
	return kept
}

// Rung one: the title as the provider spells it, or as it was first released,
// and the year the name carried.
func searchOnYear(ctx context.Context, client *tmdbClient, search identitySearch, year int) ([]identityMatch, error) {
	results, err := client.search(ctx, search.kind, search.title, year)
	if err != nil {
		return nil, err
	}
	wanted := normalizeTitle(search.title)
	var matched []identityMatch
	for _, result := range results {
		if normalizeTitle(result.name()) != wanted && normalizeTitle(result.originalName()) != wanted {
			continue
		}
		if year > 0 && result.year() != year {
			continue
		}
		matched = append(matched, identityMatch{result: result})
	}
	return matched, nil
}

// The year on either side, which exists because TMDb states the first release
// anywhere. A December opening abroad carries a different year from the one
// the release name states.
func searchNeighbouringYears(ctx context.Context, client *tmdbClient, search identitySearch) ([]identityMatch, error) {
	held := map[int]bool{}
	var matched []identityMatch
	for _, year := range []int{search.year - 1, search.year + 1} {
		found, err := searchOnYear(ctx, client, search, year)
		if err != nil {
			return nil, err
		}
		for _, match := range found {
			if held[match.result.ID] {
				continue
			}
			held[match.result.ID] = true
			matched = append(matched, match)
		}
	}
	return matched, nil
}

// The runtime rung costs one call per candidate, because a search result
// carries no runtime. So the rung runs only where the title and the year left
// several.
func readRuntimes(ctx context.Context, client *tmdbClient, kind string, matched []identityMatch) ([]identityMatch, error) {
	for at, match := range matched {
		runtime, err := client.runtime(ctx, kind, match.result.ID)
		if err != nil {
			return nil, err
		}
		matched[at].runtime = runtime
	}
	return matched, nil
}

// A provider that states no runtime is never kept by this rung, because a
// missing number is not a match.
func withinRuntime(matched []identityMatch, duration time.Duration) []identityMatch {
	var near []identityMatch
	for _, match := range matched {
		if match.runtime > 0 && absDuration(match.runtime-duration) <= runtimeMargin {
			near = append(near, match)
		}
	}
	return near
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// The receipt on each candidate says what matched and what did not, so a
// person chooses without running the search again.
func candidatesFrom(matched []identityMatch, search identitySearch) []likenCandidate {
	candidates := make([]likenCandidate, 0, len(matched))
	for _, match := range matched {
		candidates = append(candidates, likenCandidate{
			ID:      providerIDs{"tmdb": strconv.Itoa(match.result.ID)},
			Title:   match.result.name(),
			Year:    match.result.year(),
			Receipt: receiptFor(match, search),
		})
	}
	if len(candidates) == 0 {
		return nil
	}
	return candidates
}

func receiptFor(match identityMatch, search identitySearch) map[string]string {
	receipt := map[string]string{"title": "match", "year": yearReceipt(match, search)}
	if search.duration > 0 && match.runtime > 0 {
		receipt["runtime"] = runtimeReceipt(match.runtime - search.duration)
	}
	return receipt
}

func yearReceipt(match identityMatch, search identitySearch) string {
	switch {
	case search.year == 0:
		return "the name carries no year"
	case match.result.year() == search.year:
		return "match"
	default:
		return "no match"
	}
}

// The receipt states the distance in minutes and never a score, so a person
// can check it against the file.
func runtimeReceipt(off time.Duration) string {
	off = absDuration(off).Round(time.Minute)
	if off == 0 {
		return "match"
	}
	return fmt.Sprintf("%d minutes off", int(off.Minutes()))
}

// The articles the normalization drops, because a provider and a release name
// disagree about them.
var titleArticles = map[string]bool{"the": true, "a": true, "an": true}

// The roman numerals a sequel carries. The numeral one is not among them,
// because a title ending in I is a word more often than a number.
var romanNumerals = map[string]int{
	"ii": 2, "iii": 3, "iv": 4, "v": 5, "vi": 6, "vii": 7,
	"viii": 8, "ix": 9, "x": 10, "xi": 11, "xii": 12, "xiii": 13,
}

// The one normalization both sides of a title test run through: case,
// accents, punctuation, the leading article, and the roman numerals.
func normalizeTitle(title string) string {
	base, _ := partTitle(title)
	var folded strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			folded.WriteRune(r)
		default:
			if ascii, held := accentFold[r]; held {
				folded.WriteString(ascii)
				continue
			}
			folded.WriteByte(' ')
		}
	}
	words := strings.Fields(folded.String())
	if len(words) > 1 && titleArticles[words[0]] {
		words = words[1:]
	}
	for at, word := range words {
		if value, held := romanNumerals[word]; held {
			words[at] = strconv.Itoa(value)
		}
	}
	return strings.Join(words, " ")
}
