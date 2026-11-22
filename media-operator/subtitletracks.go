package main

// mpv picks a subtitle track from the track flags and the --slang order.
// Real files often have wrong flags: a forced track often has the default
// flag, and some forced tracks have no marker at all. So the command
// sidecar gives each subtitle track a role from its flags, title, file
// name, and event count, and chooses a track by role.
//
// A forced track translates only the parts of the audio that are in
// another language, so it is chosen by the language of the audio that
// plays. A full or SDH track is chosen by the subtitle languages.

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/language"
)

const (
	trackTypeAudio = "audio"
	trackTypeSub   = "sub"
)

// The values of subtitleTrack.role.
const (
	roleEmpty      = "empty"
	roleForced     = "forced"
	roleCommentary = "commentary"
	roleSDH        = "sdh"
	roleFull       = "full"
)

// The values of subtitlePreferences.SDH.
const (
	sdhPrefer = "prefer"
	sdhAvoid  = "avoid"
)

// A track with fewer than 1/forcedShare of the events of the largest track
// in its language counts as forced, even when no flag or title says so.
const forcedShare = 5

// The codecs of the subtitle tracks that are pictures: the PGS track of a
// Blu-ray and the VobSub track of a DVD. mpv draws such a track as it is
// on the disc, so the house style of the display's subtitles cannot change
// its font. Every other codec, such as subrip and ass, is text.
const (
	codecPGS    = "hdmv_pgs_subtitle"
	codecVobSub = "dvd_subtitle"
)

// mpvTrack is one entry of mpv's track-list property. FFIndex is the
// stream's index in its container, the same index that ffprobe reports.
// An external file is its own container, so its FFIndex is 0 and only its
// file name identifies it.
type mpvTrack struct {
	ID               int    `json:"id"`
	Type             string `json:"type"`
	Lang             string `json:"lang"`
	Title            string `json:"title"`
	Codec            string `json:"codec"`
	Default          bool   `json:"default"`
	Forced           bool   `json:"forced"`
	HearingImpaired  bool   `json:"hearing-impaired"`
	External         bool   `json:"external"`
	ExternalFilename string `json:"external-filename"`
	FFIndex          *int   `json:"ff-index"`
	Selected         bool   `json:"selected"`
}

// isImage reports whether the track is a set of pictures. A track with no
// codec counts as text, and so does an external file.
func (track mpvTrack) isImage() bool {
	return track.Codec == codecPGS || track.Codec == codecVobSub
}

// subtitleTrack is one subtitle track with its role. language is the base
// language, such as en for eng and en-US, and is empty when neither mpv
// nor the file name gives one. events is nil when the presentation has no
// count for the track.
type subtitleTrack struct {
	track    mpvTrack
	language string
	role     string
	events   *int64

	// Markers read from the flags, the title, and the file name.
	forced     bool
	sdh        bool
	commentary bool
}

// classifySubtitleTracks returns the subtitle tracks of track-list in
// order, each with its role. counts is the item's presentation.subtitles.
func classifySubtitleTracks(tracks []mpvTrack, counts []PlaySubtitle) []subtitleTrack {
	var subtitles []subtitleTrack
	for _, track := range tracks {
		if track.Type != trackTypeSub {
			continue
		}
		subtitles = append(subtitles, describeSubtitleTrack(track, counts))
	}
	// A track's role depends on the largest event count in its language,
	// so every track's count is read before any role is set.
	largest := map[string]int64{}
	for _, one := range subtitles {
		if one.events != nil {
			largest[one.language] = max(largest[one.language], *one.events)
		}
	}
	for index := range subtitles {
		one := &subtitles[index]
		one.role = subtitleRole(*one, largest[one.language])
	}
	return subtitles
}

// describeSubtitleTrack reads one track's language, markers, and event
// count. mpv reads the language and markers of an external file from its
// name, but not when the name ends in a number, as in Movie.eng.0.srt. So
// the sidecar parses the name too, and uses its language when mpv gives
// none and its markers in addition to mpv's flags.
func describeSubtitleTrack(track mpvTrack, counts []PlaySubtitle) subtitleTrack {
	one := subtitleTrack{
		track:    track,
		language: canonicalLanguage(track.Lang),
		forced:   track.Forced,
		sdh:      track.HearingImpaired,
	}
	title := track.Title
	if track.External {
		name := path.Base(track.ExternalFilename)
		fromName := parseSubtitleFileName(name)
		if one.language == "" {
			one.language = fromName.language
		}
		one.forced = one.forced || fromName.forced
		one.sdh = one.sdh || fromName.sdh
		// mpv titles a file that has no title of its own with the end of
		// its file name. Ignore such a title, because a word from the
		// film's name, such as Force, would match as a marker.
		if strings.HasSuffix(name, title) {
			title = ""
		}
	}
	lower := strings.ToLower(title)
	if strings.Contains(lower, "forc") {
		one.forced = true
	}
	if slices.ContainsFunc(titleWords(lower), isHearingImpairedWord) {
		one.sdh = true
	}
	one.commentary = strings.Contains(lower, "commentary")
	one.events = subtitleEvents(track, counts)
	return one
}

// subtitleRole returns the role of a track. A track with no events is
// empty whatever its flags say. A forced marker, or fewer than
// 1/forcedShare of the largest count, outranks a commentary title and an
// SDH marker. A track with no count is not empty, and its role comes from
// its markers alone.
func subtitleRole(one subtitleTrack, largest int64) string {
	switch {
	case one.events != nil && *one.events == 0:
		return roleEmpty
	case one.forced:
		return roleForced
	case one.events != nil && *one.events*forcedShare < largest:
		return roleForced
	case one.commentary:
		return roleCommentary
	case one.sdh:
		return roleSDH
	}
	return roleFull
}

// subtitleEvents returns a track's event count from the presentation, or
// nil. An embedded track matches by its container index. An external file
// matches by base name, because the resolver rewrites the media's path for
// the pod but keeps the base name.
func subtitleEvents(track mpvTrack, counts []PlaySubtitle) *int64 {
	for _, count := range counts {
		switch {
		case track.External && count.File != "" && count.File == path.Base(track.ExternalFilename):
			return count.Events
		case !track.External && count.Stream != nil && track.FFIndex != nil && *count.Stream == *track.FFIndex:
			return count.Events
		}
	}
	return nil
}

// titleWords splits a title into words, so a marker matches only as a
// whole word: sdh in "English SDH", but not hi in "Chinese".
func titleWords(title string) []string {
	return strings.FieldsFunc(title, func(r rune) bool {
		return !unicode.IsLetter(r)
	})
}

// isHearingImpairedWord reports whether the word is sdh, cc, or hi, the
// markers that Bazarr, mpv, and release groups use for a track with sound
// descriptions for the hearing impaired.
func isHearingImpairedWord(word string) bool {
	switch strings.ToLower(word) {
	case "sdh", "cc", "hi":
		return true
	}
	return false
}

type subtitleFileName struct {
	language string
	forced   bool
	sdh      bool
}

// parseSubtitleFileName reads the dot-separated segments of a name from
// the end, before the extension. It skips numbers and records the markers
// forced, sdh, hi, cc, and default. The first other segment is the
// language, or no language when it is not a language code. The media's own
// name can contain dots, so the parse stops at that segment.
func parseSubtitleFileName(name string) subtitleFileName {
	var parsed subtitleFileName
	segments := strings.Split(strings.TrimSuffix(name, path.Ext(name)), ".")
	// Segment 0 is the media's name, not a language or a marker.
	for index := len(segments) - 1; index > 0; index-- {
		segment := strings.ToLower(segments[index])
		switch {
		case segment != "" && strings.Trim(segment, "0123456789") == "":
		case segment == "forced":
			parsed.forced = true
		case isHearingImpairedWord(segment):
			parsed.sdh = true
		case segment == "default":
		default:
			parsed.language = fileNameLanguage(segment)
			return parsed
		}
	}
	return parsed
}

// fileNameLanguage returns the base language of a segment, or empty when
// its primary subtag is not two or three letters, which is the shape mpv
// accepts as a language in a file name.
func fileNameLanguage(segment string) string {
	primary, _, _ := strings.Cut(segment, "-")
	if len(primary) < 2 || len(primary) > 3 {
		return ""
	}
	return canonicalLanguage(segment)
}

// canonicalLanguage returns the base language of a tag, so that en, eng,
// and en-US all give en, and de, deu, and ger all give de. It returns
// empty for und, for an empty tag, and for a word that is not a language
// code.
func canonicalLanguage(tag string) string {
	if tag == "" {
		return ""
	}
	parsed, err := language.Parse(tag)
	if err != nil {
		return ""
	}
	base, confidence := parsed.Base()
	if confidence != language.Exact {
		return ""
	}
	return base.String()
}

// sameLanguage reports whether two tags have the same base language. A tag
// with no language matches nothing, not even itself.
func sameLanguage(a, b string) bool {
	canonical := canonicalLanguage(a)
	return canonical != "" && canonical == canonicalLanguage(b)
}

// subtitlePreferences is the part of the resolved preferences that the
// command sidecar reads. The operator writes it into the pod as JSON only
// in the modes on and auto, because in the other modes the sidecar
// chooses no track.
type subtitlePreferences struct {
	Subtitles         string   `json:"subtitles"`
	SDH               string   `json:"sdh,omitempty"`
	AudioLanguages    []string `json:"audioLanguages,omitempty"`
	SubtitleLanguages []string `json:"subtitleLanguages,omitempty"`
}

// subtitleChoice is the result of chooseSubtitle. reason is a clause for
// the log line. A nil track means no subtitles. keep means that the sidecar
// leaves mpv's own choice in place.
type subtitleChoice struct {
	track  *mpvTrack
	reason string
	keep   bool
}

// chooseSubtitle chooses from the classified tracks of one file.
// audioLanguage is the language of the audio track that mpv selected. The
// languages to read are subtitleLanguages, or audioLanguages when
// subtitleLanguages is empty. When both are empty, mpv's own choice stays.
//
// In auto mode, audio in the first of audioLanguages gets only its forced
// track, or no subtitles. Otherwise it chooses, in order, a full or SDH
// track in a language to read (a text track before an image track, when
// the role ranks the same), the forced track of the audio when
// audioLanguages has the audio's language, a forced track in a language
// to read, a full or SDH track with no language, and a commentary track in
// a language to read. It never chooses an empty track.
func chooseSubtitle(tracks []subtitleTrack, audioLanguage string, prefs subtitlePreferences) subtitleChoice {
	reading := prefs.SubtitleLanguages
	if len(reading) == 0 {
		reading = prefs.AudioLanguages
	}
	if len(reading) == 0 {
		return subtitleChoice{keep: true, reason: "neither subtitleLanguages nor audioLanguages names a language"}
	}
	forced := forcedTrackOf(tracks, audioLanguage)
	if prefs.Subtitles == subtitlesAuto && len(prefs.AudioLanguages) > 0 &&
		sameLanguage(audioLanguage, prefs.AudioLanguages[0]) {
		if forced != nil {
			return subtitleChoice{track: forced, reason: "it is the forced track of audio in the first audio language"}
		}
		return subtitleChoice{reason: "audio in the first audio language has no forced track"}
	}
	for _, wanted := range reading {
		if pick := bestTrack(tracks, wanted, readerRank(prefs.SDH), roleFull, roleSDH); pick != nil {
			return subtitleChoice{track: pick, reason: "it ranks first of the full and SDH tracks in " + wanted}
		}
	}
	if forced != nil && slices.ContainsFunc(prefs.AudioLanguages, func(wanted string) bool {
		return sameLanguage(audioLanguage, wanted)
	}) {
		return subtitleChoice{track: forced, reason: "no full or SDH track is in a subtitle language, and it is the forced track of the audio"}
	}
	// A track flagged forced is often the full translation of a foreign
	// show with the wrong flag, so a forced track in a language to read is
	// better than none.
	for _, wanted := range reading {
		if pick := bestTrack(tracks, wanted, readerRank(""), roleForced); pick != nil {
			return subtitleChoice{track: pick, reason: "no full or SDH track is in a subtitle language, and it is the forced track in " + wanted}
		}
	}
	// A track with no language, such as a file named after the video alone,
	// is usually in the language of the release. It ranks above a
	// commentary track, because commentary text follows the commentary
	// audio and not the film.
	if pick := untaggedTrack(tracks, readerRank(prefs.SDH)); pick != nil {
		return subtitleChoice{track: pick, reason: "no track is in a subtitle language, and it is a full or SDH track that names no language"}
	}
	for _, wanted := range reading {
		if pick := bestTrack(tracks, wanted, readerRank(""), roleCommentary); pick != nil {
			return subtitleChoice{track: pick, reason: "no other track is in a subtitle language, and it is the commentary in " + wanted}
		}
	}
	return subtitleChoice{reason: "no track is in a subtitle language or names no language"}
}

func forcedTrackOf(tracks []subtitleTrack, audioLanguage string) *mpvTrack {
	return bestTrack(tracks, audioLanguage, readerRank(""), roleForced)
}

// readerRank ranks the full and SDH roles for the SDH preference, lower
// first. With no preference the two roles rank the same.
func readerRank(sdh string) func(role string) int {
	return func(role string) int {
		switch {
		case sdh == sdhPrefer && role == roleSDH, sdh == sdhAvoid && role == roleFull:
			return 0
		case sdh == sdhPrefer, sdh == sdhAvoid:
			return 1
		}
		return 0
	}
}

func bestTrack(tracks []subtitleTrack, wanted string, rank func(string) int, roles ...string) *mpvTrack {
	return bestOf(tracks, func(language string) bool { return sameLanguage(language, wanted) }, rank, roles)
}

func untaggedTrack(tracks []subtitleTrack, rank func(string) int) *mpvTrack {
	return bestOf(tracks, func(language string) bool { return language == "" }, rank, []string{roleFull, roleSDH})
}

// bestOf returns the track whose language matches and whose role is one
// of roles, or nil. It orders by the rank of the role, then a text track
// before an image track, then the default flag, then track order. The
// display draws a text track in the house style and an image track as the
// disc made it, so a text track is the better pick when the role is the
// same. The role still outranks the kind, so with sdh prefer an SDH image
// track goes before a full text track.
func bestOf(tracks []subtitleTrack, matches func(string) bool, rank func(string) int, roles []string) *mpvTrack {
	var candidates []subtitleTrack
	for _, one := range tracks {
		if slices.Contains(roles, one.role) && matches(one.language) {
			candidates = append(candidates, one)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	slices.SortStableFunc(candidates, func(a, b subtitleTrack) int {
		if order := cmp.Compare(rank(a.role), rank(b.role)); order != 0 {
			return order
		}
		if order := cmp.Compare(imageOrder(a.track), imageOrder(b.track)); order != 0 {
			return order
		}
		return cmp.Compare(defaultOrder(a.track), defaultOrder(b.track))
	})
	return &candidates[0].track
}

func imageOrder(track mpvTrack) int {
	if track.isImage() {
		return 1
	}
	return 0
}

func defaultOrder(track mpvTrack) int {
	if track.Default {
		return 0
	}
	return 1
}

func selectedAudioLanguage(tracks []mpvTrack) string {
	for _, track := range tracks {
		if track.Type == trackTypeAudio && track.Selected {
			return track.Lang
		}
	}
	return ""
}
