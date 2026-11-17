package main

// markestimate.go derives the one answer the catalog holds for where each
// video's credits start and where its intro is, from every clue the catalog
// holds. The up-next card in media-operator's display rises at the credits,
// both watched rules count a play as finished there, and the display offers
// its skip over the intro. The answer is computed here, when the library
// changes, and every reader uses it as it is: a reader ranks nothing and
// merges nothing, so every screen gives the same answer, and a reader with
// no answer for a video falls back to a rule of time. The answers are rows
// of the marks table with the source `estimate` and ordinals below zero,
// beside the clues they come from.
//
// The clues of the credits come in four tiers, and a tier is used only where
// the tiers above it have nothing:
//
//  1. The placement of the titles model, where the model was sure of it. The
//     model reads the text on screen at the end of the video itself.
//  2. The exact clues, which name the span in the file itself or for the
//     work: the providers' marks, and a chapter whose title names the
//     credits. The start of a `preview` span is a limit and not a clue of
//     its own kind: a preview follows the credits, so the credits start no
//     later than it does.
//  3. The inferred clues: the length of the credits of the other episodes of
//     the series, and the quiet stretches in a subtitle file beside the
//     video.
//  4. For a film alone, the faceless end of the film's detections record
//     (markfaces.go).
//
// Where the clues of one tier differ, the latest one wins for the credits,
// because a card that rises a little late costs less than one that rises
// over the end of the story. The earliest one wins for the intro.

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
	// titlesPlacementOrdinal, in titlesfit.go, is -3.
	sceneEstimateOrdinal = -4
)

// The confidence from which the estimate takes a placement of the titles
// model over every other clue. The model's confidence is the share of its
// answer within 5 seconds of the start it gives. Of 120 videos whose credits
// a person checked, the model placed 82 with a confidence of 0.6 or more, and
// each of those was within 10 seconds of the real start, where the providers'
// marks of the same 120 videos were within 10 seconds for 92. A placement
// under the line is no clue at all, because the placements that were minutes
// late were among them, and the latest clue wins.
const titlesSureConfidence = 0.6

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
	// The start of the final run of the detections record with no face,
	// and 0 for none, and whether the title's genres include Animation.
	// markfaces.go reads both.
	faceless  int64
	animation bool
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
// something, in the order of their starts. An absent start is the start of
// the file, and an absent end is the end of the file.
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

// What the providers say of one kind, as one span for each thing they mark.
// A provider can answer several candidates for one span, from several
// submissions or release versions of the file. The candidates that overlap
// are one group, and the group becomes one span from the median start and
// the median end of its members, so one submission that is off moves the
// span less than a mean would. Groups that do not overlap stay apart,
// because a film can hold the main credits, then a scene, then more credits.
func (f estimateFile) provided(kind string) []timeSpan {
	var groups [][]timeSpan
	reach := int64(0)
	for at, span := range f.candidates(kind) {
		if at > 0 && span.Start < reach {
			groups[len(groups)-1] = append(groups[len(groups)-1], span)
		} else {
			groups = append(groups, []timeSpan{span})
		}
		reach = max(reach, span.End)
	}
	var spans []timeSpan
	for _, group := range groups {
		starts, ends := make([]int64, len(group)), make([]int64, len(group))
		for at, span := range group {
			starts[at], ends[at] = span.Start, span.End
		}
		if span := (timeSpan{Start: medianOf(starts), End: medianOf(ends)}); span.End > span.Start {
			spans = append(spans, span)
		}
	}
	return spans
}

// The providers' credits spans that start in the second half of the file. A
// credits span in the first half is an opening title sequence.
func (f estimateFile) providedCredits() []timeSpan {
	return slices.DeleteFunc(f.provided(markKindCredits), func(span timeSpan) bool { return !f.secondHalf(span.Start) })
}

// Where the titles model placed the end titles, when it was sure of the
// placement and the placement is in the second half of the file.
func (f estimateFile) surePlacement() (int64, bool) {
	for _, mark := range f.marks {
		if mark.Kind != markKindCreditsPlacement || mark.Start == nil || mark.Confidence == nil {
			continue
		}
		if *mark.Confidence >= titlesSureConfidence && f.secondHalf(*mark.Start) {
			return *mark.Start, true
		}
	}
	return 0, false
}

// The credits of one file from the first two tiers: where they start, where
// the providers end them, and the source the answer's row names.
type creditsClue struct {
	start  int64
	end    *int64
	source string
}

func (f estimateFile) exactCredits() (creditsClue, bool) {
	clue, held := creditsClue{source: markSourceEstimate}, false
	if start, sure := f.surePlacement(); sure {
		clue.start, clue.source, held = start, markSourceTitles, true
	} else {
		claim := func(start int64) {
			if f.secondHalf(start) && (!held || start > clue.start) {
				clue.start, held = start, true
			}
		}
		if provided := f.providedCredits(); len(provided) > 0 {
			claim(provided[0].Start)
		}
		if f.chapter > 0 {
			claim(f.chapter)
		}
		previews := slices.DeleteFunc(f.provided(markKindPreview), func(span timeSpan) bool { return !f.secondHalf(span.Start) })
		if len(previews) > 0 && (!held || previews[0].Start < clue.start) {
			clue.start, held = previews[0].Start, true
		}
	}
	// The answer ends where the providers end the first credits span, so a
	// screen knows that something follows the credits. An end that the
	// providers left open stays open.
	if provided := f.providedCredits(); held && len(provided) > 0 {
		if end := provided[0].End; end > clue.start && end < f.duration {
			clue.end = &end
		}
	}
	return clue, held
}

// The scene after the credits that the providers state as a gap: two
// credits spans in the second half, with the scene between the end of the
// first and the start of the last. One provider states a scene only this
// way. The estimate writes the gap as a `post-credits` span, the kind
// another provider states a scene with, because the answer for the credits
// is one span and the gap would be lost with the second one.
func (f estimateFile) sceneEstimate() (markRow, bool) {
	provided := f.providedCredits()
	if len(provided) < 2 {
		return markRow{}, false
	}
	start, end := provided[0].End, provided[len(provided)-1].Start
	if end <= start {
		return markRow{}, false
	}
	return markRow{Path: f.path, Ordinal: sceneEstimateOrdinal, Kind: markKindPostCredits,
		Start: &start, End: &end, Source: markSourceEstimate}, true
}

// The exact intro clue of one file, and whether the file has one. The
// providers' intro and a chapter that overlap mark one intro, and the
// providers' is the more exact. Where they do not overlap, the earlier one
// is the intro.
func (f estimateFile) exactIntro() (timeSpan, bool) {
	provided := slices.DeleteFunc(f.provided(markKindIntro), func(span timeSpan) bool { return !f.firstHalf(span.Start) })
	chapter := f.intro.End > f.intro.Start && f.firstHalf(f.intro.Start)
	switch {
	case !chapter && len(provided) == 0:
		return timeSpan{}, false
	case !chapter:
		return provided[0], true
	case len(provided) == 0:
		return f.intro, true
	}
	overlaps := slices.ContainsFunc(provided, func(span timeSpan) bool {
		return f.intro.Start < span.End && span.Start < f.intro.End
	})
	if overlaps || provided[0].Start <= f.intro.Start {
		return provided[0], true
	}
	return f.intro, true
}

// The estimate rows of every file, in path order with the credits before the
// intro and the scene after both.
func markEstimates(files []estimateFile) []markRow {
	lengths := map[string][]int64{}
	intros := map[string][]timeSpan{}
	for _, file := range files {
		if group := file.creditsGroup(); group != "" {
			if clue, held := file.exactCredits(); held {
				lengths[group] = append(lengths[group], file.duration-clue.start)
			}
		}
		if group := file.introGroup(); group != "" {
			if span, held := file.exactIntro(); held {
				intros[group] = append(intros[group], span)
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
		if row, held := file.sceneEstimate(); held {
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

// The credits estimate of one file. An answer from the inferred clues or
// from the faceless end runs to the end of the file.
//
// The siblings' clue takes the median length of the credits of the other
// episodes, from their own answers of the first two tiers, and one sibling
// is enough, because credits change little from episode to episode.
func (f estimateFile) creditsEstimate(siblings []int64) (markRow, bool) {
	row := markRow{Path: f.path, Ordinal: creditsEstimateOrdinal, Kind: markKindCredits, Source: markSourceEstimate}
	if clue, held := f.exactCredits(); held {
		row.Start, row.End, row.Source = &clue.start, clue.end, clue.source
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
		start, held := f.facelessCredits()
		if !held {
			return markRow{}, false
		}
		row.Start, row.Source = &start, markSourceFaces
		return row, true
	}
	start := slices.Max(inferred)
	row.Start = &start
	return row, true
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
	if span, held := f.exactIntro(); held {
		return f.spanRow(row, span), true
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

// The median of whole milliseconds: the middle value, or the mean of the two
// middle values when the count is even. The caller never passes an empty
// list.
func medianOf(values []int64) int64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return int64(math.Round(float64(sorted[middle-1]+sorted[middle]) / 2))
	}
	return sorted[middle]
}
