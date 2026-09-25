package main

// imdbanswer.go is the answerer of the imdb block. It asks no server per
// title. It answers from the rows the container's dataset reads kept, so it
// waits for those reads before its first answer.

import (
	"context"
	"errors"
	"maps"
	"os"
	"strings"
	"time"
)

// A dataset read that failed ends the imdb block's work for the run, as a
// spent day ends OMDb's, and the titles keep their gaps for the next run.
// The reads log their own error once.
var errDatasetsUnread = errors.New("the IMDb datasets could not be read in this run")

// The answerer reads the container's reads through a function, because the
// line is built before the reads start.
type imdbAnswerer struct {
	reads func() *datasetReads
}

func (a imdbAnswerer) providerBlock() string { return providerBlockIMDb }

func (a imdbAnswerer) serves(fact string) bool { return blockServes(providerBlockIMDb, fact) }

// A title with no IMDb id, and a title title.ratings does not hold, is no
// answer, which the fact records as a miss with a date.
func (a imdbAnswerer) answer(ctx context.Context, fact string, title titleRef) (factAnswer, bool, error) {
	reads := a.reads()
	if !a.serves(fact) || reads == nil {
		return factAnswer{}, false, nil
	}
	if err := reads.wait(ctx); err != nil {
		return factAnswer{}, false, errDatasetsUnread
	}
	rating, held := reads.ratings[strings.TrimSpace(title.ids[providerBlockIMDb])]
	if !held {
		return factAnswer{}, false, nil
	}
	return factAnswer{Rating: &rating}, true, nil
}

// The nfo container's line: the table every block builds from, with the imdb
// block bound to this container's reads.
func (e *enricher) nfoAnswerLine() *answerLine {
	table := maps.Clone(nfoAnswerers)
	table[providerBlockIMDb] = func(string, string, *tallies) answerer {
		return imdbAnswerer{reads: func() *datasetReads { return e.datasets }}
	}
	return &answerLine{
		answerers: recordingAnswerers(commaNames(os.Getenv(librarySourcesVariable)), os.Getenv, e.tallies, table),
		spent:     map[string]bool{},
	}
}

// The time an attempt of the fact records: the Last-Modified time of the
// title.ratings copy the container read, where the imdb block answered or
// where the reads ended and no block answered. An attempt another block
// answered records none.
func (e *enricher) datasetTimeOf(fact string, names providerNames) time.Time {
	if fact != factRatingIMDb || e.datasets == nil || (len(names) > 0 && !names.is(providerBlockIMDb)) {
		return time.Time{}
	}
	select {
	case <-e.datasets.done:
		return e.datasets.modified
	default:
		return time.Time{}
	}
}
