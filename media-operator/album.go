package main

// An album is one playlist item that plays a directory of audio files.
// The item's presentation block marks the directory as an album, and the
// `player` role replaces the directory with an mpv EDL timeline that
// lists the files. The playback pod does this because it is the only
// process that mounts the media.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/liken-sh/liken/media-operator/edl"
)

// The presentation values that mark an album. An item needs both,
// because type music alone is a standalone track.
const (
	presentationTypeMusic = "music"
	presentationHintAlbum = "album"
)

// albumTimelineDir is where the shim writes album timelines. It is the
// volume that mpv and the command sidecar already share, so mpv loads the
// timeline and the sidecar reads it back to find the album's cover. It is
// a variable so that a test can write to its own directory.
var albumTimelineDir = ipcMountPath

// expandItems returns the entries mpv loads for the resolved playlist.
// Each album directory becomes the path of the timeline written for it,
// and every other item stays as it is.
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

// writeAlbumTimeline writes the timeline for one album directory and
// returns its path. An album with no audio files is an error, so a Play
// that points at an empty or wrong directory fails instead of playing
// nothing.
func writeAlbumTimeline(dir string, index int) (string, error) {
	files, err := edl.AlbumFiles(dir)
	if err != nil {
		return "", fmt.Errorf("reading the album %q: %w", dir, err)
	}
	if len(files) == 0 {
		return "", fmt.Errorf("the album %q holds no audio files", dir)
	}
	path := filepath.Join(albumTimelineDir, fmt.Sprintf("album-%d.edl", index+1))
	if err := os.WriteFile(path, []byte(edl.Timeline(files)), 0o644); err != nil {
		return "", fmt.Errorf("writing the timeline of the album %q: %w", dir, err)
	}
	return path, nil
}

func isAlbum(presentation Presentation) bool {
	return presentation.Type == presentationTypeMusic && presentation.Hint == presentationHintAlbum
}

// allMusic reports whether every item of the run is music. player.go
// turns video off only for such a run, because --vid=no applies to
// every item.
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

// presentationAt returns the presentation block of one item. An item with
// no block, or with a block that does not parse, gets an empty block, so
// mpv keeps its defaults for it.
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
