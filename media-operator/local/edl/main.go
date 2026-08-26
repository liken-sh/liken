package main

// edl is a workstation testing utility. It builds and runs on its own, with
// `go run ./local/edl`, and no part of it ships in the media-operator binary.
// The local/music script runs it to build the two things a music run needs.

// An album plays as one EDL timeline: mpv reads it as one item with one
// duration and one chapter per track, so the display drives track selection
// with the chapter machinery a film already uses. The tool prints that
// timeline by default, and with -block it prints the presentation block that
// carries the album's words.

// The timeline this tool prints is the one the player shim writes in the
// pod, because both call the edl package, so what a person sees on a
// workstation is what a Play runs.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/liken-sh/media-operator/edl"
)

// The presentation block this tool prints. The field order here sets the
// JSON key order the block prints with.
//
// The block is meant to be pasted into a Play item's presentation, so it
// carries the two fields the operator and the shim read as a declaration,
// and the year is a number because the API's year is a number.
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
		fmt.Fprintln(os.Stderr, "usage: edl [-block] <album-dir>")
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

// albumBlock is the album's words as one presentation block. The type and the
// hint are the tool's own, because a folder of audio files is a music album
// whatever its tags say, and the rest is what the files state.
//
// The hint is what makes the pod expand the folder, so a block printed
// without it would play nothing.
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

// printBlock writes the block as one JSON object on stdout, the shape the Play
// carries under an item's presentation.
func printBlock(files []string) {
	encoded, err := json.Marshal(albumBlock(files))
	if err != nil {
		fmt.Fprintf(os.Stderr, "edl: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}
