package main

// episodeidentity.go numbers the episodes one file holds, from the episode
// .nfo beside it and the season folder and the marker in its name.

import (
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// episodeIdentity reads the numbers and the body of every episode one file
// holds. A range marker in the name numbers each of them, within one season.
//
// The .nfo file beside the video is the source where one exists. The season
// folder and the file name fill a number the .nfo file left at zero, so an
// .nfo file that names only the episode still takes its season from the
// folder. When the .nfo file's first episode is not in the name's range, the
// .nfo file's number replaces the range's first number, the way it replaces
// the number of a file of one episode.
//
// An .nfo file of several episodedetails blocks gives each episode its own
// title and body. Each block describes the episode its own number names, and
// the tools that write such a file do not all write the blocks in episode
// order, so a block goes to the episode of its number and not to the episode
// at its place in the file. A block with no number goes to the episode at its
// place. An episode with no block takes the file's technical attributes and no
// title of its own, so scanEpisode names it S04E11 and no episode ever carries
// another episode's title.
func episodeIdentity(episode episodeFile) ([]episodeMeta, error) {
	var blocks []episodeMeta
	nfoPath := filepath.Join(episode.dir, stripExtension(episode.file)+".nfo")
	data, err := os.ReadFile(nfoPath)
	switch {
	case err == nil:
		if parsed, err := parseEpisodeNFOs(data); err == nil {
			blocks = parsed
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	// A block with no number keeps its place, so the sort runs only when
	// every block carries a number.
	if !slices.ContainsFunc(blocks, func(block episodeMeta) bool { return block.Episode == 0 }) {
		slices.SortStableFunc(blocks, func(a, b episodeMeta) int { return cmp.Compare(a.Episode, b.Episode) })
	}

	var first episodeMeta
	if len(blocks) > 0 {
		first = blocks[0]
	}
	season, numbers, marked := parseEpisodeMarker(episode.file)
	if first.Season == 0 {
		if folderSeason, ok := parseSeasonFolder(filepath.Base(episode.dir)); ok {
			first.Season = folderSeason
		} else if marked {
			first.Season = season
		}
	}
	switch {
	case !marked:
		numbers = []int{first.Episode}
	case first.Episode != 0 && !slices.Contains(numbers, first.Episode):
		numbers = append([]int{first.Episode}, numbers[1:]...)
	}

	metas := make([]episodeMeta, 0, len(numbers))
	for index, number := range numbers {
		meta := episodeBlock(blocks, index, number, first)
		meta.Season = first.Season
		meta.Episode = number
		metas = append(metas, meta)
	}
	return metas, nil
}

// episodeBlock picks the .nfo block that describes the episode of a number:
// the block that names that number, or else the block with no number at the
// episode's place in the file. An episode with neither takes only the file's
// stream facts, which the first block holds.
func episodeBlock(blocks []episodeMeta, index, number int, first episodeMeta) episodeMeta {
	if number != 0 {
		if at := slices.IndexFunc(blocks, func(block episodeMeta) bool { return block.Episode == number }); at >= 0 {
			return blocks[at]
		}
	}
	if index < len(blocks) && blocks[index].Episode == 0 {
		return blocks[index]
	}
	return episodeMeta{Stream: first.Stream, Duration: itemDuration(first.Stream, 0)}
}
