package main

// The subtitle rule of plan 90. mpv chooses a subtitle track from its
// flags and the --slang order, and the flags in a real catalog are not
// reliable: a forced track often carries the default flag, and some forced
// tracks carry no marker at all. So the command sidecar gives each
// subtitle track one role and chooses by role.
//
// A forced track translates the parts of the audio that are not in the
// audio's own language, so it belongs to the audio track. A full or SDH
// track belongs to the reader. The rule takes a full or SDH track in the
// order of the subtitle languages first. When no such track exists, it
// takes the forced track of the audio that plays, if a person in the
// household hears that language. A commentary track is the last choice,
// because its text follows the commentary audio and not the film. An
// empty track is never a choice.

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/language"
)

// The track types of mpv's track-list that the rule reads.
const (
	trackTypeAudio = "audio"
	trackTypeSub   = "sub"
)

// The roles a subtitle track takes. The words appear in the sidecar's log
// line, so a person reads why a track was chosen.
const (
	roleEmpty      = "empty"
	roleForced     = "forced"
	roleCommentary = "commentary"
	roleSDH        = "sdh"
	roleFull       = "full"
)

// The two values of the sdh preference. An absent value is no preference,
// and the full and SDH roles then rank the same.
const (
	sdhPrefer = "prefer"
	sdhAvoid  = "avoid"
)

// forcedShare sets the fraction of the largest track in the same language,
// one fifth, below which a track counts as forced. Plan 90 holds the
// survey of event counts that the fraction separates.
const forcedShare = 5

// mpvTrack is one entry of mpv's track-list property, with the fields the
// rule reads. FFIndex is the stream's index in its container, which is the
// index ffprobe gives the same stream. An external file is a container of
// its own, so its FFIndex is 0 and only its file name identifies it.
type mpvTrack struct {
	ID               int    `json:"id"`
	Type             string `json:"type"`
	Lang             string `json:"lang"`
	Title            string `json:"title"`
	Default          bool   `json:"default"`
	Forced           bool   `json:"forced"`
	HearingImpaired  bool   `json:"hearing-impaired"`
	External         bool   `json:"external"`
	ExternalFilename string `json:"external-filename"`
	FFIndex          *int   `json:"ff-index"`
	Selected         bool   `json:"selected"`
}

// subtitleTrack is one subtitle track with its role. The language is the
// canonical base language, such as en for eng and en-US, and it is empty
// when neither mpv nor the file name names one. The events are nil when
// the presentation carries no count for the track.
type subtitleTrack struct {
	track    mpvTrack
	language string
	role     string
	events   *int64

	// The markers that the flags, the title, and the file name give.
	forced     bool
	sdh        bool
	commentary bool
}

// classifySubtitleTracks gives each subtitle track of track-list its role,
// in track order. The counts are the item's presentation.subtitles.
func classifySubtitleTracks(tracks []mpvTrack, counts []PlaySubtitle) []subtitleTrack {
	var subtitles []subtitleTrack
	for _, track := range tracks {
		if track.Type != trackTypeSub {
			continue
		}
		subtitles = append(subtitles, describeSubtitleTrack(track, counts))
	}
	// The forced test by count compares each track with the largest track
	// in its language, so it runs after every track has its count.
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

// describeSubtitleTrack reads one track's language, markers, and count.
// mpv reads the language and the markers from the name of an external file,
// except from a name that ends in a number, such as Movie.eng.0.srt. So the
// sidecar reads the name too, and uses what it finds where mpv's own fields
// are empty.
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
		// mpv titles a file with no title of its own by the end of its
		// name, so the title says nothing that the name has not said, and a
		// word of the film's own name, such as Force, would read as a
		// marker.
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

// subtitleRole is the one role of a track, in the order of plan 90: a
// track with no events is empty whatever its flags say, and a forced marker
// outranks a commentary title and an SDH marker. The count test applies
// only when both counts are known, because an absent count differs from
// zero.
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

// subtitleEvents finds a track's count in the presentation. An embedded
// track matches by its container index, and an external file by its base
// name, because the resolver rewrites the media's path for the pod and
// leaves the base name as it is.
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

// titleWords splits a title into its words, so a marker matches only as a
// whole word: SDH in "English SDH", and not hi in "Chinese".
func titleWords(title string) []string {
	return strings.FieldsFunc(title, func(r rune) bool {
		return !unicode.IsLetter(r)
	})
}

// isHearingImpairedWord names the three markers that Bazarr, mpv, and
// release groups write for a track with sound descriptions.
func isHearingImpairedWord(word string) bool {
	switch strings.ToLower(word) {
	case "sdh", "cc", "hi":
		return true
	}
	return false
}

// subtitleFileName is what the segments of an external file's name say.
type subtitleFileName struct {
	language string
	forced   bool
	sdh      bool
}

// parseSubtitleFileName reads the segments of a name from the end, after
// the extension: numbers, the markers forced, sdh, hi, cc, and default, and
// then one language. The media's own name can hold dots, so the walk stops
// at the first segment that is none of these.
func parseSubtitleFileName(name string) subtitleFileName {
	var parsed subtitleFileName
	segments := strings.Split(strings.TrimSuffix(name, path.Ext(name)), ".")
	// The first segment is the media's name, never a language or a marker.
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

// fileNameLanguage reads one segment as a language when its primary subtag
// has two or three letters, the shape that mpv accepts in a file name.
func fileNameLanguage(segment string) string {
	primary, _, _ := strings.Cut(segment, "-")
	if len(primary) < 2 || len(primary) > 3 {
		return ""
	}
	return canonicalLanguage(segment)
}

// canonicalLanguage is the base language of a tag, in one form for each
// language: en for en, eng, and en-US, and de for de, deu, and ger. The
// golang.org/x/text tables hold the ISO 639-1 codes, both ISO 639-2
// codes, and the BCP 47 grammar. A tag that names no language, such as
// und, an empty string, or a word that is not a code, has none.
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

// sameLanguage reports whether two tags name the same base language. A tag
// that names no language matches nothing, itself included.
func sameLanguage(a, b string) bool {
	canonical := canonicalLanguage(a)
	return canonical != "" && canonical == canonicalLanguage(b)
}

// subtitlePreferences is what the command sidecar reads of the resolved
// preferences. The operator writes it into the pod as JSON only for the two
// modes that choose a track, on and auto.
type subtitlePreferences struct {
	Subtitles         string   `json:"subtitles"`
	SDH               string   `json:"sdh,omitempty"`
	AudioLanguages    []string `json:"audioLanguages,omitempty"`
	SubtitleLanguages []string `json:"subtitleLanguages,omitempty"`
}

// subtitleChoice is the track the rule chose, and the reason as a clause for
// the log line. A nil track is no subtitles. A choice that keeps mpv's own
// track sets nothing.
type subtitleChoice struct {
	track  *mpvTrack
	reason string
	keep   bool
}

// chooseSubtitle applies the rule to the classified tracks of one file.
// audioLanguage is the language of the audio track mpv selected.
//
// The reading languages are subtitleLanguages. A household that states none
// reads the languages it hears, so audioLanguages take their place. With
// neither list, the rule has no language to choose by, and mpv's own choice
// stands.
func chooseSubtitle(tracks []subtitleTrack, audioLanguage string, prefs subtitlePreferences) subtitleChoice {
	reading := prefs.SubtitleLanguages
	if len(reading) == 0 {
		reading = prefs.AudioLanguages
	}
	if len(reading) == 0 {
		return subtitleChoice{keep: true, reason: "neither subtitleLanguages nor audioLanguages names a language"}
	}
	forced := forcedTrackOf(tracks, audioLanguage)
	// In auto, audio in the household's first language needs only the
	// forced track that translates the rest of it.
	if prefs.Subtitles == subtitlesAuto && len(prefs.AudioLanguages) > 0 &&
		sameLanguage(audioLanguage, prefs.AudioLanguages[0]) {
		if forced != nil {
			return subtitleChoice{track: forced, reason: "it is the forced track of audio in the first audio language"}
		}
		return subtitleChoice{reason: "audio in the first audio language has no forced track"}
	}
	for _, wanted := range reading {
		if pick := bestTrack(tracks, wanted, readerRank(prefs.SDH), roleFull, roleSDH); pick != nil {
			return subtitleChoice{track: pick, reason: "it is the first full or SDH track in " + wanted}
		}
	}
	if forced != nil && slices.ContainsFunc(prefs.AudioLanguages, func(wanted string) bool {
		return sameLanguage(audioLanguage, wanted)
	}) {
		return subtitleChoice{track: forced, reason: "no full or SDH track is in a subtitle language, and it is the forced track of the audio"}
	}
	for _, wanted := range reading {
		if pick := bestTrack(tracks, wanted, readerRank(""), roleCommentary); pick != nil {
			return subtitleChoice{track: pick, reason: "no other track is in a subtitle language, and it is the commentary in " + wanted}
		}
	}
	return subtitleChoice{reason: "no track is in a subtitle language, and no forced track belongs to the audio"}
}

// forcedTrackOf is the forced track in the language of the audio, or nil.
func forcedTrackOf(tracks []subtitleTrack, audioLanguage string) *mpvTrack {
	return bestTrack(tracks, audioLanguage, readerRank(""), roleForced)
}

// readerRank orders the full and SDH roles by the sdh preference. The two
// rank the same with no preference.
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

// bestTrack is the first track in the language with one of the roles: by
// the rank of its role, then the default flag, then track order.
func bestTrack(tracks []subtitleTrack, wanted string, rank func(string) int, roles ...string) *mpvTrack {
	var candidates []subtitleTrack
	for _, one := range tracks {
		if slices.Contains(roles, one.role) && sameLanguage(one.language, wanted) {
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
		return cmp.Compare(defaultOrder(a.track), defaultOrder(b.track))
	})
	return &candidates[0].track
}

// defaultOrder puts a track with the default flag first.
func defaultOrder(track mpvTrack) int {
	if track.Default {
		return 0
	}
	return 1
}

// selectedAudioLanguage is the language of the audio track mpv selected,
// or empty when it selected none or the track names no language.
func selectedAudioLanguage(tracks []mpvTrack) string {
	for _, track := range tracks {
		if track.Type == trackTypeAudio && track.Selected {
			return track.Lang
		}
	}
	return ""
}
