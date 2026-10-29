package main

// markestimate.go derives one estimate of where each video's credits start
// and one of where its intro is, from every clue the catalog holds. The
// up-next card in media-operator's display rises at the credits, both
// watched rules count a play as finished there, and the display offers its
// skip over the intro. Many files have no provider marks, and those readers
// then fall back to a rule of time. So the estimate is written as one more
// candidate in the marks table, with the source `estimate`, beside the
// provider marks, and every reader takes it the way it takes a provider's.
//
// The clues come in two tiers. The exact tier is what names the span in the
// file itself or for the work: the providers' marks, a chapter whose title
// names the span, and for the credits the start of a `preview` span. The
// inferred tier is used only where the exact tier has nothing: the span the
// other episodes of the series have, and the quiet stretches in a subtitle
// file beside the video. In each tier the earliest clue wins, because a card
// that rises a little early costs less than one that rises after the
// credits have run.

import (
	"math"
	"slices"
	"strconv"
	"strings"
)

// The source of every estimate row, in the place a provider block's name is.
const markSourceEstimate = "estimate"

// The ordinals of the two estimate rows of a file. The walk numbers the
// marks ledger's spans of a file from zero, and its write deletes the
// file's ordinals at and above that count, so a row below zero is outside
// every walk's write and stays until the estimate changes it.
const (
	creditsEstimateOrdinal = -1
	introEstimateOrdinal   = -2
)

// One span of a file in milliseconds from its start.
type timeSpan struct {
	Start int64 `yaml:"start" json:"start"`
	End   int64 `yaml:"end" json:"end"`
}

// The words a chapter title holds when the chapter is the credits, or the
// intro, compared without case. The two lists are the one place these words
// are kept.
var (
	creditsChapterWords = []string{"credit", "outro", "end title"}
	introChapterWords   = []string{"intro", "opening", "main title", "title sequence", "theme"}
)

func titleNames(title string, words []string) bool {
	title = strings.ToLower(title)
	return slices.ContainsFunc(words, func(word string) bool { return strings.Contains(title, word) })
}

// The start of the earliest chapter that names the credits and starts in the
// second half of the file, in milliseconds, and 0 for none. An opening title
// sequence named credits is in the first half.
func (p probedFile) creditsChapter() int64 {
	duration := probeMilliseconds(p.Duration)
	for _, chapter := range p.Chapters {
		start := probeMilliseconds(chapter.Start)
		if titleNames(chapter.Title, creditsChapterWords) && start > 0 && 2*start >= duration {
			return start
		}
	}
	return 0
}

// The earliest chapter that names the intro and starts in the first half,
// from its start to the start of the next chapter, or to its own end where it
// is the last. An empty span is none.
func (p probedFile) introChapter() timeSpan {
	duration := probeMilliseconds(p.Duration)
	for at, chapter := range p.Chapters {
		start := probeMilliseconds(chapter.Start)
		if !titleNames(chapter.Title, introChapterWords) || 2*start >= duration {
			continue
		}
		end := probeMilliseconds(chapter.End)
		if at+1 < len(p.Chapters) {
			end = probeMilliseconds(p.Chapters[at+1].Start)
		}
		if end > start {
			return timeSpan{Start: start, End: end}
		}
	}
	return timeSpan{}
}

// One video as the estimate reads it: its length, the series and season of
// an episode, the chapters that name the credits and the intro, its
// providers' marks, and the dialogue of the subtitle file chosen for it.
type estimateFile struct {
	path     string
	duration int64
	// The series of an episode, and empty for a film.
	series   string
	season   int
	chapter  int64
	intro    timeSpan
	marks    []markRow
	dialogue *subtitleDialogue
}

// The siblings a file is measured against for the credits: the episodes of
// one series. Credits run about the same length across a series.
func (f estimateFile) creditsGroup() string { return f.series }

// The siblings for the intro: the episodes of one season of one series. A
// title sequence can change between seasons.
func (f estimateFile) introGroup() string {
	if f.series == "" {
		return ""
	}
	return f.series + "\x1f" + strconv.Itoa(f.season)
}

func (f estimateFile) firstHalf(start int64) bool { return start >= 0 && 2*start < f.duration }
func (f estimateFile) secondHalf(start int64) bool {
	return 2*start >= f.duration && start < f.duration
}

// The provider candidates of one kind with a start and an end that mark
// something. An absent start is the start of the file, and an absent end is
// the end of the file, as the readers take them.
func (f estimateFile) candidates(kind string) []timeSpan {
	var spans []timeSpan
	for _, mark := range f.marks {
		if mark.Kind != kind {
			continue
		}
		span := timeSpan{Start: 0, End: f.duration}
		if mark.Start != nil {
			span.Start = *mark.Start
		}
		if mark.End != nil {
			span.End = *mark.End
		}
		if span.Start >= 0 && span.End > span.Start {
			spans = append(spans, span)
		}
	}
	slices.SortStableFunc(spans, func(a, b timeSpan) int { return int(a.Start - b.Start) })
	return spans
}

// The exact credits clues of one file: the earliest start, whether a
// provider's candidate starts there, and the provider's earliest candidate
// in the second half, which the estimate's end follows.
type creditsClues struct {
	start    int64
	held     bool
	provided bool
	first    *timeSpan
}

func (f estimateFile) exactCredits() creditsClues {
	clues := creditsClues{}
	take := func(start int64, provided bool) {
		if !f.secondHalf(start) {
			return
		}
		if !clues.held || start < clues.start {
			clues.start, clues.held, clues.provided = start, true, provided
		} else if start == clues.start && provided {
			clues.provided = true
		}
	}
	for _, span := range f.candidates(markKindCredits) {
		if f.secondHalf(span.Start) && clues.first == nil {
			clues.first = &span
		}
		take(span.Start, true)
	}
	if f.chapter > 0 {
		take(f.chapter, false)
	}
	for _, span := range f.candidates(markKindPreview) {
		take(span.Start, false)
	}
	return clues
}

// The exact intro clue of one file, and whether the file has one. A
// provider's candidate and a chapter that overlap mark one intro, and the
// provider's is the more exact. Where they do not overlap, the earlier one
// is the intro. Only a chapter is written as an estimate, because the
// provider's candidate is in the table already.
type introClue struct {
	span     timeSpan
	held     bool
	provided bool
}

func (f estimateFile) exactIntro() introClue {
	var provided []timeSpan
	for _, span := range f.candidates(markKindIntro) {
		if f.firstHalf(span.Start) {
			provided = append(provided, span)
		}
	}
	chapter := f.intro.End > f.intro.Start && f.firstHalf(f.intro.Start)
	switch {
	case !chapter && len(provided) == 0:
		return introClue{}
	case !chapter:
		return introClue{span: provided[0], held: true, provided: true}
	case len(provided) == 0:
		return introClue{span: f.intro, held: true}
	}
	overlaps := slices.ContainsFunc(provided, func(span timeSpan) bool {
		return f.intro.Start < span.End && span.Start < f.intro.End
	})
	if overlaps || provided[0].Start <= f.intro.Start {
		return introClue{span: provided[0], held: true, provided: true}
	}
	return introClue{span: f.intro, held: true}
}

// The estimate rows of every file, in path order with the credits before the
// intro. A file whose earliest clue is a provider's own candidate gets no
// row of that kind, because a copy would count that submission twice in the
// readers' median.
func markEstimates(files []estimateFile) []markRow {
	lengths := map[string][]int64{}
	intros := map[string][]timeSpan{}
	for _, file := range files {
		if group := file.creditsGroup(); group != "" {
			if clues := file.exactCredits(); clues.held {
				lengths[group] = append(lengths[group], file.duration-clues.start)
			}
		}
		if group := file.introGroup(); group != "" {
			if clue := file.exactIntro(); clue.held {
				intros[group] = append(intros[group], clue.span)
			}
		}
	}
	var rows []markRow
	for _, file := range files {
		if row, held := file.creditsEstimate(lengths[file.creditsGroup()]); held {
			rows = append(rows, row)
		}
		if row, held := file.introEstimate(intros[file.introGroup()]); held {
			rows = append(rows, row)
		}
	}
	slices.SortStableFunc(rows, func(a, b markRow) int {
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		return b.Ordinal - a.Ordinal
	})
	return rows
}

// The credits estimate of one file. The exact estimate ends where the
// provider's earliest candidate ends, so the readers merge it into that
// candidate's group and keep a later credits span, the second run of credits
// after a scene, apart. An inferred estimate runs to the end of the file.
//
// The siblings' estimate takes the median length of the credits of the other
// episodes, from their exact clues, and one sibling is enough, because
// credits change little from episode to episode.
func (f estimateFile) creditsEstimate(siblings []int64) (markRow, bool) {
	row := markRow{Path: f.path, Ordinal: creditsEstimateOrdinal, Kind: markKindCredits, Source: markSourceEstimate}
	if clues := f.exactCredits(); clues.held {
		if clues.provided {
			return markRow{}, false
		}
		start := clues.start
		row.Start = &start
		if clues.first != nil && f.hasEnd(markKindCredits, clues.first.Start) {
			end := clues.first.End
			row.End = &end
		}
		return row, true
	}
	var inferred []int64
	if f.series != "" && len(siblings) > 0 {
		inferred = append(inferred, f.duration-medianOf(siblings))
	}
	if f.dialogue != nil {
		if start, held := f.dialogue.creditsStart(f.duration); held {
			inferred = append(inferred, start)
		}
	}
	inferred = slices.DeleteFunc(inferred, func(start int64) bool { return !f.secondHalf(start) })
	if len(inferred) == 0 {
		return markRow{}, false
	}
	start := slices.Min(inferred)
	row.Start = &start
	return row, true
}

// Whether a provider's candidate of the kind that starts at start states its
// end, so the estimate copies an end the provider gave and leaves an open
// end open.
func (f estimateFile) hasEnd(kind string, start int64) bool {
	for _, mark := range f.marks {
		if mark.Kind == kind && mark.Start != nil && *mark.Start == start && mark.End != nil {
			return true
		}
	}
	return false
}

// The longest spread of the siblings' intro starts that still counts as
// agreement, in milliseconds. A cold open of a different length moves the
// intro, and a spread wider than half a minute says the episodes open in
// different ways, so their median says nothing about this episode.
const introSpread = 30000

// The intro estimate of one file. The siblings' estimate needs two episodes
// that agree, because one episode alone shows no agreement. It runs from the
// median start for the median length.
func (f estimateFile) introEstimate(siblings []timeSpan) (markRow, bool) {
	row := markRow{Path: f.path, Ordinal: introEstimateOrdinal, Kind: markKindIntro, Source: markSourceEstimate}
	if clue := f.exactIntro(); clue.held {
		if clue.provided {
			return markRow{}, false
		}
		return f.spanRow(row, clue.span), true
	}
	var inferred []timeSpan
	if span, held := siblingIntro(siblings); held && f.series != "" {
		inferred = append(inferred, span)
	}
	if f.dialogue != nil {
		if span, held := f.dialogue.introSpan(f.duration); held {
			inferred = append(inferred, span)
		}
	}
	inferred = slices.DeleteFunc(inferred, func(span timeSpan) bool {
		return !f.firstHalf(span.Start) || span.End > f.duration
	})
	if len(inferred) == 0 {
		return markRow{}, false
	}
	earliest := slices.MinFunc(inferred, func(a, b timeSpan) int { return int(a.Start - b.Start) })
	return f.spanRow(row, earliest), true
}

func (f estimateFile) spanRow(row markRow, span timeSpan) markRow {
	start, end := span.Start, span.End
	row.Start, row.End = &start, &end
	return row
}

func siblingIntro(spans []timeSpan) (timeSpan, bool) {
	if len(spans) < 2 {
		return timeSpan{}, false
	}
	starts := make([]int64, len(spans))
	lengths := make([]int64, len(spans))
	for at, span := range spans {
		starts[at], lengths[at] = span.Start, span.End-span.Start
	}
	if slices.Max(starts)-slices.Min(starts) > introSpread {
		return timeSpan{}, false
	}
	start := medianOf(starts)
	return timeSpan{Start: start, End: start + medianOf(lengths)}, true
}

// The median of whole milliseconds, through the readers' own median.
func medianOf(values []int64) int64 {
	floats := make([]float64, len(values))
	for at, value := range values {
		floats[at] = float64(value)
	}
	return int64(math.Round(median(floats)))
}
