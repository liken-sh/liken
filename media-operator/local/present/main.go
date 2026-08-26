package main

// present is a workstation testing utility. It builds and runs on its own,
// with `go run ./local/present`, and no part of it ships in the media-operator
// binary. The local/video script runs it to build the block it hands to
// serve-art.

// present reads the NFO that sits beside a media file and prints the
// presentation block the liken display expects. The block is one JSON object
// on stdout. A movie folder holds a sibling movie.nfo. An episode holds a
// sibling <basename>.nfo with a <season> and an <episode>.
//
// When a logo art file or a trickplay directory sits beside the media, the
// block carries its path, so the bridge decodes the art and crops the tiles.
//
// The art does not need an NFO. With no NFO the block starts empty, and the
// display falls back to mpv's own media-title for the name. The art fields are
// added when the files are present, so a film with trickplay but no NFO still
// shows the scrub thumbnail.

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The field order here sets the JSON key order the block prints with. A
// pointer field marks a number the NFO did not carry, so a real season zero
// survives and an absent value is omitted.
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
}

// An NFO is read as the direct children of its root, so the root tag itself
// does not matter. A movie NFO roots at <movie> and an episode NFO at
// <episodedetails>, and both read the same way.
type document struct {
	Elements []element `xml:",any"`
}

type element struct {
	XMLName xml.Name
	Text    string `xml:",chardata"`
}

// has reports whether the document holds a <tag>, whatever its text.
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

// text returns the stripped text of the first <tag>, and false when it is
// absent or empty.
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

// yearOf returns the release year, from <year>, or the leading year of
// <premiered>.
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

// findNFO returns the NFO for a media file: a sibling movie.nfo for a movie,
// or a sibling <basename>.nfo for an episode. The episode NFO wins when both
// exist, so a file named for its episode keeps its own metadata.
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

// findLogo returns the logo art beside the media, as an absolute path. A file
// named for the media wins over a folder-wide one, so an item with its own
// logo keeps it. The bridge opens this path and decodes it.
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

// findTrickplay returns the trickplay directory beside the media, as an
// absolute path. Jellyfin names it for the media file, so `X.mkv` has a
// sibling `X.trickplay` directory of sprite sheets. The bridge reads the
// sheets and crops one tile per scrub position.
func findTrickplay(media string) string {
	trickplay := filepath.Join(filepath.Dir(media), stemOf(media)+".trickplay")
	info, err := os.Stat(trickplay)
	if err != nil || !info.IsDir() {
		return ""
	}
	return absolute(trickplay)
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

// blockFor returns a series block when the NFO declares a <season>, and a
// movie block otherwise. A sibling movie.nfo is a movie regardless.
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

// presentationBlock builds the block for one media file: the NFO metadata when
// an NFO sits beside it, and the art fields when the art files are present.
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
	return made, nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: present <media-file>")
		os.Exit(2)
	}
	made, err := presentationBlock(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := json.Marshal(made)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}
