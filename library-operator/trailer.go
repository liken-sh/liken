package main

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// trailer.go is the vocabulary of the trailer fact: the kinds of video a
// provider names, the sites a video plays from, the two shapes a trailer
// takes (the ledger entry and the catalog row), and the rule that scores
// how sure the fact is that a video belongs to a title.

// What a video is, in one word. A screen sorts on it, and the score reads
// it: a trailer is worth more than a teaser, and a teaser more than a TV
// spot.
const (
	trailerKindTrailer = "trailer"
	trailerKindTeaser  = "teaser"
	trailerKindSpot    = "spot"
	trailerKindClip    = "clip"
	trailerKindOther   = "other"
)

// Where a video plays from. The site says what the key means, and a later
// step reads the two together to find a stream.
const (
	trailerSiteYouTube  = "youtube"
	trailerSiteVimeo    = "vimeo"
	trailerSitePeerTube = "peertube"
)

// One trailer a provider named for one title, as the ledger records it.
// Path is the ledger's own entry path, which is the title itself. Score is
// 0 to 100, and Reason says in one line why.
type trailerEntry struct {
	Path      string `yaml:"path"`
	Provider  string `yaml:"provider"`
	Key       string `yaml:"key"`
	Site      string `yaml:"site"`
	URL       string `yaml:"url"`
	Name      string `yaml:"name"`
	Kind      string `yaml:"kind"`
	Language  string `yaml:"language,omitempty"`
	Official  bool   `yaml:"official,omitempty"`
	Published string `yaml:"published,omitempty"` // YYYY-MM-DD
	// The height in lines the provider states, or 0 where it states none.
	Resolution int    `yaml:"resolution,omitempty"`
	Score      int    `yaml:"score"`
	Reason     string `yaml:"reason,omitempty"`
}

// One row of the trailers table: the ledger entry with the library and the
// item it belongs to.
type trailerRow struct {
	Library, Item, Provider, Key, Site, URL, Name, Kind, Language string
	Official                                                      bool
	Published                                                     string
	Resolution                                                    int
	Score                                                         int
	Reason                                                        string
}

// The title one trailer ask names. A provider keyed by id reads the kind
// and the ids. A provider keyed by search reads the title and the year.
type trailerTitle struct {
	kind  string
	ids   providerIDs
	title string
	year  int
	// The household and library languages, most preferred first, which the score
	// reads.
	languages []string
}

// One provider block, asked for the trailers of one title. A provider that
// holds none answers an empty list and no error. Every answerer is asked,
// because this fact takes the union of what the providers hold.
type trailerAnswerer interface {
	providerBlock() string
	trailers(ctx context.Context, title trailerTitle) ([]trailerEntry, error)
}

// What a provider's own name for a video states: the title it belongs to,
// the year that title came out, and the kind of video it is. A trailer
// channel writes names like `DUNE: PART THREE (2026) - IMAX Trailer [4K]`.
type trailerName struct {
	title string
	year  int
	kind  string
}

// The year a name states, in parentheses, which is how trailer channels
// write it.
var trailerYearPattern = regexp.MustCompile(`\((\d{4})\)`)

// The title is everything before the last year in the name, and the words
// after the year say the kind. A name with no year is all title, and its
// kind is read off the whole name.
func parseTrailerName(name string) trailerName {
	years := trailerYearPattern.FindAllStringSubmatchIndex(name, -1)
	if len(years) == 0 {
		kind, _ := trailerNameKind(name)
		return trailerName{title: strings.TrimSpace(name), kind: kind}
	}
	last := years[len(years)-1]
	year, _ := strconv.Atoi(name[last[2]:last[3]])
	kind, _ := trailerNameKind(name[last[1]:])
	return trailerName{
		title: strings.TrimSpace(name[:last[0]]),
		year:  year,
		kind:  kind,
	}
}

// The kind the words after the year name. Song and spot are read before
// trailer, because a "Trailer Song" is not a trailer and a "TV Spot" is
// its own kind.
// The second answer is whether a word named a kind at all. A provider whose
// whole collection is trailers reads it and takes trailer where no word did.
func trailerNameKind(rest string) (string, bool) {
	rest = strings.ToLower(rest)
	switch {
	case strings.Contains(rest, "song"):
		return trailerKindOther, true
	case strings.Contains(rest, "spot"):
		return trailerKindSpot, true
	case strings.Contains(rest, "teaser"):
		return trailerKindTeaser, true
	case strings.Contains(rest, "clip"):
		return trailerKindClip, true
	case strings.Contains(rest, "trailer"):
		return trailerKindTrailer, true
	}
	return trailerKindOther, false
}

// Whether a provider that holds videos of every kind records one of this
// kind: a trailer, a teaser, and a TV spot are recorded, and a clip and a
// video of another kind are not.
func recordedTrailerKind(kind string) bool {
	return kind != trailerKindClip && kind != trailerKindOther
}

// One title as a comparison reads it: lower case, & as and, and letters
// and digits alone. So `Don't Move` and `DON'T MOVE` are one word, and
// `DUNE: PART THREE` is `Dune - Part Three`.
func foldTitle(s string) string {
	folded := strings.Builder{}
	for _, letter := range strings.ToLower(strings.ReplaceAll(s, "&", "and")) {
		if unicode.IsLetter(letter) || unicode.IsDigit(letter) {
			folded.WriteRune(letter)
		}
	}
	return folded.String()
}

// The date a provider's timestamp states, as YYYY-MM-DD, or nothing where
// the timestamp is shorter than a date.
const trailerDateLength = len("2026-03-18")

func trailerDate(published string) string {
	if len(published) < trailerDateLength {
		return ""
	}
	return published[:trailerDateLength]
}

// What one provider's answer proves about the title it names.
type trailerMatch struct {
	keyed     bool // the provider keyed on the title's own id
	title     bool // a search provider's name carries the same title
	year      bool // ... and the same year
	yearKnown bool // the provider stated a year at all
}

// Where the ladder starts, by what the provider's answer proves.
const (
	trailerScoreKeyed      = 100
	trailerScoreTitleYear  = 90
	trailerScoreTitleAlone = 60
)

// What each part of a video costs, and the score a matched video never falls
// below.
const (
	trailerTeaserPenalty = -10
	trailerSpotPenalty   = -30
	trailerClipPenalty   = -40
	trailerOtherPenalty  = -50

	trailerNoLanguagePenalty    = -5
	trailerOtherLanguagePenalty = -30

	trailerUnofficialPenalty = -10

	trailerLowestScore = 1
)

// The parts of one reason, in the order the ladder applied them.
const trailerReasonSeparator = "; "

// What one trailer is worth to this title, 0 to 100, and the line that says
// why. The score ranks the videos of one title, so a screen can take the
// first.
func scoreTrailer(entry trailerEntry, match trailerMatch, languages []string) (int, string) {
	score, start := trailerStart(match)
	if score == 0 {
		return 0, ""
	}
	reason := []string{start}

	kind, word := trailerKindPenalty(entry.Kind)
	score += kind
	reason = append(reason, word)

	language, note := trailerLanguagePenalty(entry.Language, languages)
	score += language
	reason = append(reason, note)

	// TMDb alone states whether the title's own studio published the video.
	if entry.Provider == providerBlockTMDb && !entry.Official {
		score += trailerUnofficialPenalty
		reason = append(reason, "unofficial")
	}
	return max(score, trailerLowestScore), strings.Join(reason, trailerReasonSeparator)
}

// A provider keyed by the title's own id is sure. A search provider is sure
// only where the name carries the title, and surer where it carries the year.
func trailerStart(match trailerMatch) (int, string) {
	switch {
	case match.keyed:
		return trailerScoreKeyed, "keyed by the tmdb id"
	case match.title && match.year:
		return trailerScoreTitleYear, "title and year match"
	case match.title && !match.yearKnown:
		return trailerScoreTitleAlone, "title matches, no year known"
	}
	return 0, ""
}

// What the kind of video costs, and the word the reason names it by.
func trailerKindPenalty(kind string) (int, string) {
	switch kind {
	case trailerKindTrailer:
		return 0, trailerKindTrailer
	case trailerKindTeaser:
		return trailerTeaserPenalty, trailerKindTeaser
	case trailerKindSpot:
		return trailerSpotPenalty, trailerKindSpot
	case trailerKindClip:
		return trailerClipPenalty, trailerKindClip
	}
	return trailerOtherPenalty, trailerKindOther
}

// A video in a language the household asked for costs nothing. A video that
// states no language costs little. A video in another language costs the
// most.
func trailerLanguagePenalty(language string, languages []string) (int, string) {
	if language == "" {
		return trailerNoLanguagePenalty, "no language"
	}
	for _, preferred := range languages {
		if sameLanguage(preferred, language) {
			return 0, language
		}
	}
	return trailerOtherLanguagePenalty, "language " + language + " not preferred"
}

// Two tags name one language where their primary subtags are the same, so a
// preference for en-US reads a video marked en, and a preference for en reads
// a video marked en-US.
func sameLanguage(preferred, language string) bool {
	return primarySubtag(preferred) == primarySubtag(language)
}

// The primary subtag of a language tag, folded to lower case: everything
// before the first hyphen.
func primarySubtag(tag string) string {
	primary, _, _ := strings.Cut(strings.ToLower(tag), "-")
	return primary
}
