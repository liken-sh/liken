package main

// These tests cover the subtitle rule of plan 90: how the command sidecar
// gives each subtitle track of a file a role, and which track it then
// chooses. The cases are in testdata/subtitle-rule.json, because the media
// browser in library-operator applies the same rule to mark the track a
// play will show, and its tests read the same file. A change to the rule
// in one program fails a test until the other program agrees.

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// ruleTrack is one track of the shared cases. An embedded track gives its
// tag, title, and flags. An external track gives its file name, and the
// language and flags that mpv reads from that name. The flags are the
// words default, forced, and hi.
type ruleTrack struct {
	File   string   `json:"file"`
	Lang   string   `json:"lang"`
	Title  string   `json:"title"`
	Flags  []string `json:"flags"`
	Events *int64   `json:"events"`
}

// ruleCases is the whole file of shared cases.
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

// loadRuleCases reads the shared cases once for each test.
func loadRuleCases(t *testing.T) ruleCases {
	t.Helper()
	data, err := os.ReadFile("testdata/subtitle-rule.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases ruleCases
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

// The video every external file of the cases sits beside.
const (
	ruleVideoFolder = "/media/1/Movie (2024)/"
	ruleVideoName   = "Movie (2024)."
)

// mpvTracks builds the track-list that mpv gives for the tracks of one
// case, and the counts that library-operator records for them. Each
// track's mpv id is its position from 1. An embedded track's container
// index is one more than its id, because the video stream holds index 0.
// mpv titles an external file with no title of its own by the part of its
// name after the video's name.
func mpvTracks(tracks []ruleTrack) ([]mpvTrack, []PlaySubtitle) {
	var list []mpvTrack
	var counts []PlaySubtitle
	for index, one := range tracks {
		id := index + 1
		ffIndex := id + 1
		track := mpvTrack{
			ID: id, Type: trackTypeSub, Lang: one.Lang, Title: one.Title, FFIndex: &ffIndex,
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

// surveyFile is one file of the shared cases as mpv and the presentation
// give it: its subtitle tracks and the counts that library-operator
// records for them.
type surveyFile struct {
	tracks []mpvTrack
	counts []PlaySubtitle
}

// survey builds the survey file of the shared cases under this name.
func survey(t *testing.T, name string) surveyFile {
	t.Helper()
	tracks, counts := mpvTracks(loadRuleCases(t).Files[name])
	return surveyFile{tracks: tracks, counts: counts}
}

// audio builds the audio track mpv selected.
func audio(lang string) mpvTrack {
	return mpvTrack{ID: 1, Type: trackTypeAudio, Lang: lang, Selected: true}
}

// english is a household that reads and hears English.
func english(mode, sdh string) subtitlePreferences {
	return subtitlePreferences{
		Subtitles:         mode,
		SDH:               sdh,
		AudioLanguages:    []string{"en"},
		SubtitleLanguages: []string{"en"},
	}
}

// caseTracks is a case's own tracks, or the survey file it names.
func caseTracks(cases ruleCases, file string, tracks []ruleTrack) []ruleTrack {
	if file != "" {
		return cases.Files[file]
	}
	return tracks
}

// Each track takes one role from its flags, its title, its file name, and its
// event count. A track with no count takes its role from the other three.
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

// mpv reads no language and no marker from a name that ends in a number, so
// the sidecar reads the name's segments itself. The media's own name can hold
// dots, so the walk starts from the end and stops at the first segment that is
// not a number, a marker, or a language.
func TestSubtitleFileName(t *testing.T) {
	for _, one := range loadRuleCases(t).FileNames {
		t.Run(one.Name, func(t *testing.T) {
			got := parseSubtitleFileName(one.Name)
			mustMatch(t, got, subtitleFileName{language: one.Language, forced: one.Forced, sdh: one.SDH})
		})
	}
}

// The preferences name a language in any of the forms people and files use,
// and the rule treats each form as the same language.
func TestSameLanguage(t *testing.T) {
	for _, one := range loadRuleCases(t).SameLanguage {
		t.Run(one.A+" and "+one.B, func(t *testing.T) {
			mustMatch(t, sameLanguage(one.A, one.B), one.Same)
		})
	}
}

// keepsMpvsChoice marks a case in which the rule sets nothing, so mpv's own
// choice stands. No mpv id is negative.
const keepsMpvsChoice = -1

// chosen is the mpv id of the track the rule chooses, 0 for none, or
// keepsMpvsChoice. The selected audio track comes first in the track-list,
// as it does in mpv's.
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

// The rule of plan 90: a full or SDH track in the order of the subtitle
// languages, then the forced track that belongs to the audio, and a
// commentary track last. In auto, audio in the first language takes only its
// forced track.
func TestChooseSubtitle(t *testing.T) {
	cases := loadRuleCases(t)
	for _, one := range cases.Choose {
		t.Run(one.Name, func(t *testing.T) {
			mustMatch(t, chosen(caseTracks(cases, one.File, one.Tracks), one.Audio, one.Prefs), one.Want)
		})
	}
}

// The file holds cases of every kind, so a file that fails to parse into
// these fields, or loses a section, fails here and not as a test that ran
// no case.
func TestTheSharedCasesHoldEverySection(t *testing.T) {
	cases := loadRuleCases(t)
	mustMatch(t, len(cases.Classify) > 0 && len(cases.FileNames) > 0 &&
		len(cases.SameLanguage) > 0 && len(cases.Choose) > 0, true)
}
