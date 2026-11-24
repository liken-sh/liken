package main

// The cases are in testdata/subtitle-rule.json. The media browser in
// library-operator applies the same rule to show which track a play will
// choose. The roles of that rule are in the media-screen crate, which the
// browser and the display both link, and the choice is in the browser. The
// tests of both crates read the same file, so a change to the rule in one
// program fails a test until the other program matches.

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// ruleTrack is one track of the shared cases. An external track has a
// file, and an embedded track has none. The flag values are default,
// forced, and hi. A track with no codec is a text track.
type ruleTrack struct {
	File   string   `json:"file"`
	Lang   string   `json:"lang"`
	Title  string   `json:"title"`
	Codec  string   `json:"codec"`
	Flags  []string `json:"flags"`
	Events *int64   `json:"events"`
}

type ruleCases struct {
	Files    map[string][]ruleTrack `json:"files"`
	Classify []struct {
		Name   string      `json:"name"`
		File   string      `json:"file"`
		Tracks []ruleTrack `json:"tracks"`
		Roles  []string    `json:"roles"`
	} `json:"classify"`
	FileNames []struct {
		Name     string `json:"name"`
		Language string `json:"language"`
		Forced   bool   `json:"forced"`
		SDH      bool   `json:"sdh"`
	} `json:"fileNames"`
	SameLanguage []struct {
		A    string `json:"a"`
		B    string `json:"b"`
		Same bool   `json:"same"`
	} `json:"sameLanguage"`
	Choose []struct {
		Name   string              `json:"name"`
		File   string              `json:"file"`
		Tracks []ruleTrack         `json:"tracks"`
		Audio  string              `json:"audio"`
		Prefs  subtitlePreferences `json:"prefs"`
		Want   int                 `json:"want"`
	} `json:"choose"`
}

func loadRuleCases(t *testing.T) ruleCases {
	t.Helper()
	data, err := os.ReadFile("testdata/subtitle-rule.json")
	if err != nil {
		t.Fatalf("reading testdata/subtitle-rule.json: %v", err)
	}
	var cases ruleCases
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("decoding testdata/subtitle-rule.json: %v", err)
	}
	return cases
}

// The external files of the cases are in this folder, beside a video of
// this name.
const (
	ruleVideoFolder = "/media/1/Movie (2024)/"
	ruleVideoName   = "Movie (2024)."
)

// mpvTracks builds the track-list that mpv reports for the tracks of one
// case, and the counts that library-operator records for them. A track's
// mpv id is its position, starting at 1. An embedded track's container
// index is its id plus one, after the video and audio streams at 0 and 1.
// mpv titles an external file that has no title of its own with the part
// of its name after the video's name.
func mpvTracks(tracks []ruleTrack) ([]mpvTrack, []PlaySubtitle) {
	var list []mpvTrack
	var counts []PlaySubtitle
	for index, one := range tracks {
		id := index + 1
		ffIndex := id + 1
		track := mpvTrack{
			ID: id, Type: trackTypeSub, Lang: one.Lang, Title: one.Title, Codec: one.Codec, FFIndex: &ffIndex,
			Default:         slices.Contains(one.Flags, "default"),
			Forced:          slices.Contains(one.Flags, "forced"),
			HearingImpaired: slices.Contains(one.Flags, "hi"),
		}
		count := PlaySubtitle{Stream: &ffIndex, Events: one.Events}
		if one.File != "" {
			zero := 0
			track.FFIndex = &zero
			track.External = true
			track.ExternalFilename = ruleVideoFolder + one.File
			track.Title = strings.TrimPrefix(one.File, ruleVideoName)
			count = PlaySubtitle{File: one.File, Events: one.Events}
		}
		list = append(list, track)
		if one.Events != nil {
			counts = append(counts, count)
		}
	}
	return list, counts
}

// surveyFile is one file of the shared cases: the subtitle tracks as mpv
// reports them, and the counts that library-operator records for them.
type surveyFile struct {
	tracks []mpvTrack
	counts []PlaySubtitle
}

// survey builds the file of the shared cases with this name.
func survey(t *testing.T, name string) surveyFile {
	t.Helper()
	tracks, counts := mpvTracks(loadRuleCases(t).Files[name])
	return surveyFile{tracks: tracks, counts: counts}
}

func audio(lang string) mpvTrack {
	return mpvTrack{ID: 1, Type: trackTypeAudio, Lang: lang, Selected: true}
}

// english returns the preferences of a household that reads and hears
// English.
func english(mode, sdh string) subtitlePreferences {
	return subtitlePreferences{
		Subtitles:         mode,
		SDH:               sdh,
		AudioLanguages:    []string{"en"},
		SubtitleLanguages: []string{"en"},
	}
}

// caseTracks returns a case's own tracks, or the tracks of the file it
// names.
func caseTracks(cases ruleCases, file string, tracks []ruleTrack) []ruleTrack {
	if file != "" {
		return cases.Files[file]
	}
	return tracks
}

// Each track gets one role from its flags, title, file name, and event
// count. A track with no count gets its role from the other three.
func TestClassifySubtitleTracks(t *testing.T) {
	cases := loadRuleCases(t)
	for _, one := range cases.Classify {
		t.Run(one.Name, func(t *testing.T) {
			tracks, counts := mpvTracks(caseTracks(cases, one.File, one.Tracks))
			var got []string
			for _, track := range classifySubtitleTracks(tracks, counts) {
				got = append(got, track.role)
			}
			mustMatchAll(t, got, one.Roles)
		})
	}
}

// mpv reads no language and no marker from a name that ends in a number,
// so the sidecar parses the name itself.
func TestSubtitleFileName(t *testing.T) {
	for _, one := range loadRuleCases(t).FileNames {
		t.Run(one.Name, func(t *testing.T) {
			got := parseSubtitleFileName(one.Name)
			mustMatch(t, got, subtitleFileName{language: one.Language, forced: one.Forced, sdh: one.SDH})
		})
	}
}

// Preferences and file names write a language in several forms, such as en,
// eng, and en-US, and the rule treats them as the same language.
func TestSameLanguage(t *testing.T) {
	for _, one := range loadRuleCases(t).SameLanguage {
		t.Run(one.A+" and "+one.B, func(t *testing.T) {
			mustMatch(t, sameLanguage(one.A, one.B), one.Same)
		})
	}
}

// keepsMpvsChoice is the value of chosen when the rule changes nothing and
// mpv's own choice stays. No mpv id is negative.
const keepsMpvsChoice = -1

// chosen returns the mpv id of the track the rule chooses, 0 for no
// subtitles, or keepsMpvsChoice. The selected audio track comes first in
// the track-list, as it does in mpv's.
func chosen(tracks []ruleTrack, audioLanguage string, prefs subtitlePreferences) int {
	list, counts := mpvTracks(tracks)
	choice := chooseSubtitle(classifySubtitleTracks(append([]mpvTrack{audio(audioLanguage)}, list...), counts), audioLanguage, prefs)
	switch {
	case choice.keep:
		return keepsMpvsChoice
	case choice.track == nil:
		return 0
	}
	return choice.track.ID
}

func TestChooseSubtitle(t *testing.T) {
	cases := loadRuleCases(t)
	for _, one := range cases.Choose {
		t.Run(one.Name, func(t *testing.T) {
			mustMatch(t, chosen(caseTracks(cases, one.File, one.Tracks), one.Audio, one.Prefs), one.Want)
		})
	}
}

// A cases file that fails to parse into these fields, or loses a section,
// fails here. Otherwise the tests that loop over that section would run no
// case and pass.
func TestTheSharedCasesHoldEverySection(t *testing.T) {
	cases := loadRuleCases(t)
	mustMatch(t, len(cases.Classify) > 0 && len(cases.FileNames) > 0 &&
		len(cases.SameLanguage) > 0 && len(cases.Choose) > 0, true)
}
