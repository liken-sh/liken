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
	Score     int    `yaml:"score"`
	Reason    string `yaml:"reason,omitempty"`
}

// One row of the trailers table: the ledger entry with the library and the
// item it belongs to.
type trailerRow struct {
	Library, Item, Provider, Key, Site, URL, Name, Kind, Language string
	Official                                                      bool
	Published                                                     string
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
		return trailerName{title: strings.TrimSpace(name), kind: trailerNameKind(name)}
	}
	last := years[len(years)-1]
	year, _ := strconv.Atoi(name[last[2]:last[3]])
	return trailerName{
		title: strings.TrimSpace(name[:last[0]]),
		year:  year,
		kind:  trailerNameKind(name[last[1]:]),
	}
}

// The kind the words after the year name. Song and spot are read before
// trailer, because a "Trailer Song" is not a trailer and a "TV Spot" is
// its own kind.
func trailerNameKind(rest string) string {
	rest = strings.ToLower(rest)
	switch {
	case strings.Contains(rest, "song"):
		return trailerKindOther
	case strings.Contains(rest, "spot"):
		return trailerKindSpot
	case strings.Contains(rest, "teaser"):
		return trailerKindTeaser
	case strings.Contains(rest, "clip"):
		return trailerKindClip
	case strings.Contains(rest, "trailer"):
		return trailerKindTrailer
	}
	return trailerKindOther
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

// What one trailer is worth to this title, 0 to 100, and the line that
// says why. A provider keyed by the title's own id is sure. A provider
// keyed by a search is sure only when the name carries the same title and
// year, and then the kind sets the score.
const (
	trailerScoreKeyed   = 100
	trailerScoreTrailer = 90
	trailerScoreTeaser  = 80
	trailerScoreSpot    = 60
	trailerScoreOther   = 40
)

func scoreTrailer(entry trailerEntry, parsed trailerName, title trailerTitle) (int, string) {
	if entry.Provider == providerBlockTMDb {
		return trailerScoreKeyed, "keyed by the tmdb id"
	}
	if foldTitle(parsed.title) != foldTitle(title.title) {
		return 0, "the name's title differs"
	}
	if parsed.year != title.year {
		return 0, "the name's year differs"
	}
	return trailerKindScore(parsed.kind), "title and year match; the name says " + parsed.kind
}

func trailerKindScore(kind string) int {
	switch kind {
	case trailerKindTrailer:
		return trailerScoreTrailer
	case trailerKindTeaser:
		return trailerScoreTeaser
	case trailerKindSpot:
		return trailerScoreSpot
	}
	return trailerScoreOther
}
