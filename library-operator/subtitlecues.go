package main

// subtitlecues.go reads a text subtitle file beside a video into the
// stretches where nobody speaks, and reads two marks out of those stretches.
// Credits and a title sequence play over music with no dialogue, so a long
// quiet stretch at the end of a file is a hint of the credits, and a shorter
// one near the start is a hint of the intro. The probe reads the file once
// when it appears or changes, records the stretches in the probe ledger, and
// the marks estimate reads them from the catalog. The probe never extracts a
// subtitle stream from inside a video, because that reads the whole video
// over the network.

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The subtitle formats whose cue times the probe reads. Each is text, with a
// start and an end on every cue. A picture format, such as .sup or .idx,
// carries no text to tell a song from a line.
var textSubtitleExtensions = map[string]bool{".srt": true, ".vtt": true, ".ass": true, ".ssa": true}

// Whether a file is a text subtitle file the probe reads.
func isTextSubtitle(name string) bool {
	return textSubtitleExtensions[strings.ToLower(filepath.Ext(name))]
}

// The condition that holds a text subtitle file in a query of the files
// table, by the extension of its path. The extensions are this file's
// constants and never input.
func textSubtitleClause() string {
	var terms []string
	for _, extension := range slices.Sorted(maps.Keys(textSubtitleExtensions)) {
		terms = append(terms, `lower(path) LIKE '%`+extension+`'`)
	}
	return `(` + strings.Join(terms, ` OR `) + `)`
}

// The subtitle file's record in the probe ledger: the file's own facts and
// the dialogue the probe read from it. A file with no dialogue holds no
// stretches, and its record still closes the probe gap.
func readSubtitleRecord(absolute string) (probedFile, error) {
	file, err := os.Open(absolute)
	if err != nil {
		return probedFile{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, subtitleReadLimit))
	if err != nil {
		return probedFile{}, fmt.Errorf("reading the subtitle file %s: %w", absolute, err)
	}
	record := probedFile{}
	if dialogue, held := readSubtitleDialogue(absolute, data); held {
		record.Dialogue = &dialogue
	}
	return record, nil
}

// The most of one subtitle file the probe reads. A feature's subtitles take
// well under a megabyte, so a larger file is not a subtitle file to trust.
const subtitleReadLimit = 8 << 20

// The lengths of quiet that the rules read, in milliseconds.
const (
	// The shortest quiet stretch that counts as credits. A pause inside a
	// scene rarely runs this long, and end credits run longer.
	subtitleCreditsGap = int64(45 * time.Second / time.Millisecond)
	// The longest run of dialogue that counts as a scene after the credits.
	// Such a scene is a short sting, and a longer run is the story going on.
	subtitleSceneLength = int64(3 * time.Minute / time.Millisecond)
	// The shortest and the longest quiet stretch that counts as an intro.
	// A title sequence runs from about half a minute to two minutes, and a
	// shorter pause is a beat in a scene.
	subtitleIntroShortest = int64(30 * time.Second / time.Millisecond)
	subtitleIntroLongest  = int64(2 * time.Minute / time.Millisecond)
	// How far into a file an intro starts and ends. An intro follows a
	// cold open of a few minutes, and a quiet stretch later than this is a
	// scene with no words.
	subtitleIntroWindow = int64(10 * time.Minute / time.Millisecond)
)

// The stretches with no dialogue in one subtitle file, in milliseconds: the
// start of the first line, the end of the last, and every quiet stretch
// between two lines that is long enough for either rule to read. A song cue
// and an empty cue are not dialogue.
type subtitleDialogue struct {
	First int64      `yaml:"first" json:"first"`
	Last  int64      `yaml:"last" json:"last"`
	Gaps  []timeSpan `yaml:"gaps,omitempty" json:"gaps,omitempty"`
}

// The dialogue a file holds, and false for a file with no line of dialogue.
// The extension chooses the format.
func readSubtitleDialogue(name string, data []byte) (subtitleDialogue, bool) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	var cues []timeSpan
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ass", ".ssa":
		cues = substationCues(text)
	default:
		cues = subripCues(text)
	}
	return dialogueOf(cues)
}

// The dialogue cues of a SubRip or a WebVTT file. Both put the two times of a
// cue on one line around an arrow, and its text on the lines after it, up to
// a blank line. A WebVTT cue line can carry settings after the end time.
func subripCues(text string) []timeSpan {
	var cues []timeSpan
	lines := strings.Split(text, "\n")
	for at := 0; at < len(lines); at++ {
		left, right, timed := strings.Cut(lines[at], "-->")
		if !timed {
			continue
		}
		start, startRead := cueTime(left)
		end, endRead := cueTime(firstField(right))
		var words []string
		for at+1 < len(lines) && strings.TrimSpace(lines[at+1]) != "" {
			at++
			words = append(words, lines[at])
		}
		if startRead && endRead && spoken(strings.Join(words, " ")) {
			cues = append(cues, timeSpan{Start: start, End: end})
		}
	}
	return cues
}

// The dialogue cues of an Advanced SubStation or a SubStation file: each
// Dialogue line of the events, whose second and third fields are the times
// and whose tenth is the text, which can itself hold commas.
func substationCues(text string) []timeSpan {
	var cues []timeSpan
	for line := range strings.SplitSeq(text, "\n") {
		event, held := strings.CutPrefix(strings.TrimSpace(line), "Dialogue:")
		if !held {
			continue
		}
		fields := strings.SplitN(event, ",", 10)
		if len(fields) < 10 {
			continue
		}
		start, startRead := cueTime(fields[1])
		end, endRead := cueTime(fields[2])
		if startRead && endRead && spoken(fields[9]) {
			cues = append(cues, timeSpan{Start: start, End: end})
		}
	}
	return cues
}

func firstField(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// One cue time in milliseconds: hours, minutes, and seconds with a fraction,
// as 00:01:38,500 in SubRip, 01:38.500 in WebVTT, and 0:01:38.50 in
// SubStation.
func cueTime(text string) (int64, bool) {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), ",", "."), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil || seconds < 0 {
		return 0, false
	}
	total := seconds
	for at, scale := len(parts)-2, 60.0; at >= 0; at, scale = at-1, scale*60 {
		whole, err := strconv.Atoi(parts[at])
		if err != nil || whole < 0 {
			return 0, false
		}
		total += float64(whole) * scale
	}
	return int64(total*1000 + 0.5), true
}

// Whether a cue's text is a line someone speaks. Markup is not text, a cue
// with no text marks nothing, and a cue that a note mark opens or closes is
// a song or a score, which plays over the credits and the intro.
func spoken(text string) bool {
	text = stripMarkup(text)
	if text == "" {
		return false
	}
	for _, note := range []string{"♪", "♫", "#"} {
		if strings.HasPrefix(text, note) || strings.HasSuffix(text, note) {
			return false
		}
	}
	return true
}

// The words of a cue with the HTML tags of SubRip and WebVTT, the override
// blocks of SubStation, and SubStation's line breaks taken out.
func stripMarkup(text string) string {
	var words strings.Builder
	depth := 0
	for _, r := range text {
		switch {
		case r == '<' || r == '{':
			depth++
		case (r == '>' || r == '}') && depth > 0:
			depth--
		case depth == 0:
			words.WriteRune(r)
		}
	}
	plain := strings.NewReplacer(`\N`, " ", `\n`, " ", `\h`, " ").Replace(words.String())
	return strings.TrimSpace(plain)
}

// The dialogue the cues make, in time order. Cues that overlap make one run
// of dialogue, and only a stretch long enough for one of the rules is kept.
func dialogueOf(cues []timeSpan) (subtitleDialogue, bool) {
	if len(cues) == 0 {
		return subtitleDialogue{}, false
	}
	slices.SortStableFunc(cues, func(a, b timeSpan) int { return int(a.Start - b.Start) })
	dialogue := subtitleDialogue{First: cues[0].Start}
	reach := cues[0].End
	for _, cue := range cues[1:] {
		if cue.Start-reach >= subtitleIntroShortest {
			dialogue.Gaps = append(dialogue.Gaps, timeSpan{Start: reach, End: cue.Start})
		}
		reach = max(reach, cue.End)
	}
	dialogue.Last = reach
	return dialogue, true
}

// Where the credits start, from the dialogue and the video's length. The
// credits start at the end of the last line when a quiet stretch of credits
// length runs from it to the end of the file. They start earlier, at the
// start of the last such stretch in the second half, when only a scene's
// worth of dialogue follows that stretch: the scene after the credits, which
// either ends the file or is followed by more credits. An answer in the
// first half is an opening, not the credits.
func (d subtitleDialogue) creditsStart(duration int64) (int64, bool) {
	var stretch *timeSpan
	for at := range d.Gaps {
		gap := d.Gaps[at]
		if gap.End-gap.Start >= subtitleCreditsGap && 2*gap.Start >= duration {
			stretch = &d.Gaps[at]
		}
	}
	scene := stretch != nil && d.Last-stretch.End <= subtitleSceneLength
	start := int64(-1)
	switch {
	case scene:
		start = stretch.Start
	case duration-d.Last >= subtitleCreditsGap:
		start = d.Last
	}
	if start < 0 || 2*start < duration || start >= duration {
		return 0, false
	}
	return start, true
}

// The intro: the first quiet stretch of an intro's length that ends in the
// first ten minutes and the first half of the file. The stretch before the
// first line counts, for a file that opens on its title sequence. A stretch
// always starts at the start of the file or at the end of a line, so every
// stretch here starts after a cue or at the start.
func (d subtitleDialogue) introSpan(duration int64) (timeSpan, bool) {
	window := min(subtitleIntroWindow, duration/2)
	stretches := append([]timeSpan{{Start: 0, End: d.First}}, d.Gaps...)
	for _, stretch := range stretches {
		if stretch.End > window {
			break
		}
		if length := stretch.End - stretch.Start; length >= subtitleIntroShortest && length <= subtitleIntroLongest {
			return stretch, true
		}
	}
	return timeSpan{}, false
}

// The dialogue as the catalog's dialogue column holds it: JSON, and empty for
// a file with none.
func (d subtitleDialogue) column() string {
	// A struct of whole numbers always encodes.
	data, _ := json.Marshal(d)
	return string(data)
}

// The dialogue a column holds, or nil for an empty column or one that does
// not decode.
func dialogueColumn(text string) *subtitleDialogue {
	if text == "" {
		return nil
	}
	var dialogue subtitleDialogue
	if err := json.Unmarshal([]byte(text), &dialogue); err != nil {
		return nil
	}
	return &dialogue
}
