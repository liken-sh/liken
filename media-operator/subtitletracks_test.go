package main

// These tests cover the subtitle rule of plan 90: how the command sidecar
// gives each subtitle track of a file a role, and which track it then
// chooses. The rows are files from a survey of a home cluster's catalog,
// reduced to the English tracks' flags, titles, and event counts.

import (
	"strings"
	"testing"
)

// embedded builds one subtitle track of a container, as mpv's track-list
// gives it. The container index is one more than mpv's id, because the
// video stream holds index 0 in every row here. The flags are the words
// default, forced, and hi.
func embedded(id int, lang, title string, flags ...string) mpvTrack {
	index := id + 1
	track := mpvTrack{ID: id, Type: trackTypeSub, Lang: lang, Title: title, FFIndex: &index}
	for _, flag := range flags {
		switch flag {
		case "default":
			track.Default = true
		case "forced":
			track.Forced = true
		case "hi":
			track.HearingImpaired = true
		}
	}
	return track
}

// external builds one subtitle file beside the media, as mpv's track-list
// gives it after --sub-auto loads it. mpv reads the language and the
// markers from the name, except from a name that ends in a number, so the
// caller states what mpv read.
func external(id int, name, lang string, flags ...string) mpvTrack {
	track := embedded(id, lang, "", flags...)
	zero := 0
	track.FFIndex = &zero
	track.External = true
	track.ExternalFilename = "/media/1/Movie (2024)/" + name
	// mpv titles a file with no title of its own by the part of its name
	// after the media's name.
	track.Title = strings.TrimPrefix(name, "Movie (2024).")
	return track
}

// audio builds the audio track mpv selected.
func audio(lang string) mpvTrack {
	return mpvTrack{ID: 1, Type: trackTypeAudio, Lang: lang, Selected: true}
}

// eventsOf is the count library-operator records for the embedded track
// with mpv's id, keyed by its container index the way embedded numbers it.
func eventsOf(id int, events int64) PlaySubtitle {
	index := id + 1
	return PlaySubtitle{Stream: &index, Events: &events}
}

// fileEvents is the count library-operator records for one external file.
func fileEvents(name string, events int64) PlaySubtitle {
	return PlaySubtitle{File: name, Events: &events}
}

// A file from the survey: its tracks, the counts the presentation carries,
// and the language of the audio that plays.
type surveyFile struct {
	tracks []mpvTrack
	counts []PlaySubtitle
}

var (
	pluribus = surveyFile{
		tracks: []mpvTrack{
			embedded(1, "eng", "Forced", "forced", "default"),
			embedded(2, "eng", ""),
			embedded(3, "eng", "SDH"),
		},
		counts: []PlaySubtitle{eventsOf(1, 4), eventsOf(2, 569), eventsOf(3, 708)},
	}
	falcon = surveyFile{
		tracks: []mpvTrack{
			embedded(1, "eng", "Forced foreign language only", "forced", "default"),
			embedded(2, "eng", "English SDH"),
		},
		counts: []PlaySubtitle{eventsOf(1, 14), eventsOf(2, 622)},
	}
	swarm = surveyFile{
		tracks: []mpvTrack{
			embedded(1, "eng", "English Force", "forced", "default"),
			embedded(2, "eng", "English"),
		},
		counts: []PlaySubtitle{eventsOf(1, 53), eventsOf(2, 382)},
	}
	hawkeye = surveyFile{
		tracks: []mpvTrack{
			embedded(1, "eng", "", "default"),
			embedded(2, "eng", ""),
			embedded(3, "eng", ""),
		},
		counts: []PlaySubtitle{eventsOf(1, 22), eventsOf(2, 0), eventsOf(3, 1382)},
	}
	genV = surveyFile{
		tracks: []mpvTrack{
			embedded(1, "eng", "", "default"),
			embedded(2, "eng", ""),
			embedded(3, "eng", ""),
		},
		counts: []PlaySubtitle{eventsOf(1, 1288), eventsOf(2, 0), eventsOf(3, 1400)},
	}
	horrorStory = surveyFile{
		tracks: []mpvTrack{
			embedded(1, "eng", "", "default"),
			embedded(2, "eng", ""),
		},
		counts: []PlaySubtitle{eventsOf(1, 884), eventsOf(2, 1058)},
	}
	germanForced = surveyFile{
		tracks: []mpvTrack{
			embedded(1, "ger", "Forced", "default"),
			embedded(2, "eng", "English"),
		},
	}
	bazarr = surveyFile{
		tracks: []mpvTrack{
			external(1, "Movie (2024).en.srt", "en"),
			external(2, "Movie (2024).en.hi.srt", "en", "hi"),
			external(3, "Movie (2024).eng.forced.srt", "eng", "forced"),
			external(4, "Movie (2024).eng.0.srt", ""),
		},
		counts: []PlaySubtitle{
			fileEvents("Movie (2024).en.srt", 1210),
			fileEvents("Movie (2024).en.hi.srt", 1302),
			fileEvents("Movie (2024).eng.forced.srt", 31),
			fileEvents("Movie (2024).eng.0.srt", 1188),
		},
	}
)

// roles is the role of each subtitle track, in track order.
func roles(file surveyFile) []string {
	classified := classifySubtitleTracks(file.tracks, file.counts)
	got := make([]string, len(classified))
	for index, track := range classified {
		got[index] = track.role
	}
	return got
}

// Each track takes one role from its flags, its title, its file name, and its
// event count. A track with no count takes its role from the other three.
func TestClassifySubtitleTracks(t *testing.T) {
	cases := []struct {
		name string
		file surveyFile
		want []string
	}{
		{"a forced flag, an untitled track, and an SDH title", pluribus, []string{roleForced, roleFull, roleSDH}},
		{"a forced flag and an SDH title", falcon, []string{roleForced, roleSDH}},
		{"a title with forc in it", swarm, []string{roleForced, roleFull}},
		{"a default track with a fifth of the events, and an empty track", hawkeye, []string{roleForced, roleEmpty, roleFull}},
		{"a default track with most of the events", genV, []string{roleFull, roleEmpty, roleFull}},
		{"two full tracks", horrorStory, []string{roleFull, roleFull}},
		{"a forced title in another language", germanForced, []string{roleForced, roleFull}},
		{"external files named the way Bazarr names them", bazarr, []string{roleFull, roleSDH, roleForced, roleFull}},
		{
			"a short track with no count beside it keeps its role",
			surveyFile{
				tracks: hawkeye.tracks,
				counts: []PlaySubtitle{eventsOf(1, 22)},
			},
			[]string{roleFull, roleFull, roleFull},
		},
		{
			"a title with commentary in it",
			surveyFile{tracks: []mpvTrack{embedded(1, "eng", "Director's Commentary"), embedded(2, "eng", "")}},
			[]string{roleCommentary, roleFull},
		},
		{
			"a hearing-impaired flag and a CC title",
			surveyFile{tracks: []mpvTrack{embedded(1, "eng", "", "hi"), embedded(2, "eng", "English (CC)")}},
			[]string{roleSDH, roleSDH},
		},
		{
			"a file name segment that reads sdh, with a number after it",
			surveyFile{tracks: []mpvTrack{external(1, "Movie (2024).eng.sdh.1.srt", "")}},
			[]string{roleSDH},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatchAll(t, roles(one.file), one.want)
		})
	}
}

// mpv reads no language and no marker from a name that ends in a number, so
// the sidecar reads the name's segments itself. The media's own name can hold
// dots, so the walk starts from the end and stops at the first segment that is
// not a number, a marker, or a language.
func TestSubtitleFileName(t *testing.T) {
	cases := []struct {
		name     string
		language string
		forced   bool
		sdh      bool
	}{
		{"Movie.en.srt", "en", false, false},
		{"Movie.en.hi.srt", "en", false, true},
		{"Movie.eng.forced.srt", "en", true, false},
		{"Movie.eng.0.srt", "en", false, false},
		{"Movie.eng.sdh.1.srt", "en", false, true},
		{"Movie.pt-BR.cc.srt", "pt", false, true},
		{"Movie.srt", "", false, false},
		{"Movie.2.srt", "", false, false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := parseSubtitleFileName(one.name)
			mustMatch(t, got, subtitleFileName{language: one.language, forced: one.forced, sdh: one.sdh})
		})
	}
}

// The preferences name a language in any of the forms people and files use,
// and the rule treats each form as the same language.
func TestSameLanguage(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"en", "eng", true},
		{"en", "en-US", true},
		{"eng", "en-GB", true},
		{"de", "ger", true},
		{"de", "deu", true},
		{"fre", "fra", true},
		{"en", "de", false},
		{"", "", false},
		{"und", "und", false},
	}
	for _, one := range cases {
		t.Run(one.a+" and "+one.b, func(t *testing.T) {
			mustMatch(t, sameLanguage(one.a, one.b), one.want)
		})
	}
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

// keepsMpvsChoice marks a row in which the rule sets nothing, so mpv's own
// choice stands. No mpv id is negative.
const keepsMpvsChoice = -1

// chosen is the mpv id of the track the rule chooses, 0 for none, or
// keepsMpvsChoice.
func chosen(file surveyFile, audioLanguage string, prefs subtitlePreferences) int {
	tracks := append([]mpvTrack{audio(audioLanguage)}, file.tracks...)
	choice := chooseSubtitle(classifySubtitleTracks(tracks, file.counts), audioLanguage, prefs)
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
	cases := []struct {
		name  string
		file  surveyFile
		audio string
		prefs subtitlePreferences
		want  int
	}{
		{"on takes the first full or SDH track, not the forced default", pluribus, "eng", english(subtitlesOn, ""), 2},
		{"on with sdh prefer takes the SDH track", pluribus, "eng", english(subtitlesOn, sdhPrefer), 3},
		{"on with sdh avoid takes the full track", pluribus, "eng", english(subtitlesOn, sdhAvoid), 2},
		{"on takes an SDH track over a forced default", falcon, "eng", english(subtitlesOn, ""), 2},
		{"on with sdh avoid still takes the SDH track when it is the only one", falcon, "eng", english(subtitlesOn, sdhAvoid), 2},
		{"on takes the full track over a title with forc in it", swarm, "eng", english(subtitlesOn, ""), 2},
		{"on takes the long track over a short default and an empty one", hawkeye, "eng", english(subtitlesOn, ""), 3},
		{"on takes the default of two full tracks", genV, "eng", english(subtitlesOn, ""), 1},
		{"on takes the default of two full tracks with no empty one", horrorStory, "eng", english(subtitlesOn, ""), 1},
		{"on never takes a forced track in another language", germanForced, "eng", english(subtitlesOn, ""), 2},
		{"on takes the first external file in track order", bazarr, "eng", english(subtitlesOn, ""), 1},
		{"on with sdh prefer takes the hi file", bazarr, "eng", english(subtitlesOn, sdhPrefer), 2},
		{"auto under audio in the first language takes its forced track", pluribus, "eng", english(subtitlesAuto, ""), 1},
		{"auto under audio in the first language takes the short default", hawkeye, "eng", english(subtitlesAuto, ""), 1},
		{"auto under audio in the first language takes no full track", horrorStory, "eng", english(subtitlesAuto, ""), 0},
		{"auto never takes a forced track in another language", germanForced, "eng", english(subtitlesAuto, ""), 0},
		{"auto under other audio takes a track as on does", pluribus, "jpn", english(subtitlesAuto, ""), 2},
		{"auto reads en-US and eng as the same language", pluribus, "eng", subtitlePreferences{
			Subtitles: subtitlesAuto, AudioLanguages: []string{"en-US"}, SubtitleLanguages: []string{"en"},
		}, 1},
		{
			"on falls back to the forced track of the audio",
			surveyFile{tracks: []mpvTrack{embedded(1, "eng", "Forced", "forced"), embedded(2, "fre", "")}},
			"eng", english(subtitlesOn, ""), 1,
		},
		{
			"on takes no forced track of audio in a language the household does not hear",
			surveyFile{tracks: []mpvTrack{embedded(1, "jpn", "Forced", "forced")}},
			"jpn", english(subtitlesOn, ""), 0,
		},
		{
			"on takes a forced track in a subtitle language under other audio before a commentary track",
			surveyFile{tracks: []mpvTrack{embedded(1, "eng", "Commentary"), embedded(2, "eng", "Forced", "forced")}},
			"jpn", english(subtitlesOn, ""), 2,
		},
		{
			"on takes a commentary track only when nothing else is in the language",
			surveyFile{tracks: []mpvTrack{embedded(1, "eng", "Commentary"), embedded(2, "fre", "")}},
			"jpn", english(subtitlesOn, ""), 1,
		},
		{
			"on takes the only English track under Korean audio although it is flagged forced",
			surveyFile{tracks: []mpvTrack{embedded(1, "eng", "English", "forced")}},
			"kor", english(subtitlesOn, ""), 1,
		},
		{
			"on takes an untagged track when no track is in a subtitle language",
			surveyFile{tracks: []mpvTrack{embedded(1, "ger", ""), embedded(2, "", "")}},
			"eng", english(subtitlesOn, ""), 2,
		},
		{
			"on takes an untagged file with the video's exact name",
			surveyFile{tracks: []mpvTrack{external(1, "Movie (2024).srt", "")}},
			"eng", english(subtitlesOn, ""), 1,
		},
		{
			"on takes a tagged commentary track after an untagged full track",
			surveyFile{tracks: []mpvTrack{embedded(1, "eng", "Commentary"), embedded(2, "", "")}},
			"eng", english(subtitlesOn, ""), 2,
		},
		{
			"on reads an SDH marker from an untagged title",
			surveyFile{tracks: []mpvTrack{embedded(1, "", "English."), embedded(2, "", "English(SDH)"), embedded(3, "", "Spanish")}},
			"eng", english(subtitlesOn, sdhPrefer), 2,
		},
		{
			"on keeps a tagged track's own language over a language in its title",
			surveyFile{tracks: []mpvTrack{embedded(1, "ger", "English"), embedded(2, "eng", "")}},
			"eng", english(subtitlesOn, ""), 2,
		},
		{
			"auto takes no untagged track under audio in the first language",
			surveyFile{tracks: []mpvTrack{embedded(1, "", "")}},
			"eng", english(subtitlesAuto, ""), 0,
		},
		{
			"auto takes an untagged track under other audio",
			surveyFile{tracks: []mpvTrack{embedded(1, "", "")}},
			"jpn", english(subtitlesAuto, ""), 1,
		},
		{
			"on takes the forced track of the audio before a commentary track",
			surveyFile{tracks: []mpvTrack{embedded(1, "eng", "Commentary"), embedded(2, "eng", "Forced", "forced")}},
			"eng", english(subtitlesOn, ""), 2,
		},
		{
			"on takes a full track in the second language before a commentary track in the first",
			surveyFile{tracks: []mpvTrack{embedded(1, "eng", "Commentary"), embedded(2, "spa", "")}},
			"jpn", subtitlePreferences{Subtitles: subtitlesOn, SubtitleLanguages: []string{"en", "es"}}, 2,
		},
		{
			"on with no subtitle languages reads the audio languages",
			pluribus, "jpn", subtitlePreferences{Subtitles: subtitlesOn, SDH: sdhPrefer, AudioLanguages: []string{"en"}}, 3,
		},
		{
			"auto with no subtitle languages reads the audio languages under other audio",
			pluribus, "jpn", subtitlePreferences{Subtitles: subtitlesAuto, AudioLanguages: []string{"en"}}, 2,
		},
		{
			"auto with no subtitle languages still takes the forced track of the first audio language",
			pluribus, "eng", subtitlePreferences{Subtitles: subtitlesAuto, AudioLanguages: []string{"en"}}, 1,
		},
		{
			"on with neither list keeps mpv's choice",
			pluribus, "eng", subtitlePreferences{Subtitles: subtitlesOn}, keepsMpvsChoice,
		},
		{
			"auto with neither list keeps mpv's choice",
			pluribus, "eng", subtitlePreferences{Subtitles: subtitlesAuto}, keepsMpvsChoice,
		},
		{
			"on never takes an empty track",
			surveyFile{tracks: []mpvTrack{embedded(1, "eng", "")}, counts: []PlaySubtitle{eventsOf(1, 0)}},
			"jpn", english(subtitlesOn, ""), 0,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, chosen(one.file, one.audio, one.prefs), one.want)
		})
	}
}
