package main

// PROSE: this file is the identity ladder: the exact tests a title climbs
// before liken writes an id, and why no rung carries a score.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PROSE: how far a provider's runtime may sit from the file's and still count
// as the same work, which the plan leaves for the drill to settle.
const runtimeMargin = 5 * time.Minute

// PROSE: the reason an id was written, which the ledger records so a person
// reads what the answer rested on.
const (
	reasonTitle            = "title"
	reasonTitleAndYear     = "title and year"
	reasonTitleAndNearYear = "title and a year on either side"
)

// PROSE: says why the runtime rung renames the reason it climbed from.
var runtimeReasons = map[string]string{
	reasonTitle:            "title and runtime",
	reasonTitleAndYear:     "title, year, and runtime",
	reasonTitleAndNearYear: "title, a year on either side, and runtime",
}

// PROSE: what the ladder is asked about: the kind, the clues the name gave,
// and the runtime the probe measured, which is zero where none was read.
type identitySearch struct {
	kind     string
	title    string
	year     int
	duration time.Duration
}

// PROSE: what the ladder answers: an id with the reason for it, or the
// candidates a person chooses from.
type identityAnswer struct {
	id         int
	reason     string
	candidates []likenCandidate
}

// PROSE: one result the title test kept, with the runtime where the ladder
// read it.
type identityMatch struct {
	result  tmdbResult
	runtime time.Duration
}

// PROSE: the ladder itself, rung by rung, with the reason each rung carries.
func climbIdentityLadder(ctx context.Context, client *tmdbClient, search identitySearch) (identityAnswer, error) {
	matched, err := searchOnYear(ctx, client, search, search.year)
	if err != nil {
		return identityAnswer{}, err
	}
	reason := reasonTitleAndYear
	if search.year == 0 {
		reason = reasonTitle
	}

	if len(matched) == 0 && search.year > 0 {
		matched, err = searchNeighbouringYears(ctx, client, search)
		if err != nil {
			return identityAnswer{}, err
		}
		reason = reasonTitleAndNearYear
	}
	if len(matched) == 1 {
		return identityAnswer{id: matched[0].result.ID, reason: reason}, nil
	}
	if len(matched) > 1 && search.duration > 0 {
		matched, err = readRuntimes(ctx, client, search.kind, matched)
		if err != nil {
			return identityAnswer{}, err
		}
		if near := withinRuntime(matched, search.duration); len(near) == 1 {
			return identityAnswer{id: near[0].result.ID, reason: runtimeReasons[reason]}, nil
		}
	}
	return identityAnswer{candidates: candidatesFrom(matched, search)}, nil
}

// PROSE: rung one: the title as the provider spells it, or as it was first
// released, and the year the name carried.
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

// PROSE: rung three, and why it exists: a December opening abroad carries a
// different year from the one the release name states.
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

// PROSE: rung four's one call per candidate, which is why the rung runs only
// where the title and the year left several.
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

// PROSE: says why a provider that states no runtime is never kept by this rung.
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

// PROSE: the receipt on each candidate, which says what matched and what did
// not, so a person chooses without running the search again.
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

// PROSE: says why the receipt states the distance and never a score.
func runtimeReceipt(off time.Duration) string {
	off = absDuration(off).Round(time.Minute)
	if off == 0 {
		return "match"
	}
	return fmt.Sprintf("%d minutes off", int(off.Minutes()))
}

// PROSE: the articles the normalization drops, because a provider and a
// release name disagree about them.
var titleArticles = map[string]bool{"the": true, "a": true, "an": true}

// PROSE: the roman numerals a sequel carries, and why the numeral one is not
// among them.
var romanNumerals = map[string]int{
	"ii": 2, "iii": 3, "iv": 4, "v": 5, "vi": 6, "vii": 7,
	"viii": 8, "ix": 9, "x": 10, "xi": 11, "xii": 12, "xiii": 13,
}

// PROSE: the one normalization both sides of a title test run through: case,
// accents, punctuation, the leading article, and the roman numerals.
func normalizeTitle(title string) string {
	var folded strings.Builder
	for _, r := range strings.ToLower(title) {
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
