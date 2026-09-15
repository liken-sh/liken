package main

// archive.go is what the trailer fact asks the Internet Archive: a search of
// its movie_trailers collection by title, and the trailer entry each item
// becomes.

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// The archive is one service at one fixed address.
const archiveAPIBase = "https://archive.org"

// The path the archive answers a search on, and the collection this block
// reads.
const (
	archiveSearchPath  = "/advancedsearch.php"
	archiveDetailsPath = "/details/"
	archiveCollection  = "movie_trailers"
)

// How many items one search reads, and the fields it asks for.
const (
	archiveSearchRows   = 20
	archiveSearchFields = "identifier,title,year,date,licenseurl,mediatype"
)

// The client holds the address. The shared request layer holds the pace the
// archive asks for.
type archiveClient struct {
	providerRequests
}

func newArchiveClient(base string) *archiveClient {
	return &archiveClient{newProviderRequests(providerBlockArchive, base, nil)}
}

// One item of the collection, with the fields the search asked for.
type archiveDoc struct {
	Identifier string
	Title      string
	Year       string
	Date       string
	LicenseURL string
	MediaType  string
}

// The same item as the search answers it. A field can arrive in more than one
// shape, so each is read raw and turned into a word after.
type archiveDocument struct {
	Identifier string          `json:"identifier"`
	Title      json.RawMessage `json:"title"`
	Year       json.RawMessage `json:"year"`
	Date       json.RawMessage `json:"date"`
	LicenseURL json.RawMessage `json:"licenseurl"`
	MediaType  json.RawMessage `json:"mediatype"`
}

func (d *archiveDoc) UnmarshalJSON(body []byte) error {
	held := archiveDocument{}
	if err := json.Unmarshal(body, &held); err != nil {
		return err
	}
	*d = archiveDoc{
		Identifier: held.Identifier,
		Title:      archiveText(held.Title),
		Year:       archiveText(held.Year),
		Date:       archiveText(held.Date),
		LicenseURL: archiveText(held.LicenseURL),
		MediaType:  archiveText(held.MediaType),
	}
	return nil
}

// One field as a word, whichever shape it arrived in: a string, a number, or
// a list whose first entry is one of those.
func archiveText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var held any
	// The whole answer decoded before this field did, so a field that cannot be
	// read here is a shape with no word, not broken JSON.
	_ = json.Unmarshal(raw, &held)
	switch value := held.(type) {
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case []any:
		if len(value) > 0 {
			if first, ok := value[0].(string); ok {
				return first
			}
		}
	}
	return ""
}

type archiveSearchAnswer struct {
	Response archiveSearchResponse `json:"response"`
}

type archiveSearchResponse struct {
	NumFound int          `json:"numFound"`
	Docs     []archiveDoc `json:"docs"`
}

// The search reads the collection alone, and the title travels as one quoted
// phrase, so the words of it match together.
func archiveSearchQuery(title string) string {
	return "collection:" + archiveCollection +
		` AND title:("` + strings.ReplaceAll(title, `"`, `\"`) + `")`
}

func (c *archiveClient) search(ctx context.Context, title string) ([]archiveDoc, error) {
	values := url.Values{
		"q":      {archiveSearchQuery(title)},
		"fl[]":   {archiveSearchFields},
		"rows":   {strconv.Itoa(archiveSearchRows)},
		"output": {"json"},
	}
	var answer archiveSearchAnswer
	if err := c.get(ctx, archiveSearchPath, values, &answer); err != nil {
		return nil, err
	}
	return answer.Response.Docs, nil
}

// The page one item plays on, at the archive's own address.
func archiveDetailsURL(identifier string) string {
	return archiveAPIBase + archiveDetailsPath + identifier
}

// The site of an entry the archive named.
const trailerSiteArchive = "archive"

// The kind the item's own name states, or trailer where the name states none,
// because every item of the movie_trailers collection is one.
func archiveDocKind(name string) string {
	kind, stated := trailerNameKind(name)
	if !stated {
		return trailerKindTrailer
	}
	return kind
}

// The two forms an item's own title states a year in: (1940) or |1940|.
var archiveTitleYear = regexp.MustCompile(`[(|](\d{4})[)|]`)

// The words an uploader writes beside the title. They say what the file is,
// not which title it belongs to, so the match drops them.
var (
	archiveTitleBracket = regexp.MustCompile(`\[[^\]]*\]`)
	archiveTitleWords   = regexp.MustCompile(
		`(?i)\b(trailer|theatrical|movie|restoration|official|hd|720p|1080p|4k|webrip)\b`)
)

// The quoted segment of a name. An uploader writes the title in quotes where
// the words around it name the director or the studio.
var archiveTitleQuoted = regexp.MustCompile(`"([^"]+)"`)

// The year the item states, out of the year field or out of its title, or
// zero where it states none.
func archiveDocYear(doc archiveDoc) int {
	if year, err := strconv.Atoi(strings.TrimSpace(doc.Year)); err == nil && year > 0 {
		return year
	}
	if found := archiveTitleYear.FindStringSubmatch(doc.Title); found != nil {
		year, _ := strconv.Atoi(found[1])
		return year
	}
	return 0
}

// The title alone, out of the name the uploader wrote: the quoted segment
// where the name holds one, and the name before the year with the file words
// dropped otherwise.
func archiveDocTitle(name string) string {
	if quoted := archiveTitleQuoted.FindStringSubmatch(name); quoted != nil {
		return quoted[1]
	}
	if cut := strings.Index(name, " | "); cut >= 0 {
		name = name[:cut]
	}
	if found := archiveTitleYear.FindStringIndex(name); found != nil {
		name = name[:found[0]]
	}
	name = archiveTitleBracket.ReplaceAllString(name, " ")
	return archiveTitleWords.ReplaceAllString(name, " ")
}

// What the item proves about the title it belongs to: whether its own title
// and year are this title's.
func archiveDocMatch(doc archiveDoc, title trailerTitle) trailerMatch {
	year := archiveDocYear(doc)
	return trailerMatch{
		title:     foldTitle(archiveDocTitle(doc.Title)) == foldTitle(title.title),
		year:      year == title.year,
		yearKnown: year != 0,
	}
}

// The date the item states, or the first day of the year where it states a
// year alone.
func archiveDocPublished(doc archiveDoc) string {
	if date := trailerDate(doc.Date); date != "" {
		return date
	}
	if year, err := strconv.Atoi(strings.TrimSpace(doc.Year)); err == nil && year > 0 {
		return strconv.Itoa(year) + "-01-01"
	}
	return ""
}

// The archive's trailer answerer keys on the title's own name, because the
// collection holds no provider ids.
type archiveTrailerAnswerer struct {
	client *archiveClient
}

func newArchiveTrailerAnswerer(client *archiveClient) archiveTrailerAnswerer {
	return archiveTrailerAnswerer{client: client}
}

func (a archiveTrailerAnswerer) providerBlock() string { return providerBlockArchive }

// Every item whose own title carries this title. A search for `Dune` answers
// every Dune item the collection holds, so an item that scores 0 is dropped
// and never recorded.
func (a archiveTrailerAnswerer) trailers(ctx context.Context, title trailerTitle) ([]trailerEntry, error) {
	if title.title == "" {
		return nil, nil
	}
	docs, err := a.client.search(ctx, title.title)
	if err != nil {
		return nil, err
	}
	entries := []trailerEntry{}
	for _, doc := range docs {
		entry := trailerEntry{
			Path:      likenSelfPath,
			Provider:  providerBlockArchive,
			Key:       doc.Identifier,
			Site:      trailerSiteArchive,
			URL:       archiveDetailsURL(doc.Identifier),
			Name:      doc.Title,
			Kind:      archiveDocKind(doc.Title),
			Published: archiveDocPublished(doc),
		}
		if !recordedTrailerKind(entry.Kind) {
			continue
		}
		entry.Score, entry.Reason = scoreTrailer(entry, archiveDocMatch(doc, title), title.languages)
		if entry.Score <= 0 {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
