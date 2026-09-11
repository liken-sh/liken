package main

import (
	"context"
	"regexp"
	"slices"
	"strings"
)

// One episode file's clue: the season and episode its name is marked with, and
// the title the name carries after the marker. A namer that writes
// "<series> - S01E02 - <title>.mkv" gives the ladder one clue per file.
type episodeClue struct {
	season  int
	episode int
	title   string
}

// How many clues one folder hands the rung. One season answers the question,
// and a dozen episode names are more than two shows with one title share.
const maxEpisodeClues = 12

// How many clues a candidate must answer before the rung keeps it. One match
// is a coincidence, such as an episode named Pilot.
const minEpisodeMatches = 2

// A quality tag or a release group a namer writes after the title, in brackets
// or parentheses, which is no part of the episode's name.
var episodeTitleTag = regexp.MustCompile(`\s*(\[[^]]*]|\([^)]*\))$`)

// The clue one file name gives: the marker's numbers, and the text after the
// marker up to the next " - ", with every trailing tag cut off. A name with no
// marker, or nothing after it, gives no clue. A double episode takes the first
// number, because the title after the marker names that one.
func episodeClueFrom(name string) (episodeClue, bool) {
	name = stripExtension(name)
	season, episodes, marked := parseEpisodeMarker(name)
	marker := episodeMarker.FindStringIndex(name)
	if !marked || len(episodes) == 0 || marker == nil {
		return episodeClue{}, false
	}
	title, _, _ := strings.Cut(strings.TrimLeft(name[marker[1]:], " ._-"), " - ")
	for {
		trimmed := episodeTitleTag.ReplaceAllString(title, "")
		if trimmed == title {
			break
		}
		title = trimmed
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return episodeClue{}, false
	}
	return episodeClue{season: season, episode: episodes[0], title: title}, true
}

// The clues of one series folder. A folder the enricher cannot read gives no
// clue and no error, because the ladder has other rungs and the read is only
// evidence. A movie folder gives none, because the rung is a series test.
func (e *enricher) episodeClues(folder string) []episodeClue {
	if e.kind != libraryKindSeries {
		return nil
	}
	files, _ := collectEpisodeFiles(folder, e.ignore)
	var clues []episodeClue
	for _, file := range files {
		if clue, held := episodeClueFrom(file.file); held {
			clues = append(clues, clue)
		}
	}
	return firstSeasonClues(clues)
}

// The clues of the first season the folder holds, in episode order, capped at
// maxEpisodeClues. Season zero is the specials, whose names are the weakest
// evidence a show has, so it is the first season only where it is the only one.
func firstSeasonClues(clues []episodeClue) []episodeClue {
	if len(clues) == 0 {
		return nil
	}
	lowest := clues[0].season
	for _, clue := range clues {
		if lowest == 0 || (clue.season > 0 && clue.season < lowest) {
			lowest = clue.season
		}
	}
	var first []episodeClue
	for _, clue := range clues {
		if clue.season == lowest {
			first = append(first, clue)
		}
	}
	slices.SortFunc(first, func(a, b episodeClue) int { return a.episode - b.episode })
	if len(first) > maxEpisodeClues {
		first = first[:maxEpisodeClues]
	}
	return first
}

// The episode rung. It keeps each candidate whose season on TMDb carries the
// folder's episode titles, at least two of them and at least half. The rung
// runs only for a series with clues; anything else keeps the list as it was.
func fromEpisodes(ctx context.Context, client *tmdbClient, search identitySearch,
	matched []identityMatch) ([]identityMatch, error) {
	if search.kind != libraryKindSeries || len(search.episodes) == 0 {
		return nil, nil
	}
	var kept []identityMatch
	for _, match := range matched {
		matches, err := countEpisodeMatches(ctx, client, match.result.ID, search.episodes)
		if err != nil {
			return nil, err
		}
		if matches >= minEpisodeMatches && matches*2 >= len(search.episodes) {
			kept = append(kept, match)
		}
	}
	return kept, nil
}

// How many clues one candidate answers with an episode of the same number and
// the same normalized name. Each season is fetched once per candidate.
func countEpisodeMatches(ctx context.Context, client *tmdbClient, id int,
	clues []episodeClue) (int, error) {
	seasons := map[int]map[int]string{}
	matches := 0
	for _, clue := range clues {
		names, read := seasons[clue.season]
		if !read {
			episodes, err := client.season(ctx, id, clue.season)
			if err != nil {
				return 0, err
			}
			names = map[int]string{}
			for _, episode := range episodes {
				names[episode.Number] = normalizeTitle(episode.Name)
			}
			seasons[clue.season] = names
		}
		wanted := normalizeTitle(clue.title)
		if wanted != "" && names[clue.episode] == wanted {
			matches++
		}
	}
	return matches, nil
}
