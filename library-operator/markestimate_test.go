package main

// markestimate_test.go proves the rule that derives one credits start and
// one intro span for each video from every clue the catalog holds.

import (
	"reflect"
	"testing"
)

// Milliseconds as a mark's edge.
func ms(value int64) *int64 { return &value }

// A provider's candidate of one kind for one file.
func provider(kind string, start, end *int64) markRow {
	return markRow{Kind: kind, Start: start, End: end, Source: "theintrodb"}
}

func providerCredits(start, end *int64) markRow { return provider(markKindCredits, start, end) }
func providerPreview(start, end *int64) markRow { return provider(markKindPreview, start, end) }
func providerIntro(start, end *int64) markRow   { return provider(markKindIntro, start, end) }

// A film of 6,000 seconds with the credits chapter and the provider marks
// given.
func estimateFilm(path string, chapter int64, marks ...markRow) estimateFile {
	return estimateFile{path: path, duration: 6000000, chapter: chapter, marks: marks}
}

// An episode in season 1 of one series with the length, the credits
// chapter, and the provider marks given.
func episodeOf(series, path string, duration, chapter int64, marks ...markRow) estimateFile {
	return estimateFile{path: path, duration: duration, series: series, season: 1, chapter: chapter, marks: marks}
}

// The same file with an intro chapter.
func (f estimateFile) withIntro(start, end int64) estimateFile {
	f.intro = timeSpan{Start: start, End: end}
	return f
}

// The same file in another season.
func (f estimateFile) inSeason(season int) estimateFile {
	f.season = season
	return f
}

// The same file with the dialogue of a subtitle file beside it.
func (f estimateFile) withSubtitles(dialogue subtitleDialogue) estimateFile {
	f.dialogue = &dialogue
	return f
}

// The titles model's placement of one file's end titles, with how sure the
// model was of it.
func placement(start int64, confidence float64) markRow {
	return markRow{Kind: markKindCreditsPlacement, Start: ms(start), Source: markSourceTitles, Confidence: &confidence}
}

// The credits estimate of one file where the titles model's placement wins.
func placedCreditsAt(path string, start int64, end *int64) markRow {
	row := creditsAt(path, start, end)
	row.Source = markSourceTitles
	return row
}

// The scene after the credits of one file, as the rule writes it.
func sceneAt(path string, start, end int64) markRow {
	return markRow{Path: path, Ordinal: sceneEstimateOrdinal, Kind: markKindPostCredits,
		Start: ms(start), End: ms(end), Source: markSourceEstimate}
}

// The credits estimate of one file, as the rule writes it.
func creditsAt(path string, start int64, end *int64) markRow {
	return markRow{Path: path, Ordinal: creditsEstimateOrdinal, Kind: markKindCredits,
		Start: ms(start), End: end, Source: markSourceEstimate}
}

// The intro estimate of one file, as the rule writes it.
func introAt(path string, start, end int64) markRow {
	return markRow{Path: path, Ordinal: introEstimateOrdinal, Kind: markKindIntro,
		Start: ms(start), End: ms(end), Source: markSourceEstimate}
}

type estimateCase struct {
	name  string
	files []estimateFile
	want  []markRow
}

func runEstimateCases(t *testing.T, cases []estimateCase) {
	t.Helper()
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := markEstimates(test.files); !reflect.DeepEqual(got, test.want) {
				t.Errorf("estimates = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestTheCreditsEstimateTakesTheLatestExactClue(t *testing.T) {
	runEstimateCases(t, []estimateCase{
		{
			name:  "a credits chapter alone",
			files: []estimateFile{estimateFilm("a.mkv", 5600000)},
			want:  []markRow{creditsAt("a.mkv", 5600000, nil)},
		},
		{
			name:  "a preview alone",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerPreview(ms(5800000), ms(5900000)))},
			want:  []markRow{creditsAt("a.mkv", 5800000, nil)},
		},
		{
			// The readers use the estimate alone, so the providers' answer
			// is an estimate too, with the end the providers gave.
			name:  "a provider's credits alone",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerCredits(ms(5700000), ms(5950000)))},
			want:  []markRow{creditsAt("a.mkv", 5700000, ms(5950000))},
		},
		{
			// Three submissions for one span are one clue at their median,
			// so the one that is a minute off moves nothing.
			name: "several submissions for one credits span",
			files: []estimateFile{estimateFilm("a.mkv", 0,
				providerCredits(ms(5700000), ms(5950000)),
				providerCredits(ms(5704000), ms(5952000)),
				providerCredits(ms(5760000), ms(5950000)))},
			want: []markRow{creditsAt("a.mkv", 5704000, ms(5950000))},
		},
		{
			name:  "a chapter before the provider's credits",
			files: []estimateFile{estimateFilm("a.mkv", 5500000, providerCredits(ms(5700000), ms(5950000)))},
			want:  []markRow{creditsAt("a.mkv", 5700000, ms(5950000))},
		},
		{
			// The provider ends the credits before the chapter starts, so
			// the answer has no end of the provider's.
			name:  "a chapter after the provider's credits",
			files: []estimateFile{estimateFilm("a.mkv", 5800000, providerCredits(ms(5300000), ms(5700000)))},
			want:  []markRow{creditsAt("a.mkv", 5800000, nil)},
		},
		{
			// A preview follows the credits, so the credits start no later
			// than the preview does.
			name:  "a preview before the chapter",
			files: []estimateFile{estimateFilm("a.mkv", 5600000, providerPreview(ms(5400000), ms(5500000)))},
			want:  []markRow{creditsAt("a.mkv", 5400000, nil)},
		},
		{
			name: "a preview after the latest clue",
			files: []estimateFile{estimateFilm("a.mkv", 5600000,
				providerCredits(ms(5300000), ms(5900000)), providerPreview(ms(5800000), nil))},
			want: []markRow{creditsAt("a.mkv", 5600000, ms(5900000))},
		},
		{
			// The provider left the end open, so the estimate leaves it open.
			name:  "a provider's credits with no end",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerCredits(ms(5700000), nil))},
			want:  []markRow{creditsAt("a.mkv", 5700000, nil)},
		},
		{
			// Two credits spans are the main credits, a scene, and more
			// credits. The answer is the first span, and the gap is the
			// scene.
			name: "two credits spans with a scene between them",
			files: []estimateFile{estimateFilm("a.mkv", 0,
				providerCredits(ms(5700000), ms(5800000)),
				providerCredits(ms(5900000), ms(5990000)))},
			want: []markRow{creditsAt("a.mkv", 5700000, ms(5800000)), sceneAt("a.mkv", 5800000, 5900000)},
		},
		{
			name:  "a credits chapter and an intro chapter",
			files: []estimateFile{estimateFilm("a.mkv", 5600000).withIntro(0, 90000)},
			want:  []markRow{creditsAt("a.mkv", 5600000, nil), introAt("a.mkv", 0, 90000)},
		},
		{
			name:  "a chapter in the first half",
			files: []estimateFile{estimateFilm("a.mkv", 2900000)},
			want:  nil,
		},
		{
			// An opening title sequence marked as credits moves nothing, and
			// a later chapter still counts.
			name: "provider credits in the first half",
			files: []estimateFile{estimateFilm("a.mkv", 5600000,
				providerCredits(ms(60000), ms(120000)), providerCredits(nil, ms(30000)))},
			want: []markRow{creditsAt("a.mkv", 5600000, nil)},
		},
		{
			name:  "no clue",
			files: []estimateFile{estimateFilm("a.mkv", 0)},
			want:  nil,
		},
	})
}

func TestTheCreditsEstimateTakesAPlacementTheModelIsSureOf(t *testing.T) {
	runEstimateCases(t, []estimateCase{
		{
			name:  "a sure placement alone",
			files: []estimateFile{estimateFilm("a.mkv", 0, placement(5650000, 0.6))},
			want:  []markRow{placedCreditsAt("a.mkv", 5650000, nil)},
		},
		{
			// The placement wins over a later chapter and the provider's
			// start, and keeps the end the provider gave.
			name: "a sure placement and every other exact clue",
			files: []estimateFile{estimateFilm("a.mkv", 5800000,
				placement(5650000, 0.9), providerCredits(ms(5700000), ms(5950000)))},
			want: []markRow{placedCreditsAt("a.mkv", 5650000, ms(5950000))},
		},
		{
			// A placement the model was not sure of is no clue, so a late
			// one cannot win as the latest clue.
			name:  "an unsure placement after a chapter",
			files: []estimateFile{estimateFilm("a.mkv", 5600000, placement(5990000, 0.59))},
			want:  []markRow{creditsAt("a.mkv", 5600000, nil)},
		},
		{
			name:  "an unsure placement alone",
			files: []estimateFile{estimateFilm("a.mkv", 0, placement(5650000, 0.3))},
			want:  nil,
		},
		{
			name:  "a sure placement in the first half",
			files: []estimateFile{estimateFilm("a.mkv", 0, placement(2000000, 0.9))},
			want:  nil,
		},
		{
			// The sibling's placement puts its credits 150 seconds before
			// the end, and the episode with no clue takes that length.
			name: "a sibling with a sure placement",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 0, placement(2850000, 0.8)),
				episodeOf("series:a", "e2.mkv", 2000000, 0),
			},
			want: []markRow{placedCreditsAt("e1.mkv", 2850000, nil), creditsAt("e2.mkv", 1850000, nil)},
		},
	})
}

func TestTheCreditsEstimateFallsBackToTheInferredClues(t *testing.T) {
	// Dialogue that ends 200 seconds before the end of a 3,000-second
	// episode.
	quietLast200 := subtitleDialogue{First: 5000, Last: 2800000}
	runEstimateCases(t, []estimateCase{
		{
			// The siblings' credits run 100, 120, and 90 seconds, so the
			// median is 100 seconds before the end.
			name: "three siblings with clues",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 2900000),
				episodeOf("series:a", "e2.mkv", 3000000, 0, providerCredits(ms(2880000), ms(2990000))),
				episodeOf("series:a", "e3.mkv", 3000000, 0, providerPreview(ms(2910000), nil)),
				episodeOf("series:a", "e4.mkv", 2500000, 0),
			},
			want: []markRow{
				creditsAt("e1.mkv", 2900000, nil),
				creditsAt("e2.mkv", 2880000, ms(2990000)),
				creditsAt("e3.mkv", 2910000, nil),
				creditsAt("e4.mkv", 2400000, nil),
			},
		},
		{
			name: "two siblings take the mean of the middle two",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 2900000),
				episodeOf("series:a", "e2.mkv", 3000000, 2880000),
				episodeOf("series:a", "e3.mkv", 2000000, 0),
			},
			want: []markRow{
				creditsAt("e1.mkv", 2900000, nil),
				creditsAt("e2.mkv", 2880000, nil),
				creditsAt("e3.mkv", 1890000, nil),
			},
		},
		{
			name: "no sibling has a clue",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 0),
				episodeOf("series:a", "e2.mkv", 3000000, 0),
			},
			want: nil,
		},
		{
			name: "the clues belong to another series",
			files: []estimateFile{
				episodeOf("series:b", "b1.mkv", 3000000, 2900000),
				episodeOf("series:a", "a1.mkv", 3000000, 0),
			},
			want: []markRow{creditsAt("b1.mkv", 2900000, nil)},
		},
		{
			name:  "a film never takes the series' median",
			files: []estimateFile{estimateFilm("f1.mkv", 5600000), estimateFilm("f2.mkv", 0)},
			want:  []markRow{creditsAt("f1.mkv", 5600000, nil)},
		},
		{
			// A short special with the series' 100 seconds of credits would
			// start them at 50 of its 150 seconds, in the first half.
			name: "a median that lands in the first half",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 2900000),
				episodeOf("series:a", "special.mkv", 150000, 0),
			},
			want: []markRow{creditsAt("e1.mkv", 2900000, nil)},
		},
		{
			name:  "a subtitle gap alone",
			files: []estimateFile{episodeOf("series:a", "e1.mkv", 3000000, 0).withSubtitles(quietLast200)},
			want:  []markRow{creditsAt("e1.mkv", 2800000, nil)},
		},
		{
			name: "an exact clue leaves the subtitle gap unused",
			files: []estimateFile{episodeOf("series:a", "e1.mkv", 3000000, 2950000).
				withSubtitles(quietLast200)},
			want: []markRow{creditsAt("e1.mkv", 2950000, nil)},
		},
		{
			// The sibling's credits run 100 seconds, so the median puts this
			// episode's at 2,900 seconds, after the subtitle gap, and the
			// later of the two wins.
			name: "the subtitle gap before the sibling median",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 2900000),
				episodeOf("series:a", "e2.mkv", 3000000, 0).withSubtitles(quietLast200),
			},
			want: []markRow{creditsAt("e1.mkv", 2900000, nil), creditsAt("e2.mkv", 2900000, nil)},
		},
		{
			name: "the sibling median before the subtitle gap",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 2700000),
				episodeOf("series:a", "e2.mkv", 3000000, 0).withSubtitles(quietLast200),
			},
			want: []markRow{creditsAt("e1.mkv", 2700000, nil), creditsAt("e2.mkv", 2800000, nil)},
		},
	})
}

func TestTheIntroEstimateTakesTheExactClues(t *testing.T) {
	runEstimateCases(t, []estimateCase{
		{
			name:  "an intro chapter alone",
			files: []estimateFile{estimateFilm("a.mkv", 0).withIntro(60000, 105000)},
			want:  []markRow{introAt("a.mkv", 60000, 105000)},
		},
		{
			// The readers use the estimate alone, so the provider's answer
			// is an estimate too.
			name:  "a provider's intro alone",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerIntro(ms(61000), ms(104000)))},
			want:  []markRow{introAt("a.mkv", 61000, 104000)},
		},
		{
			// Three submissions for one intro are one clue at their medians.
			name: "several submissions for one intro",
			files: []estimateFile{estimateFilm("a.mkv", 0,
				providerIntro(nil, ms(107000)), providerIntro(ms(7000), ms(106000)), providerIntro(ms(8000), ms(109000)))},
			want: []markRow{introAt("a.mkv", 7000, 107000)},
		},
		{
			// The two overlap, so they mark one intro, and the provider's
			// candidate is the more exact of the two.
			name:  "a chapter that overlaps the provider's intro",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerIntro(ms(61000), ms(104000))).withIntro(60000, 105000)},
			want:  []markRow{introAt("a.mkv", 61000, 104000)},
		},
		{
			name:  "a chapter before the provider's intro",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerIntro(ms(300000), ms(340000))).withIntro(0, 90000)},
			want:  []markRow{introAt("a.mkv", 0, 90000)},
		},
		{
			name:  "a chapter after the provider's intro",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerIntro(nil, ms(40000))).withIntro(300000, 340000)},
			want:  []markRow{introAt("a.mkv", 0, 40000)},
		},
		{
			name:  "an intro chapter in the second half",
			files: []estimateFile{estimateFilm("a.mkv", 0).withIntro(3100000, 3150000)},
			want:  nil,
		},
	})
}

func TestTheIntroEstimateFallsBackToTheInferredClues(t *testing.T) {
	// Dialogue with one quiet stretch from 70 to 130 seconds.
	quietIntro := subtitleDialogue{First: 2000, Last: 2990000, Gaps: []timeSpan{{Start: 70000, End: 130000}}}
	runEstimateCases(t, []estimateCase{
		{
			// The siblings' intros start at 60, 65, and 70 seconds and run
			// 60, 60, and 48 seconds.
			name: "siblings that agree",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 0).withIntro(60000, 120000),
				episodeOf("series:a", "e2.mkv", 3000000, 0, providerIntro(ms(65000), ms(125000))),
				episodeOf("series:a", "e3.mkv", 3000000, 0, providerIntro(ms(70000), ms(118000))),
				episodeOf("series:a", "e4.mkv", 3000000, 0),
			},
			want: []markRow{
				introAt("e1.mkv", 60000, 120000),
				introAt("e2.mkv", 65000, 125000),
				introAt("e3.mkv", 70000, 118000),
				introAt("e4.mkv", 65000, 125000),
			},
		},
		{
			// A cold open of a different length moves the intro, so the
			// siblings say nothing about this episode.
			name: "siblings that scatter",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 0).withIntro(60000, 120000),
				episodeOf("series:a", "e2.mkv", 3000000, 0).withIntro(300000, 360000),
				episodeOf("series:a", "e3.mkv", 3000000, 0),
			},
			want: []markRow{introAt("e1.mkv", 60000, 120000), introAt("e2.mkv", 300000, 360000)},
		},
		{
			name: "one sibling cannot show agreement",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 0).withIntro(60000, 120000),
				episodeOf("series:a", "e2.mkv", 3000000, 0),
			},
			want: []markRow{introAt("e1.mkv", 60000, 120000)},
		},
		{
			name: "siblings of another season",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 0).withIntro(60000, 120000),
				episodeOf("series:a", "e2.mkv", 3000000, 0).withIntro(62000, 121000),
				episodeOf("series:a", "s2e1.mkv", 3000000, 0).inSeason(2),
			},
			want: []markRow{introAt("e1.mkv", 60000, 120000), introAt("e2.mkv", 62000, 121000)},
		},
		{
			name: "a film never takes the siblings' intro",
			files: []estimateFile{
				estimateFilm("f1.mkv", 0).withIntro(60000, 120000),
				estimateFilm("f2.mkv", 0).withIntro(61000, 121000),
				estimateFilm("f3.mkv", 0),
			},
			want: []markRow{introAt("f1.mkv", 60000, 120000), introAt("f2.mkv", 61000, 121000)},
		},
		{
			name:  "a subtitle gap alone",
			files: []estimateFile{estimateFilm("f1.mkv", 0).withSubtitles(quietIntro)},
			want:  []markRow{introAt("f1.mkv", 70000, 130000)},
		},
		{
			name:  "an exact clue leaves the subtitle gap unused",
			files: []estimateFile{estimateFilm("f1.mkv", 0, providerIntro(ms(10000), ms(50000))).withSubtitles(quietIntro)},
			want:  []markRow{introAt("f1.mkv", 10000, 50000)},
		},
		{
			name: "the earlier of the siblings and the subtitle gap",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 0).withIntro(20000, 60000),
				episodeOf("series:a", "e2.mkv", 3000000, 0).withIntro(22000, 62000),
				episodeOf("series:a", "e3.mkv", 3000000, 0).withSubtitles(quietIntro),
			},
			want: []markRow{
				introAt("e1.mkv", 20000, 60000),
				introAt("e2.mkv", 22000, 62000),
				introAt("e3.mkv", 21000, 61000),
			},
		},
	})
}
