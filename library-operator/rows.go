package main

// rows.go holds the catalog's three row kinds as Go values, and the pure
// functions that derive an item's identity. The scanner fills a movieRow,
// a seriesRow, or an episodeRow, a fileRow for each physical file, and the
// aliasRow values that roll an item's several names onto one id, then hands
// them to the catalog write client. Nothing here reaches the network. An
// item's id is a function of what the volume already holds, so a re-walk
// derives the same id and the scanner mints none.

import (
	"fmt"
	"strconv"
	"strings"
)

// The id scopes, the word that leads every canonical id. The scope is the
// singular of the kind, so a movies library holds movie:... items. The
// scope namespaces the id, so a movie and a series that carry the same
// provider's numeric id do not collide.
const (
	scopeMovie   = "movie"
	scopeSeries  = "series"
	scopeEpisode = "episode"
)

// The provider preference per scope. itemID takes the first provider present
// in this order, so one title resolves to one canonical id whichever databases
// its sidecar names. The order leads with the database the project trusts most.
var providerOrder = map[string][]string{
	scopeMovie:  {"tmdb", "imdb"},
	scopeSeries: {"tvdb", "tmdb", "imdb"},
}

// The sources an alias row records: a provider id read from the sidecar, or
// the folder name the scanner fell back to. The source says how the name was
// learned, so a later pass tells a durable provider id from a guessed folder.
const (
	aliasSourceProvider = "provider"
	aliasSourceFolder   = "folder"
)

// castMember is one credited person and the part they played.
type castMember struct {
	Name string `json:"name,omitempty"`
	Role string `json:"role,omitempty"`
}

// movieBody is what movie.nfo holds beyond the shared header, stored in the
// item's body column as JSON.
type movieBody struct {
	Plot          string            `json:"plot,omitempty"`
	Tagline       string            `json:"tagline,omitempty"`
	Cast          []castMember      `json:"cast,omitempty"`
	Directors     []string          `json:"directors,omitempty"`
	Writers       []string          `json:"writers,omitempty"`
	Studios       []string          `json:"studios,omitempty"`
	Genres        []string          `json:"genres,omitempty"`
	Collection    string            `json:"collection,omitempty"`
	ProviderIDs   map[string]string `json:"providerIds,omitempty"`
	Country       string            `json:"country,omitempty"`
	ContentRating string            `json:"contentRating,omitempty"`
}

// seriesBody is what tvshow.nfo holds beyond the shared header. A series
// credits its creators where a movie credits its directors and writers.
type seriesBody struct {
	Plot          string            `json:"plot,omitempty"`
	Tagline       string            `json:"tagline,omitempty"`
	Cast          []castMember      `json:"cast,omitempty"`
	Creators      []string          `json:"creators,omitempty"`
	Studios       []string          `json:"studios,omitempty"`
	Genres        []string          `json:"genres,omitempty"`
	ProviderIDs   map[string]string `json:"providerIds,omitempty"`
	Country       string            `json:"country,omitempty"`
	ContentRating string            `json:"contentRating,omitempty"`
}

// episodeBody is what an episode .nfo holds beyond the shared header.
type episodeBody struct {
	Plot        string            `json:"plot,omitempty"`
	Directors   []string          `json:"directors,omitempty"`
	Writers     []string          `json:"writers,omitempty"`
	Cast        []castMember      `json:"cast,omitempty"`
	ProviderIDs map[string]string `json:"providerIds,omitempty"`
}

// movieRow is one row of the movies item table: the header columns every
// kind sorts on, the movie body, and the id of the set the movie belongs
// to, empty where the sidecar names no set.
type movieRow struct {
	Id       string
	Library  string
	Kind     string
	Path     string
	Title    string
	SortKey  string
	Slug     string
	Released string
	Added    int64
	Art      string
	Arts     []string
	Duration int64
	Body     movieBody
	SetID    string
	// The nfo_facts column: the nfo facts the title's sidecar already answers,
	// each name wrapped in commas. The gap query of each nfo fact reads it.
	NFOFacts string
}

// seriesRow is one row of the series item table, the same header as a
// movieRow with the series body.
type seriesRow struct {
	Id       string
	Library  string
	Kind     string
	Path     string
	Title    string
	SortKey  string
	Slug     string
	Released string
	Added    int64
	Art      string
	Arts     []string
	Duration int64
	Body     seriesBody
	// The nfo_facts column, the same list a movie carries.
	NFOFacts string
}

// episodeRow is one row of the episodes item table: the shared header, the
// episode body, and the three columns that place the episode under its series.
type episodeRow struct {
	Id       string
	Library  string
	Kind     string
	Path     string
	Title    string
	SortKey  string
	Slug     string
	Released string
	Added    int64
	Art      string
	Arts     []string
	Duration int64
	Body     episodeBody
	Series   string
	Season   int
	Episode  int
}

// fileRow is one physical file and the item ids it belongs to. Items drives
// the many-to-many file_items link: one file names more than one item where
// a file holds two episodes, and one item holds more than one file where a
// title has a second encoding.
//
// Type, Role, and Language are the classification in files.go, read off the
// file's name and the directory that holds it. Modified is the time the one
// stat that read the size also read.
type fileRow struct {
	Path       string
	Library    string
	Container  string
	VideoCodec string
	AudioCodec string
	Width      int
	Height     int
	SizeBytes  int64
	DurationMs int64
	Trickplay  string
	Present    bool
	Type       string
	Role       string
	Language   string
	Modified   int64
	Items      []string
}

// fileItemKey names one row of the link table: the file's path, and the id of
// the item it belongs to.
type fileItemKey struct {
	Path string
	Item string
}

// aliasRow maps one of an item's names to the item, with the source of the
// name.
//
// The alias table keys on the library and the alias together, so two
// libraries that read the same name each keep their own row, and neither
// overwrites the other.
type aliasRow struct {
	Alias   string
	Library string
	Item    string
	Source  string
}

// itemID derives the provider-scoped canonical id. It takes the first provider
// present in the scope's fixed order and mints none, so a re-walk of an
// unchanged sidecar derives the same id, movie:tmdb:603. A folder with no
// provider id falls back to movie:path:<key>, which a move of that folder
// breaks. That is the weak case the design accepts, in place of a minted id
// the derived catalog has nowhere to keep.
func itemID(kind string, providerIDs map[string]string, folderKey string) string {
	for _, provider := range providerOrder[kind] {
		if value := providerIDs[provider]; value != "" {
			return kind + ":" + provider + ":" + value
		}
	}
	return kind + ":path:" + folderKey
}

// episodeID reuses the series' provider tail under the episode scope, with a
// zero-padded season and episode, so series:tvdb:81189 yields
// episode:tvdb:81189:s02e05. A path-fallback series id carries its tail
// through unchanged, so a sidecar-less series still gives its episodes a stable
// id for as long as its folder holds.
func episodeID(seriesID string, season, episode int) string {
	_, tail, _ := strings.Cut(seriesID, ":")
	return fmt.Sprintf("%s:%s:s%02de%02d", scopeEpisode, tail, season, episode)
}

// aliasesFor rolls every name an item carries onto its canonical id: one row
// per provider id, one for the folder key, and the canonical id itself. The
// canonical is its own alias, so alias resolution is one lookup and needs no
// special case for the id a caller already holds.
func aliasesFor(library, kind string, providerIDs map[string]string, folderKey, canonicalID string) []aliasRow {
	var rows []aliasRow
	seen := map[string]bool{}
	add := func(alias, source string) {
		if alias == "" || seen[alias] {
			return
		}
		seen[alias] = true
		rows = append(rows, aliasRow{Alias: alias, Library: library, Item: canonicalID, Source: source})
	}
	for _, provider := range providerOrder[kind] {
		if value := providerIDs[provider]; value != "" {
			add(kind+":"+provider+":"+value, aliasSourceProvider)
		}
	}
	if folderKey != "" {
		add(kind+":path:"+folderKey, aliasSourceFolder)
	}
	if _, held := seen[canonicalID]; !held {
		add(canonicalID, aliasSourceProvider)
	}
	return rows
}

// sortKey strips a leading article so a list sorts on the first word that
// carries meaning, and "The Matrix" files under M. This is the opposite of the
// slug, which keeps the article.
func sortKey(title string) string {
	for _, article := range []string{"The ", "A ", "An "} {
		if len(title) >= len(article) && strings.EqualFold(title[:len(article)], article) {
			return title[len(article):]
		}
	}
	return title
}

// slug is the legible display name for a URL and a screen, such as
// the-matrix-1999. It lowercases the title, folds accents to ASCII, keeps
// the article, hyphenates the rest, and appends the year where there is one.
// The slug is a display name and not the item's id, so a corrected title
// changes it freely. What does key on a name is folderKey, the slug of a
// folder's own name, which a title with no provider id rests its id on.
func slug(title string, year int) string {
	var b strings.Builder
	// A run of separators becomes at most one hyphen, and only between two kept
	// tokens, so the slug carries no leading or trailing hyphen.
	pendingHyphen := false
	write := func(s string) {
		if pendingHyphen && b.Len() > 0 {
			b.WriteByte('-')
		}
		pendingHyphen = false
		b.WriteString(s)
	}
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			write(string(r))
		default:
			if folded, ok := accentFold[r]; ok {
				write(folded)
				continue
			}
			pendingHyphen = true
		}
	}
	out := b.String()
	if year > 0 {
		if out == "" {
			return strconv.Itoa(year)
		}
		return out + "-" + strconv.Itoa(year)
	}
	return out
}

// accentFold maps the common accented Latin letters to their ASCII base, so
// slug folds an accent with no Unicode-table dependency in the image.
var accentFold = func() map[rune]string {
	folds := map[string]string{
		"a": "àáâãäåāăą",
		"c": "çćĉċč",
		"d": "ďđ",
		"e": "èéêëēĕėęě",
		"g": "ĝğġģ",
		"i": "ìíîïĩīĭįı",
		"l": "ĺļľŀł",
		"n": "ñńņňŉ",
		"o": "òóôõöøōŏő",
		"r": "ŕŗř",
		"s": "śŝşš",
		"t": "ţťŧ",
		"u": "ùúûüũūŭůűų",
		"y": "ýÿŷ",
		"z": "źżž",
	}
	m := map[rune]string{'ß': "ss", 'æ': "ae", 'œ': "oe"}
	for base, accented := range folds {
		for _, r := range accented {
			m[r] = base
		}
	}
	return m
}()
