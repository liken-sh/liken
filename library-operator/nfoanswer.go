package main

// The seam between one nfo fact and the providers that can answer it. Two
// rules from the plan: a single value takes the first provider in the
// Library's sources that answers, and a set is the union of every provider
// that answers, in that same order.

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// What a fact knows about a title before it asks: the kind of library it sits
// in, and every id its sidecar carries. A provider keys on the id it knows,
// so the ids the identity fact wrote are what make the other providers
// reachable.
type titleRef struct {
	kind string
	ids  providerIDs
}

// One credited person as a fact writes them: the name, the part, the billing
// order, and the provider's own picture of them, which the people wave reads.
type creditedActor struct {
	Name  string
	Role  string
	Order int
	Thumb string
}

// One site's score and the count of votes behind it. A count of zero means
// the provider stated none.
type titleRating struct {
	Value float64
	Votes int
}

// One answer holds every value the nfo facts of this wave can write. A
// provider fills the fields of the fact it was asked for and leaves the rest
// empty.
type factAnswer struct {
	Plot           string
	Tagline        string
	Genres         []string
	Studios        []string
	Premiered      string
	RuntimeMinutes int
	Certification  string
	Rating         *titleRating
	Cast           []creditedActor
}

// One provider block, asked for one fact of one title. It answers false where
// the provider holds nothing for that title, which is not an error. A source
// naming a block this image cannot ask is skipped and never fails a run.
type answerer interface {
	providerBlock() string
	serves(fact string) bool
	answer(ctx context.Context, fact string, title titleRef) (factAnswer, bool, error)
}

// A provider that states its day's calls are spent. A container stops asking
// that provider for the rest of the run, leaves the remaining titles their
// gaps, and logs the count it left, so no container sleeps for hours inside a
// Job.
var errDailyLimit = errors.New("the provider has spent its calls for the day")

// One provider's answer with the block that gave it, so the ledger records
// which provider answered.
type providerAnswer struct {
	block  string
	answer factAnswer
}

// The merge: a single value takes the first answer that holds one, and a set
// takes every answer's values in order, with a repeat dropped. The names it
// returns are the providers whose values reached the merged answer.
func mergeAnswers(fact string, answers []providerAnswer) (factAnswer, providerNames) {
	merged := factAnswer{}
	var names providerNames
	note := func(block string) {
		if !slices.Contains(names, block) {
			names = append(names, block)
		}
	}
	_, rating := ratingSites[fact]
	for _, held := range answers {
		switch {
		case rating:
			if merged.Rating == nil && held.answer.Rating != nil {
				merged.Rating = held.answer.Rating
				note(held.block)
			}
		case fact == factOverview:
			mergeOverview(&merged, held, note)
		case fact == factCertification:
			if merged.Certification == "" && held.answer.Certification != "" {
				merged.Certification = held.answer.Certification
				note(held.block)
			}
		case fact == factCredits:
			mergeCast(&merged, held, note)
		}
	}
	return merged, names
}

// Which fields of the overview are single values and which are sets. They
// merge by the two rules together in one pass.
func mergeOverview(merged *factAnswer, held providerAnswer, note func(string)) {
	if merged.Plot == "" && held.answer.Plot != "" {
		merged.Plot = held.answer.Plot
		note(held.block)
	}
	if merged.Tagline == "" && held.answer.Tagline != "" {
		merged.Tagline = held.answer.Tagline
		note(held.block)
	}
	if merged.Premiered == "" && held.answer.Premiered != "" {
		merged.Premiered = held.answer.Premiered
		note(held.block)
	}
	if merged.RuntimeMinutes == 0 && held.answer.RuntimeMinutes > 0 {
		merged.RuntimeMinutes = held.answer.RuntimeMinutes
		note(held.block)
	}
	merged.Genres = unionOf(merged.Genres, held.answer.Genres, held.block, note)
	merged.Studios = unionOf(merged.Studios, held.answer.Studios, held.block, note)
}

func unionOf(held, adding []string, block string, note func(string)) []string {
	for _, value := range adding {
		if value = strings.TrimSpace(value); value == "" || slices.Contains(held, value) {
			continue
		}
		held = append(held, value)
		note(block)
	}
	return held
}

// The cast is a set keyed by the person's name, and the billing order is the
// place in the union, so two providers make one list a player reads in order.
func mergeCast(merged *factAnswer, held providerAnswer, note func(string)) {
	for _, actor := range held.answer.Cast {
		if actor.Name = strings.TrimSpace(actor.Name); actor.Name == "" || namedInCast(merged.Cast, actor.Name) {
			continue
		}
		actor.Order = len(merged.Cast)
		merged.Cast = append(merged.Cast, actor)
		note(held.block)
	}
}

func namedInCast(cast []creditedActor, name string) bool {
	for _, held := range cast {
		if strings.EqualFold(held.Name, name) {
			return true
		}
	}
	return false
}

// An answer with nothing in it for the fact asked is a miss with a date and
// not a write.
func answersFact(fact string, answer factAnswer) bool {
	if _, rating := ratingSites[fact]; rating {
		return answer.Rating != nil
	}
	switch fact {
	case factOverview:
		return answer.Plot != "" || answer.Tagline != "" || answer.Premiered != "" ||
			answer.RuntimeMinutes > 0 || len(answer.Genres) > 0 || len(answer.Studios) > 0
	case factCertification:
		return answer.Certification != ""
	case factCredits:
		return len(answer.Cast) > 0
	}
	return false
}
