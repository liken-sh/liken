package main

// markfaces_test.go proves the third tier of the credits estimate: the
// faceless end of a film's detections record.

import "testing"

// The same film with the start of the final faceless run of its detections
// record.
func (f estimateFile) withFacelessTail(start int64) estimateFile {
	f.faceless = start
	return f
}

// The same title with Animation among its genres.
func (f estimateFile) animated() estimateFile {
	f.animation = true
	return f
}

// The credits estimate of the faces tier.
func facesCreditsAt(path string, start int64) markRow {
	return markRow{Path: path, Ordinal: creditsEstimateOrdinal, Kind: markKindCredits,
		Start: ms(start), Source: markSourceFaces}
}

func TestTheFacelessTailIsTheCreditsOnlyWhereNoOtherClueIs(t *testing.T) {
	runEstimateCases(t, []estimateCase{
		{
			// The estimate starts 90 seconds into the run, because a story
			// often ends on a faceless shot before its credits start.
			name:  "a faceless tail alone",
			files: []estimateFile{estimateFilm("a.mkv", 0).withFacelessTail(5600000)},
			want:  []markRow{facesCreditsAt("a.mkv", 5690000)},
		},
		{
			name:  "no faceless tail",
			files: []estimateFile{estimateFilm("a.mkv", 0)},
			want:  nil,
		},
		{
			// The film runs 6,000 seconds, so the last 15% starts at 5,100.
			name:  "a tail that starts before the last 15% of the film",
			files: []estimateFile{estimateFilm("a.mkv", 0).withFacelessTail(5099000)},
			want:  nil,
		},
		{
			name:  "a tail that starts at the edge of the last 15%",
			files: []estimateFile{estimateFilm("a.mkv", 0).withFacelessTail(5100000)},
			want:  []markRow{facesCreditsAt("a.mkv", 5190000)},
		},
		{
			name:  "a tail shorter than credits run",
			files: []estimateFile{estimateFilm("a.mkv", 0).withFacelessTail(6000000 - facelessCreditsShortest + 1)},
			want:  nil,
		},
		{
			name:  "a tail as long as the shortest credits",
			files: []estimateFile{estimateFilm("a.mkv", 0).withFacelessTail(6000000 - facelessCreditsShortest)},
			want:  []markRow{facesCreditsAt("a.mkv", 5970000)},
		},
		{
			// The detector finds few drawn faces, so a faceless run in an
			// animated film is no sign of the credits.
			name:  "an animated film",
			files: []estimateFile{estimateFilm("a.mkv", 0).withFacelessTail(5600000).animated()},
			want:  nil,
		},
		{
			name:  "an exact clue wins",
			files: []estimateFile{estimateFilm("a.mkv", 5700000).withFacelessTail(5600000)},
			want:  []markRow{creditsAt("a.mkv", 5700000, nil)},
		},
		{
			name:  "a provider's credits win and need no row",
			files: []estimateFile{estimateFilm("a.mkv", 0, providerCredits(ms(5700000), nil)).withFacelessTail(5600000)},
			want:  nil,
		},
		{
			name: "a subtitle file's quiet end wins",
			files: []estimateFile{estimateFilm("a.mkv", 0).withFacelessTail(5600000).
				withSubtitles(subtitleDialogue{First: 1000, Last: 5650000})},
			want: []markRow{creditsAt("a.mkv", 5650000, nil)},
		},
		{
			// The tier is measured on films. An episode's credits run
			// under a minute, and its siblings give the inferred tier.
			name:  "an episode",
			files: []estimateFile{episodeOf("show", "e.mkv", 3000000, 0).withFacelessTail(2800000)},
			want:  nil,
		},
	})
}
