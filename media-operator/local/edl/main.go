package main

// edl prints the mpv EDL timeline of an album directory, or with -block
// the presentation block of a Play item that plays it. local/music runs
// it on a workstation. mpv reads the timeline as one item with one
// chapter per track, so track selection uses the same chapter keys as a
// film. The player shim in the playback pod builds its timeline with the
// same edl package, so the timeline printed here is the one a Play runs.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/liken-sh/liken/media-operator/edl"
)

// block is the presentation block of a Play item. The field order sets
// the JSON key order.
type block struct {
	Type   string `json:"type"`
	Hint   string `json:"hint"`
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`
	Year   int    `json:"year,omitempty"`
}

func main() {
	asBlock := flag.Bool("block", false, "print the presentation block instead of the timeline")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "edl: want one album directory, got %d arguments\nusage: edl [-block] <album-dir>\n", flag.NArg())
		os.Exit(2)
	}

	files, err := edl.AlbumFiles(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "edl: %v\n", err)
		os.Exit(1)
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "edl: no audio files in %q\n", flag.Arg(0))
		os.Exit(1)
	}

	if *asBlock {
		printBlock(files)
		return
	}
	fmt.Print(edl.Timeline(files))
}

// albumBlock sets Type and Hint whatever the tags say, because the
// player shim expands a directory into a timeline only when the item has
// type music and hint album. Without them the Play plays nothing.
func albumBlock(files []string) block {
	facts := edl.AlbumFacts(files)
	return block{
		Type:   "music",
		Hint:   "album",
		Artist: facts.Artist,
		Album:  facts.Album,
		Year:   facts.Year,
	}
}

func printBlock(files []string) {
	encoded, err := json.Marshal(albumBlock(files))
	if err != nil {
		fmt.Fprintf(os.Stderr, "edl: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}
