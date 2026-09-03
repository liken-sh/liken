package main

// What the nfo facts make of OMDb. OMDb answers one lookup per title, keyed
// on the IMDb id the identity fact wrote, and the one answer carries the
// plot, the US certification, and the scores of three sites. A title OMDb
// does not hold is a miss and not an error. A key with no calls left ends
// OMDb's work for the run.

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The word OMDb writes in every field it holds no value for, which is no
// answer at all.
const omdbNotAvailable = "N/A"

// The form OMDb states a release date in, against the ISO date the premiered
// element carries.
const omdbReleasedLayout = "2 Jan 2006"

func omdbValue(value string) string {
	if value = strings.TrimSpace(value); strings.EqualFold(value, omdbNotAvailable) {
		return ""
	}
	return value
}

// The date the premiered element takes, or nothing where OMDb states a date
// this layout does not read.
func omdbPremiered(released string) string {
	day, err := time.Parse(omdbReleasedLayout, omdbValue(released))
	if err != nil {
		return ""
	}
	return day.Format(time.DateOnly)
}

// OMDb states the runtime as minutes with the word after it.
func omdbRuntimeMinutes(runtime string) int {
	minutes, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(omdbValue(runtime), "min")))
	if err != nil || minutes <= 0 {
		return 0
	}
	return minutes
}

// OMDb states the genres as one line, separated by commas.
func omdbGenres(genre string) []string {
	var genres []string
	for _, name := range strings.Split(omdbValue(genre), ",") {
		if name = strings.TrimSpace(name); name != "" {
			genres = append(genres, name)
		}
	}
	return genres
}

// Each site states its score in its own form: 9.2/10, 97%, or 76/100. The
// number in front of the scale is the score, and the scale itself is the max
// the rating element carries.
func omdbScoreValue(value string) (float64, bool) {
	value = strings.TrimSuffix(omdbValue(value), "%")
	if before, _, held := strings.Cut(value, "/"); held {
		value = before
	}
	score, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || score <= 0 {
		return 0, false
	}
	return score, true
}

// OMDb states one site's score in two places, so the first form that reads as
// a number is the answer, and a site it scored nowhere is no answer.
func omdbFirstScore(values ...string) *titleRating {
	for _, value := range values {
		if score, held := omdbScoreValue(value); held {
			return &titleRating{Value: score}
		}
	}
	return nil
}

// OMDb states the count of votes with the thousands marked.
func omdbVotes(votes string) int {
	count, err := strconv.Atoi(strings.ReplaceAll(omdbValue(votes), ",", ""))
	if err != nil || count <= 0 {
		return 0
	}
	return count
}

// The OMDb answerer: one account answers one fact of one title. Every call
// keys on the IMDb id, and a title with no IMDb id is no answer, because the
// identity fact fills that id first. The answer of each title is held for the
// life of the container, so the facts of one title cost one call.
type omdbAnswerer struct {
	client *omdbClient
	titles map[string]*omdbTitle
}

func newOMDbAnswerer(client *omdbClient) omdbAnswerer {
	return omdbAnswerer{client: client, titles: map[string]*omdbTitle{}}
}

func (a omdbAnswerer) providerBlock() string { return providerBlockOMDb }

func (a omdbAnswerer) serves(fact string) bool {
	return slices.Contains(providerFacts[providerBlockOMDb], fact)
}

// The ask. The day's limit is read before the call and after it, so a key
// with no calls left leaves the line instead of failing the title, and the
// remaining titles keep their gaps for the next run.
func (a omdbAnswerer) answer(ctx context.Context, fact string, title titleRef) (factAnswer, bool, error) {
	imdb := strings.TrimSpace(title.ids["imdb"])
	if !a.serves(fact) || imdb == "" {
		return factAnswer{}, false, nil
	}
	held, err := a.title(ctx, imdb)
	if err != nil || held == nil {
		return factAnswer{}, false, err
	}
	answer := omdbAnswerOf(fact, *held)
	return answer, answersFact(fact, answer), nil
}

// The one call a title costs. The answer the container holds is read again
// for every other fact of the same title. A title OMDb does not hold is held
// as no title at all, and a title already held spends no call, so the day's
// limit is read only where a call is made.
func (a omdbAnswerer) title(ctx context.Context, imdb string) (*omdbTitle, error) {
	if held, cached := a.titles[imdb]; cached {
		return held, nil
	}
	if a.client.dailyLimitReached() {
		return nil, errDailyLimit
	}
	held, err := a.client.title(ctx, imdb)
	if err != nil {
		if a.client.dailyLimitReached() {
			return nil, errDailyLimit
		}
		return nil, err
	}
	if !held.found() {
		held = nil
	}
	a.titles[imdb] = held
	return held, nil
}

// Which fields of the one answer each fact reads. OMDb names no studio and no
// tagline, so the overview it answers holds neither. The certification is the
// Rated value as it stands, which is what Jellyfin writes into the mpaa
// element, per its MediaBrowser.Providers/Plugins/Omdb/OmdbProvider.cs, read
// on 2026-09-03.
func omdbAnswerOf(fact string, title omdbTitle) factAnswer {
	switch fact {
	case factOverview:
		return factAnswer{
			Plot:           omdbValue(title.Plot),
			Genres:         omdbGenres(title.Genre),
			Premiered:      omdbPremiered(title.Released),
			RuntimeMinutes: omdbRuntimeMinutes(title.Runtime),
		}
	case factCertification:
		return factAnswer{Certification: omdbValue(title.Rated)}
	case factRatingIMDb:
		return factAnswer{Rating: omdbIMDbRating(title)}
	case factRatingRottenTomatoes:
		return factAnswer{Rating: omdbFirstScore(title.score(omdbSourceRottenTomatoes))}
	case factRatingMetacritic:
		return factAnswer{Rating: omdbFirstScore(title.Metascore, title.score(omdbSourceMetacritic))}
	}
	return factAnswer{}
}

// IMDb is the one site OMDb states a count of votes for.
func omdbIMDbRating(title omdbTitle) *titleRating {
	rating := omdbFirstScore(title.IMDbRating, title.score(omdbSourceIMDb))
	if rating == nil {
		return nil
	}
	rating.Votes = omdbVotes(title.IMDbVotes)
	return rating
}
