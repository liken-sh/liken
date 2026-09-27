package main

// genrenames.go is the one genre vocabulary. Each provider spells a genre its
// own way: TMDb writes "Science Fiction", OMDb "Sci-Fi", and TVmaze
// "Science-Fiction", and TMDb's series genres join two genres in one name.
// The enricher unites the providers' answers, so without one vocabulary a
// title carries the same genre twice and the genres strip shows each spelling
// as a genre of its own. The enricher's merge and the walk's read of a .nfo
// file both speak it, so a file another writer filled reaches the catalog in
// the same words.

import (
	"slices"
	"strings"
)

// The names each spelling becomes, keyed by the spelling in lower case. A
// name the map does not hold is a genre of its own and stays as written.
var genreNames = map[string][]string{
	"sci-fi":             {"Science Fiction"},
	"science-fiction":    {"Science Fiction"},
	"science fiction":    {"Science Fiction"},
	"sci-fi & fantasy":   {"Science Fiction", "Fantasy"},
	"action & adventure": {"Action", "Adventure"},
	"war & politics":     {"War", "Politics"},
	"sport":              {"Sports"},
	"sports":             {"Sports"},
	"talk":               {"Talk Show"},
	"talk show":          {"Talk Show"},
}

// The genres in the vocabulary's words, in their first order. A repeat keeps
// the place of its first appearance, because the first genre is the title's
// main genre. Two spellings that differ only in case are one genre.
func canonicalGenres(genres []string) []string {
	var out []string
	for _, genre := range genres {
		genre = strings.TrimSpace(genre)
		if genre == "" {
			continue
		}
		names, known := genreNames[strings.ToLower(genre)]
		if !known {
			names = []string{genre}
		}
		for _, name := range names {
			if !slices.ContainsFunc(out, func(held string) bool { return strings.EqualFold(held, name) }) {
				out = append(out, name)
			}
		}
	}
	return out
}
