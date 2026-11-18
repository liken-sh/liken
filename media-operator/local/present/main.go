package main

// present is a command for a workstation, and not part of the
// media-operator binary. The script local/video runs it with
// `go run ./local/present <media-file>` to build the presentation block
// that it passes to the display.
//
// present reads the NFO file beside a media file and prints the block
// as one JSON object on stdout. A movie folder has a movie.nfo beside
// the media file. An episode has a <basename>.nfo with a <season> and
// an <episode>.
//
// The block also holds the path of a logo file or a trickplay directory
// beside the media, when one exists. When
// .liken/appearances/<media file>.spans.json exists beside the media,
// the block holds its path and the path of the library's .contributors
// directory, so the display can show who is on screen when the film
// pauses. present finds the logo, the trickplay directory, and the
// appearances file without an NFO. Without one the block has no name,
// and the display uses mpv's media-title.

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// block is the JSON object that present prints, with its keys in the
// order of the fields. A number is a pointer, so that a zero such as
// season 0 is printed and a value that the NFO does not have is
// omitted.
type block struct {
	Type         string `json:"type,omitempty"`
	Hint         string `json:"hint,omitempty"`
	Title        string `json:"title,omitempty"`
	Year         *int   `json:"year,omitempty"`
	Series       string `json:"series,omitempty"`
	Season       *int   `json:"season,omitempty"`
	Episode      *int   `json:"episode,omitempty"`
	EpisodeTitle string `json:"episodeTitle,omitempty"`
	Date         string `json:"date,omitempty"`
	Logo         string `json:"logo,omitempty"`
	Trickplay    string `json:"trickplay,omitempty"`
	Appearances  string `json:"appearances,omitempty"`
	Contributors string `json:"contributors,omitempty"`
}

// document is the direct children of an NFO's root element. The root's
// own name is ignored, because a movie NFO has <movie> as its root and
// an episode NFO has <episodedetails>, and both are read the same way.
type document struct {
	Elements []element `xml:",any"`
}

type element struct {
	XMLName xml.Name
	Text    string `xml:",chardata"`
}

// has reports whether the document has a <tag> element, even an empty
// one.
func (d document) has(tag string) bool {
	_, found := d.find(tag)
	return found
}

func (d document) find(tag string) (element, bool) {
	for _, el := range d.Elements {
		if el.XMLName.Local == tag {
			return el, true
		}
	}
	return element{}, false
}

// text returns the trimmed text of the first <tag>, and false when
// there is none or it is empty.
func (d document) text(tag string) (string, bool) {
	el, found := d.find(tag)
	if !found {
		return "", false
	}
	value := strings.TrimSpace(el.Text)
	if value == "" {
		return "", false
	}
	return value, true
}

// yearOf returns the year from <year>, or else the first four
// characters of <premiered>. It returns nil when neither is present.
func (d document) yearOf() (*int, error) {
	raw, found := d.text("year")
	if !found {
		premiered, hasPremiered := d.text("premiered")
		if !hasPremiered {
			return nil, nil
		}
		raw = premiered
		if len(raw) > 4 {
			raw = raw[:4]
		}
	}
	return number(raw)
}

func number(raw string) (*int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// findNFO returns the NFO for a media file: <basename>.nfo beside it,
// or else movie.nfo in its folder. The first wins when both exist, so
// an episode keeps its own metadata.
func findNFO(media string) (string, bool) {
	episodeNFO := strings.TrimSuffix(media, filepath.Ext(media)) + ".nfo"
	movieNFO := filepath.Join(filepath.Dir(media), "movie.nfo")
	if exists(episodeNFO) {
		return episodeNFO, true
	}
	if exists(movieNFO) {
		return movieNFO, true
	}
	return "", false
}

// findLogo returns the absolute path of the logo file beside the media,
// or an empty string. A logo named for the media wins over a logo for
// the whole folder.
func findLogo(media string) string {
	parent := filepath.Dir(media)
	stem := stemOf(media)
	candidates := []string{
		filepath.Join(parent, stem+"-clearlogo.png"),
		filepath.Join(parent, stem+"-logo.png"),
		filepath.Join(parent, "clearlogo.png"),
		filepath.Join(parent, "logo.png"),
	}
	for _, candidate := range candidates {
		if exists(candidate) {
			return absolute(candidate)
		}
	}
	return ""
}

// findTrickplay returns the absolute path of the trickplay directory
// beside the media, or an empty string. Jellyfin names the directory
// for the media file, so `X.mkv` has `X.trickplay`, which holds sprite
// sheets. The display crops one tile from them for each scrub position.
func findTrickplay(media string) string {
	trickplay := filepath.Join(filepath.Dir(media), stemOf(media)+".trickplay")
	info, err := os.Stat(trickplay)
	if err != nil || !info.IsDir() {
		return ""
	}
	return absolute(trickplay)
}

// findAppearances returns the absolute path of the media's spans file
// in .liken/appearances/, and the path of the nearest .contributors
// directory at or above the media's folder. The spans file names each
// person's portrait by its path under that directory. It returns empty
// strings unless both exist, because the display needs the portrait to
// draw a person.
func findAppearances(media string) (string, string) {
	spans := filepath.Join(filepath.Dir(media), ".liken", "appearances", filepath.Base(media)+".spans.json")
	if !exists(spans) {
		return "", ""
	}
	for dir := filepath.Dir(absolute(media)); ; dir = filepath.Dir(dir) {
		contributors := filepath.Join(dir, ".contributors")
		if info, err := os.Stat(contributors); err == nil && info.IsDir() {
			return absolute(spans), contributors
		}
		if dir == filepath.Dir(dir) {
			return "", ""
		}
	}
}

func stemOf(media string) string {
	name := filepath.Base(media)
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func absolute(path string) string {
	full, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return full
}

func movieBlock(doc document) (block, error) {
	made := block{Type: "video", Hint: "movie"}
	title, found := doc.text("title")
	if found {
		made.Title = title
	}
	year, err := doc.yearOf()
	if err != nil {
		return block{}, err
	}
	made.Year = year
	return made, nil
}

func seriesBlock(doc document) (block, error) {
	made := block{Type: "video", Hint: "series"}
	series, found := doc.text("showtitle")
	if found {
		made.Series = series
	}
	season, found := doc.text("season")
	if found {
		value, err := number(season)
		if err != nil {
			return block{}, err
		}
		made.Season = value
	}
	episode, found := doc.text("episode")
	if found {
		value, err := number(episode)
		if err != nil {
			return block{}, err
		}
		made.Episode = value
	}
	episodeTitle, found := doc.text("title")
	if found {
		made.EpisodeTitle = episodeTitle
	}
	date, found := doc.text("aired")
	if found {
		made.Date = date
	}
	return made, nil
}

// blockFor returns a series block when the NFO has a <season>, and a
// movie block otherwise. A file named movie.nfo always gives a movie
// block.
func blockFor(nfo string) (block, error) {
	raw, err := os.ReadFile(nfo)
	if err != nil {
		return block{}, err
	}
	var doc document
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return block{}, err
	}
	if filepath.Base(nfo) != "movie.nfo" && doc.has("season") {
		return seriesBlock(doc)
	}
	return movieBlock(doc)
}

func presentationBlock(media string) (block, error) {
	made := block{}
	nfo, found := findNFO(media)
	if found {
		fromNFO, err := blockFor(nfo)
		if err != nil {
			return block{}, err
		}
		made = fromNFO
	}
	made.Logo = findLogo(media)
	made.Trickplay = findTrickplay(media)
	made.Appearances, made.Contributors = findAppearances(media)
	return made, nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "present: got %d arguments, want 1\nusage: present <media-file>\n", len(os.Args)-1)
		os.Exit(2)
	}
	made, err := presentationBlock(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "present: %v\n", err)
		os.Exit(1)
	}
	encoded, err := json.Marshal(made)
	if err != nil {
		fmt.Fprintf(os.Stderr, "present: encoding the presentation block: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}
