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

func TestTheCreditsEstimateTakesTheEarliestExactClue(t *testing.T) {
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
			// The provider's candidate is in the table already, and a copy
			// would count that submission twice in the readers' median.
			name:  "a provider's credits alone",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerCredits(ms(5700000), ms(5950000)))},
			want:  nil,
		},
		{
			// The estimate ends where the provider's earliest candidate ends,
			// so the readers merge it into that candidate's group and keep a
			// later credits span apart.
			name: "a chapter before the provider's credits",
			files: []estimateFile{estimateFilm("a.mkv", 5500000,
				providerCredits(ms(5700000), ms(5800000)),
				providerCredits(ms(5900000), ms(5990000)),
				providerPreview(ms(5850000), nil))},
			want: []markRow{creditsAt("a.mkv", 5500000, ms(5800000))},
		},
		{
			name:  "a preview before the chapter",
			files: []estimateFile{estimateFilm("a.mkv", 5600000, providerPreview(ms(5400000), ms(5500000)))},
			want:  []markRow{creditsAt("a.mkv", 5400000, nil)},
		},
		{
			name: "the provider's credits before every other clue",
			files: []estimateFile{estimateFilm("a.mkv", 5600000,
				providerCredits(ms(5300000), ms(5900000)), providerPreview(ms(5800000), nil))},
			want: nil,
		},
		{
			name:  "a chapter at the provider's own start",
			files: []estimateFile{estimateFilm("a.mkv", 5700000, providerCredits(ms(5700000), ms(5950000)))},
			want:  nil,
		},
		{
			// The provider left the end open, so the estimate leaves it open.
			name:  "a chapter before a provider's credits with no end",
			files: []estimateFile{estimateFilm("a.mkv", 5500000, providerCredits(ms(5700000), nil))},
			want:  []markRow{creditsAt("a.mkv", 5500000, nil)},
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
			// episode's at 2,900 seconds, after the subtitle gap.
			name: "the subtitle gap before the sibling median",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 2900000),
				episodeOf("series:a", "e2.mkv", 3000000, 0).withSubtitles(quietLast200),
			},
			want: []markRow{creditsAt("e1.mkv", 2900000, nil), creditsAt("e2.mkv", 2800000, nil)},
		},
		{
			name: "the sibling median before the subtitle gap",
			files: []estimateFile{
				episodeOf("series:a", "e1.mkv", 3000000, 2700000),
				episodeOf("series:a", "e2.mkv", 3000000, 0).withSubtitles(quietLast200),
			},
			want: []markRow{creditsAt("e1.mkv", 2700000, nil), creditsAt("e2.mkv", 2700000, nil)},
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
			name:  "a provider's intro alone",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerIntro(ms(61000), ms(104000)))},
			want:  nil,
		},
		{
			// The two overlap, so they mark one intro, and the provider's
			// candidate is the more exact of the two.
			name:  "a chapter that overlaps the provider's intro",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerIntro(ms(61000), ms(104000))).withIntro(60000, 105000)},
			want:  nil,
		},
		{
			name:  "a chapter before the provider's intro",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerIntro(ms(300000), ms(340000))).withIntro(0, 90000)},
			want:  []markRow{introAt("a.mkv", 0, 90000)},
		},
		{
			name:  "a chapter after the provider's intro",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerIntro(nil, ms(40000))).withIntro(300000, 340000)},
			want:  nil,
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
			want:  nil,
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
