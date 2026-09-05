package main

// franchisefile.go reads one franchise.yaml and enforces the schema the
// public repository publishes, the same file as
// docs/static/franchise.schema.json. The file is the whole contract
// between its author and the scanner, because no provider holds a story
// order. A file that breaks one rule is refused whole; the scanner reports
// it and reads the other files. The rules are written by hand rather than
// read from the schema, because the operator's image carries one static
// binary and no schema library.

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// franchiseFile is one franchise.yaml, in the file's own names. name and
// order are required, and every other part is optional.
type franchiseFile struct {
	Name     string             `yaml:"name"`
	Sources  []string           `yaml:"sources"`
	Calendar *franchiseCalendar `yaml:"calendar"`
	Universe string             `yaml:"universe"`
	Eras     []franchiseEra     `yaml:"eras"`
	Order    []franchiseEntry   `yaml:"order"`
	// Art is the franchise's own art, as links and not as bytes. The keys are
	// Kodi's names, and every one of them is optional. The repository holds no
	// image, because a franchise is an opinion about a story and the art
	// belongs to whoever published it.
	Art map[string]string `yaml:"art"`
}

// franchiseArtKinds are the art keys the file may name, in the order the fetch
// reads them. Each one names the art fact of plan 30 that writes the same kind
// of image, so the file the fetch writes carries the name that fact writes.
// fanart is Kodi's name for the backdrop, and landscape for the thumb.
var franchiseArtKinds = []struct {
	Name string
	Fact string
}{
	{"poster", factPoster},
	{"fanart", factBackdrop},
	{"landscape", factLandscape},
	{"logo", factLogo},
	{"banner", factBanner},
}

// artLinks are the links the file names, in the key order above, each with the
// file name the fetch writes it under. A file that names no art block names no
// link.
func (f *franchiseFile) artLinks() []franchiseArtLink {
	links := []franchiseArtLink{}
	for _, kind := range franchiseArtKinds {
		if url := f.Art[kind.Name]; url != "" {
			links = append(links, franchiseArtLink{Base: franchiseArtBase(kind.Fact), URL: url})
		}
	}
	return links
}

// franchiseArtLink is one link of the art block: the URL, and the name the
// file takes on the claim without its extension. The extension comes from what
// the link answers, so the base is what the ledger and the fetch key on.
type franchiseArtLink struct {
	Base string
	URL  string
}

// franchiseArtBase is the file name one art fact writes, without its
// extension. The names are plan 30's own, so a franchise's poster sits beside
// a film's poster under the same name.
func franchiseArtBase(fact string) string {
	file := artTypes[fact].file
	return strings.TrimSuffix(file, filepath.Ext(file))
}

// validateArt requires every art key to be one of the five and every value to
// be an https link. The links leave the cluster, so http is refused rather
// than followed. The keys are read in name order, so a file with two faults
// reports the same one every time.
func (f *franchiseFile) validateArt() error {
	known := map[string]bool{}
	for _, kind := range franchiseArtKinds {
		known[kind.Name] = true
	}
	names := make([]string, 0, len(f.Art))
	for name := range f.Art {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !known[name] {
			return fmt.Errorf("art names %q, which is not poster, fanart, landscape, logo, or banner", name)
		}
		if !strings.HasPrefix(f.Art[name], artLinkScheme) {
			return fmt.Errorf("the art %s %q does not start with %s", name, f.Art[name], artLinkScheme)
		}
	}
	return nil
}

// artLinkScheme is the one scheme an art link may carry.
const artLinkScheme = "https://"

// franchiseCalendar is the franchise's own clock, which every time in the
// file counts in. unit is required, and it is years or days. zero, before,
// and after name the event the times count from, and a calendar without
// them counts in plain years.
type franchiseCalendar struct {
	Unit   string `yaml:"unit" json:"unit"`
	Zero   string `yaml:"zero" json:"zero,omitempty"`
	Before string `yaml:"before" json:"before,omitempty"`
	After  string `yaml:"after" json:"after,omitempty"`
}

// franchiseEra is one named stretch of the timeline, which the page draws
// as a bar on the rail beside the wall. The name and both ends are
// required, and spans may overlap, because a saga holds phases.
type franchiseEra struct {
	Name string   `yaml:"name" json:"name"`
	From *float64 `yaml:"from" json:"from"`
	To   *float64 `yaml:"to" json:"to"`
}

// franchiseTime is the span of one entry in the calendar's unit. Both ends
// are required, and they are equal for a story that stays in one year.
type franchiseTime struct {
	From *float64 `yaml:"from"`
	To   *float64 `yaml:"to"`
}

// franchiseEntry is one entry of the story order: one film or one series.
// An entry holds exactly one of movie and series, each a provider id.
// seasons cut a series into the runs the story plays. universes names
// every universe whose story the entry continues or joins, and an entry
// with none is in the franchise's own universe.
type franchiseEntry struct {
	Movie  string `yaml:"movie"`
	Series string `yaml:"series"`
	Title  string `yaml:"title"`
	// Released is the real-world date, as much of it as the author knows:
	// 1999, 1999-05, or 1999-05-19. On a film it is the first public
	// release, and on a series the day the first episode aired. It is
	// never the story's own calendar, which Time carries. It is optional,
	// and the scanner never reads a date out of the title.
	Released  string            `yaml:"released"`
	Time      *franchiseTime    `yaml:"time"`
	Universes []string          `yaml:"universes"`
	Note      string            `yaml:"note"`
	Seasons   []franchiseSeason `yaml:"seasons"`
}

// franchiseSeason is one season of a series entry, or one run of one
// season. The season number is required, and specials are season 0.
// episodes holds codes and ranges in the order they play, and a season with
// none plays whole.
type franchiseSeason struct {
	Season   *int           `yaml:"season"`
	Episodes []string       `yaml:"episodes"`
	Time     *franchiseTime `yaml:"time"`
	Note     string         `yaml:"note"`
}

// franchiseEpisode is one episode of one season, as the runs table holds
// it.
type franchiseEpisode struct {
	Season  int
	Episode int
}

// episodeCode admits the two forms an episodes entry takes: one code, or a
// range of two. The schema names the same pattern, so a file that
// validates in an editor validates here.
var episodeCode = regexp.MustCompile(`^S([0-9]{2,})E([0-9]{2,})(?:-S([0-9]{2,})E([0-9]{2,}))?$`)

// releasedDate admits the three precisions a released date takes, the
// same pattern the schema states: a year, a year and a month, or a whole
// day. validReleased checks the two-digit parts beside the pattern,
// because 1999-13-32 matches the shape and names no date.
var releasedDate = regexp.MustCompile(`^([0-9]{4})(?:-([0-9]{2})(?:-([0-9]{2}))?)?$`)

// providerReference is the shape of a provider id, scheme:id, such as
// tmdb:1893.
var providerReference = regexp.MustCompile(`^[a-z0-9]+:[A-Za-z0-9_-]+$`)

// parseFranchiseFile reads one franchise.yaml and returns the file or the
// rule it breaks. The decoder refuses a field the schema does not name,
// because the schema closes every object.
func parseFranchiseFile(data []byte) (*franchiseFile, error) {
	file := &franchiseFile{}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(file); err != nil {
		return nil, err
	}
	if err := file.validate(); err != nil {
		return nil, err
	}
	return file, nil
}

// validate applies the rules the schema states, in the order a reader
// meets them in the file.
func (f *franchiseFile) validate() error {
	if f.Name == "" {
		return fmt.Errorf("the file names no name")
	}
	if len(f.Order) == 0 {
		return fmt.Errorf("the file names no order")
	}
	if len(f.Eras) > 0 && f.Calendar == nil {
		return fmt.Errorf("the file names eras and no calendar")
	}
	if err := f.Calendar.validate(); err != nil {
		return err
	}
	if err := f.validateArt(); err != nil {
		return err
	}
	for _, era := range f.Eras {
		if err := era.validate(); err != nil {
			return err
		}
	}
	for position, entry := range f.Order {
		if err := entry.validate(); err != nil {
			return fmt.Errorf("entry %d: %w", position+1, err)
		}
	}
	return nil
}

// validate requires a unit of years or days. A file with no calendar holds
// an order alone, and that is legal.
func (c *franchiseCalendar) validate() error {
	if c == nil {
		return nil
	}
	if c.Unit != "years" && c.Unit != "days" {
		return fmt.Errorf("the calendar unit %q is neither years nor days", c.Unit)
	}
	return nil
}

func (e franchiseEra) validate() error {
	if e.Name == "" {
		return fmt.Errorf("an era names no name")
	}
	return spanOf(e.From, e.To, "the era "+e.Name)
}

// spanOf requires both ends of a span, because a bar with one end draws
// nothing.
func spanOf(from, to *float64, what string) error {
	if from == nil {
		return fmt.Errorf("%s names no from", what)
	}
	if to == nil {
		return fmt.Errorf("%s names no to", what)
	}
	return nil
}

// validate requires an entry to be one film or one series, never both and
// never neither. A movie carries no seasons, because seasons cut a series.
func (e franchiseEntry) validate() error {
	if e.Movie == "" && e.Series == "" {
		return fmt.Errorf("names neither a movie nor a series")
	}
	if e.Movie != "" && e.Series != "" {
		return fmt.Errorf("names both a movie and a series")
	}
	if e.Movie != "" && len(e.Seasons) > 0 {
		return fmt.Errorf("names a movie and seasons")
	}
	if !providerReference.MatchString(e.reference()) {
		return fmt.Errorf("the provider id %q is not scheme:id", e.reference())
	}
	for _, universe := range e.Universes {
		if universe == "" {
			return fmt.Errorf("universes names an empty universe")
		}
	}
	if err := validReleased(e.Released); err != nil {
		return err
	}
	if e.Time != nil {
		if err := spanOf(e.Time.From, e.Time.To, "the time"); err != nil {
			return err
		}
	}
	for _, season := range e.Seasons {
		if err := season.validate(); err != nil {
			return err
		}
	}
	return nil
}

// validReleased accepts a year, a year and a month, or a whole day, and
// refuses a month outside 01 to 12 or a day outside 01 to 31, so a date
// that matches the shape and names no day never reaches a row. It leaves
// the calendar's own month lengths alone: 1999-02-31 is admitted, because
// nothing reads the day back as a date.
func validReleased(released string) error {
	if released == "" {
		return nil
	}
	parts := releasedDate.FindStringSubmatch(released)
	if parts == nil {
		return fmt.Errorf("the released date %q is not a year, a year and a month, or a whole day", released)
	}
	if parts[2] != "" && (number(parts[2]) < 1 || number(parts[2]) > 12) {
		return fmt.Errorf("the released date %q names no month", released)
	}
	if parts[3] != "" && (number(parts[3]) < 1 || number(parts[3]) > 31) {
		return fmt.Errorf("the released date %q names no day", released)
	}
	return nil
}

// releaseYear is the year the wall labels a row with, the first four
// characters of the released date. The file validated before a row read
// it, so those four characters are digits. An entry with no date leaves
// the year at 0.
func (e franchiseEntry) releaseYear() int {
	if len(e.Released) < 4 {
		return 0
	}
	return number(e.Released[:4])
}

// reference is the provider id the entry names, whichever kind it is.
func (e franchiseEntry) reference() string {
	if e.Movie != "" {
		return e.Movie
	}
	return e.Series
}

// kind is the kind column of the member row this entry writes.
func (e franchiseEntry) kind() string {
	if e.Movie != "" {
		return scopeMovie
	}
	return scopeSeries
}

// validate requires a season number, and requires every episode code to
// expand.
func (s franchiseSeason) validate() error {
	if s.Season == nil {
		return fmt.Errorf("a season names no season number")
	}
	if *s.Season < 0 {
		return fmt.Errorf("the season number %d is below zero", *s.Season)
	}
	if s.Time != nil {
		if err := spanOf(s.Time.From, s.Time.To, "the time"); err != nil {
			return err
		}
	}
	for _, code := range s.Episodes {
		if _, err := expandEpisodeCode(code); err != nil {
			return err
		}
	}
	return nil
}

// expandEpisodeCode turns one code into one episode, and a range into
// every episode between its two codes. The file expands here and not
// against the catalog, so a held-episode count on the page is one join. A
// range stays inside one season and never runs backwards.
func expandEpisodeCode(code string) ([]franchiseEpisode, error) {
	parts := episodeCode.FindStringSubmatch(code)
	if parts == nil {
		return nil, fmt.Errorf("the episode code %q is not SnnEnn or SnnEnn-SnnEnn", code)
	}
	season, episode := number(parts[1]), number(parts[2])
	if parts[3] == "" {
		return []franchiseEpisode{{Season: season, Episode: episode}}, nil
	}
	lastSeason, lastEpisode := number(parts[3]), number(parts[4])
	if lastSeason != season {
		return nil, fmt.Errorf("the range %q crosses two seasons", code)
	}
	if lastEpisode < episode {
		return nil, fmt.Errorf("the range %q ends before it starts", code)
	}
	held := make([]franchiseEpisode, 0, lastEpisode-episode+1)
	for number := episode; number <= lastEpisode; number++ {
		held = append(held, franchiseEpisode{Season: season, Episode: number})
	}
	return held, nil
}

// number reads the digits of one code part. The pattern admits digits
// alone, so the read cannot fail.
func number(digits string) int {
	value, _ := strconv.Atoi(strings.TrimLeft(digits, "0"))
	return value
}
