package main

// archive.go is what the trailer fact asks the Internet Archive: a search of
// its movie_trailers collection by title, and the trailer entry each item
// becomes.
// The metadata of an item states how long its video runs, which is what tells
// a trailer from a whole film.

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The archive is one service at one fixed address.
const archiveAPIBase = "https://archive.org"

// The path the archive answers a search on, and the collection this block
// reads.
// The metadata of one item answers on its own path.
const (
	archiveSearchPath   = "/advancedsearch.php"
	archiveDetailsPath  = "/details/"
	archiveMetadataPath = "/metadata/"
	archiveCollection   = "movie_trailers"
)

// The longest video this answerer records. The movie_trailers collection
// holds whole films beside the trailers, and a trailer runs a few minutes.
const archiveTrailerLongest = 8 * time.Minute

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

// The kind the item's own name states, or trailer where the name states none.
// The kind the item's own name states, or trailer where the name states none.
// The second answer says whether the name stated a kind: only an item that
// stated none needs its metadata read to tell a trailer from a whole film.
func archiveDocKind(name string) (string, bool) {
	kind, stated := trailerNameKind(name)
	if !stated {
		return trailerKindTrailer, false
	}
	return kind, true
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

// One item's metadata: the file list the length and the resolution are read
// from.
type archiveItem struct {
	Files []archiveFile `json:"files"`
}

// One file of an item, in the fields that state what it is, how long it runs,
// and how tall it is.
type archiveFile struct {
	Name   string
	Format string
	Length string
	Height string
}

// The same file as the metadata answers it, each field raw, because a number
// and a word both arrive here.
type archiveFileFields struct {
	Name   json.RawMessage `json:"name"`
	Format json.RawMessage `json:"format"`
	Length json.RawMessage `json:"length"`
	Height json.RawMessage `json:"height"`
}

func (f *archiveFile) UnmarshalJSON(body []byte) error {
	held := archiveFileFields{}
	if err := json.Unmarshal(body, &held); err != nil {
		return err
	}
	*f = archiveFile{
		Name:   archiveText(held.Name),
		Format: archiveText(held.Format),
		Length: archiveText(held.Length),
		Height: archiveText(held.Height),
	}
	return nil
}

func (c *archiveClient) item(ctx context.Context, identifier string) (archiveItem, error) {
	var answer archiveItem
	if err := c.get(ctx, archiveMetadataPath+identifier, nil, &answer); err != nil {
		return archiveItem{}, err
	}
	return answer, nil
}

// The formats the archive names its video files by.
var archiveVideoFormats = map[string]bool{
	"MPEG4": true, "h.264": true, "h.264 IA": true, "Matroska": true,
	"QuickTime": true, "Ogg Video": true, "WebM": true, "MPEG2": true,
	"Windows Media": true, "Flash Video": true, "DivX": true,
	"512Kb MPEG4": true, "Cinepack": true,
}

// The names of video files, for a format this operator has no word for.
var archiveVideoNames = []string{
	".mp4", ".mkv", ".mov", ".avi", ".webm", ".ogv", ".m4v", ".wmv", ".flv",
	".mpg", ".mpeg",
}

func archiveFileIsVideo(file archiveFile) bool {
	if archiveVideoFormats[file.Format] {
		return true
	}
	name := strings.ToLower(file.Name)
	for _, ending := range archiveVideoNames {
		if strings.HasSuffix(name, ending) {
			return true
		}
	}
	return false
}

// The video files of one item.
func archiveVideoFiles(item archiveItem) []archiveFile {
	videos := []archiveFile{}
	for _, file := range item.Files {
		if archiveFileIsVideo(file) {
			videos = append(videos, file)
		}
	}
	return videos
}

// The length one file states, as seconds, as h:mm:ss, or as mm:ss.
func archiveFileLength(stated string) (time.Duration, bool) {
	seconds := 0.0
	parts := strings.Split(strings.TrimSpace(stated), ":")
	if len(parts) > 3 {
		return 0, false
	}
	for _, part := range parts {
		value, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || value < 0 {
			return 0, false
		}
		seconds = seconds*60 + value
	}
	return time.Duration(seconds * float64(time.Second)), true
}

// The item runs as long as its shortest video file, which is the trailer
// where an item holds a trailer and a whole film both.
func archiveVideoLength(videos []archiveFile) time.Duration {
	shortest := time.Duration(0)
	for _, file := range videos {
		length, stated := archiveFileLength(file.Length)
		if !stated {
			continue
		}
		if shortest == 0 || length < shortest {
			shortest = length
		}
	}
	return shortest
}

// The tallest video file's height, which is the resolution the entry records.
func archiveVideoHeight(videos []archiveFile) int {
	tallest := 0
	for _, file := range videos {
		height, err := strconv.Atoi(strings.TrimSpace(file.Height))
		if err == nil && height > tallest {
			tallest = height
		}
	}
	return tallest
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

// One item the search matched, with the entry it becomes.
// One item the search kept. An item whose own name stated no kind is
// ambiguous, and its metadata is what tells a trailer from a whole film.
type archiveCandidate struct {
	entry     trailerEntry
	ambiguous bool
}

// Every item whose own title carries this title. A search for `Dune` answers
// every Dune item the collection holds, so an item that scores 0 is dropped
// and never recorded.
// Every item whose own title carries this title. A search for `Dune` answers
// every Dune item the collection holds, so an item that scores 0 is dropped
// and never recorded. The metadata of each ambiguous item is read, and an
// item with no video or with one longer than eight minutes is dropped as a
// whole film.
func (a archiveTrailerAnswerer) trailers(ctx context.Context, title trailerTitle) ([]trailerEntry, error) {
	if title.title == "" {
		return nil, nil
	}
	docs, err := a.client.search(ctx, title.title)
	if err != nil {
		return nil, err
	}
	kept := archiveCandidates(docs, title)
	items, failures := a.metadata(ctx, kept)
	return archiveEntries(kept, items, failures)
}

// The items the title and the score kept, in the order the search answered
// them.
func archiveCandidates(docs []archiveDoc, title trailerTitle) []archiveCandidate {
	kept := []archiveCandidate{}
	for _, doc := range docs {
		kind, stated := archiveDocKind(doc.Title)
		entry := trailerEntry{
			Path:      likenSelfPath,
			Provider:  providerBlockArchive,
			Key:       doc.Identifier,
			Site:      trailerSiteArchive,
			URL:       archiveDetailsURL(doc.Identifier),
			Name:      doc.Title,
			Kind:      kind,
			Published: archiveDocPublished(doc),
		}
		if !recordedTrailerKind(entry.Kind) {
			continue
		}
		entry.Score, entry.Reason = scoreTrailer(entry, archiveDocMatch(doc, title), title.languages)
		if entry.Score <= 0 {
			continue
		}
		kept = append(kept, archiveCandidate{entry: entry, ambiguous: !stated})
	}
	return kept
}

// The metadata of every ambiguous item at once. The client's own pace holds
// the requests to the archive's interval, and the latency is what a title
// waits on. Each goroutine writes the slot of its own item, so the answers
// need no lock.
func (a archiveTrailerAnswerer) metadata(ctx context.Context, kept []archiveCandidate) ([]archiveItem, []error) {
	items := make([]archiveItem, len(kept))
	failures := make([]error, len(kept))
	var reading sync.WaitGroup
	for at, one := range kept {
		if !one.ambiguous {
			continue
		}
		reading.Add(1)
		go func() {
			defer reading.Done()
			items[at], failures[at] = a.client.item(ctx, one.entry.Key)
		}()
	}
	reading.Wait()
	return items, failures
}

// The entries in the search's own order, so the answer does not depend on
// which metadata read finished first. One item the archive refused is one
// item lost. Every item refused is an answer the caller cannot stand on.
func archiveEntries(kept []archiveCandidate, items []archiveItem, failures []error) ([]trailerEntry, error) {
	entries := []trailerEntry{}
	asked, refused := 0, 0
	var firstRefusal error
	for at, one := range kept {
		if !one.ambiguous {
			entries = append(entries, one.entry)
			continue
		}
		asked++
		if failures[at] != nil {
			refused++
			if firstRefusal == nil {
				firstRefusal = failures[at]
			}
			continue
		}
		videos := archiveVideoFiles(items[at])
		if len(videos) == 0 || archiveVideoLength(videos) > archiveTrailerLongest {
			continue
		}
		one.entry.Resolution = archiveVideoHeight(videos)
		entries = append(entries, one.entry)
	}
	if asked > 0 && refused == asked {
		return nil, firstRefusal
	}
	return entries, nil
}
