package main

// An album is one item. The presentation block declares an item's shape,
// the way a video item's hint does: a directory item marked as an album
// becomes one written timeline, and a file item passes through as itself.
// The expansion runs in the shim, because the playback pod is the one
// process that mounts the media, so it is the one process that can read an
// album's files.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/liken-sh/media-operator/edl"
)

// The two block values that mark an album. An item needs both, because the
// music type alone is a standalone track, and the hint names the shape the
// way movie and series name a video's.
const (
	presentationTypeMusic = "music"
	presentationHintAlbum = "album"
)

// Where the shim writes an album's timeline. It is the volume the playback
// pod already shares between mpv and the command sidecar, so mpv loads the
// timeline and the sidecar reads it back for the album's cover. It is a
// variable rather than a constant so a test writes under its own directory.
var albumTimelineDir = ipcMountPath

// expandItems turns the resolved playlist into the entries mpv loads. An
// album directory becomes the one timeline written for it, and every other
// item is the path the resolver already settled.
func expandItems(items []string, blocks []json.RawMessage) ([]string, error) {
	entries := make([]string, len(items))
	for index, item := range items {
		entries[index] = item
		if !isAlbum(presentationAt(blocks, index)) {
			continue
		}
		timeline, err := writeAlbumTimeline(item, index)
		if err != nil {
			return nil, err
		}
		entries[index] = timeline
	}
	return entries, nil
}

// writeAlbumTimeline reads one album folder and writes its timeline. An
// album with no audio files fails the run, because a spec that marks a
// folder as an album and points at an empty one states a wrong fact, and
// silence would hide it.
func writeAlbumTimeline(dir string, index int) (string, error) {
	files, err := edl.AlbumFiles(dir)
	if err != nil {
		return "", fmt.Errorf("read the album %q: %w", dir, err)
	}
	if len(files) == 0 {
		return "", fmt.Errorf("the album %q holds no audio files", dir)
	}
	path := filepath.Join(albumTimelineDir, fmt.Sprintf("album-%d.edl", index+1))
	if err := os.WriteFile(path, []byte(edl.Timeline(files)), 0o644); err != nil {
		return "", fmt.Errorf("write the timeline for %q: %w", dir, err)
	}
	return path, nil
}

// isAlbum reads one block as the mark of an album: the music type and the
// album hint together.
func isAlbum(presentation Presentation) bool {
	return presentation.Type == presentationTypeMusic && presentation.Hint == presentationHintAlbum
}

// allMusic says whether every item of the run is music. The answer decides
// --vid=no, and the flag is global to the run, so one film in the list must
// keep video on for the whole of it.
func allMusic(blocks []json.RawMessage, items int) bool {
	if items == 0 {
		return false
	}
	for index := range items {
		if presentationAt(blocks, index).Type != presentationTypeMusic {
			return false
		}
	}
	return true
}

// presentationAt reads one item's block. An item the pod baked no block for
// reads as an empty block, so a Play that declares nothing keeps the
// player's own defaults.
func presentationAt(blocks []json.RawMessage, index int) Presentation {
	var presentation Presentation
	if index < 0 || index >= len(blocks) {
		return presentation
	}
	if err := json.Unmarshal(blocks[index], &presentation); err != nil {
		return Presentation{}
	}
	return presentation
}
