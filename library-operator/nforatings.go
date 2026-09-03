package main

// nforatings.go is the ratings block of a sidecar: the element as Kodi and
// Jellyfin write it, the names and scales of the four sites the rating facts
// serve, and the read that carries the scores into an item's body.

import (
	"strconv"
	"strings"
)

// One rating element inside the ratings block, in Kodi's form: the site that
// scored the title, the top of that site's scale, whether a reader takes this
// one first, the score, and how many people voted. The score is read as text
// and parsed per rating, because a number field fails the decode of the whole
// sidecar on one bad score, and a title would lose its plot and its cast to
// one site's empty value.
type nfoRating struct {
	Name    string  `xml:"name,attr"`
	Max     float64 `xml:"max,attr"`
	Default bool    `xml:"default,attr"`
	Value   string  `xml:"value"`
	Votes   int     `xml:"votes"`
}

// The score as a number, and false where the element holds none or holds
// text that is not one.
func (r nfoRating) score() (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(r.Value), 64)
	return value, err == nil
}

// The ratings element holds one rating per site, which is why one site's
// score is a fact of its own.
type nfoRatings struct {
	Ratings []nfoRating `xml:"rating"`
}

// themoviedb is the name Kodi and Jellyfin write on a TMDb rating, and 10 is
// the top of TMDb's own scale.
const (
	tmdbRatingName = "themoviedb"
	tmdbRatingMax  = 10
)

// The name and the scale of the three other sites. Kodi's own NFO page lists
// imdb, metacritic, and the tomatometer names for the ratings block. IMDb
// scores out of 10; the tomatometer and the Metascore score out of 100.
// Jellyfin reads a name that holds "tomato", without "audience" and without
// "avg", as the critic rating, which is why the Rotten Tomatoes name is
// tomatometerallcritics. Sources read on 2026-09-03: the Kodi wiki page NFO
// files/Movies, and Jellyfin's
// MediaBrowser.XbmcMetadata/Parsers/BaseNfoParser.cs.
const (
	imdbRatingName = "imdb"
	imdbRatingMax  = 10

	rottenTomatoesRatingName = "tomatometerallcritics"
	rottenTomatoesRatingMax  = 100

	metacriticRatingName = "metacritic"
	metacriticRatingMax  = 100
)

// One site's rating in the block, or nil where the block holds none.
func ratingNamed(ratings []nfoRating, name string) *nfoRating {
	for at := range ratings {
		if strings.EqualFold(strings.TrimSpace(ratings[at].Name), name) {
			return &ratings[at]
		}
	}
	return nil
}

// The block as the body holds it: one entry per site that scored,
// keyed by the sidecar's own rating name, valued on that site's own scale. A
// rating that states no score is left out, and a block with no score at all
// leaves the item with no ratings key.
func bodyRatings(ratings []nfoRating) map[string]float64 {
	held := map[string]float64{}
	for _, rating := range ratings {
		name := strings.ToLower(strings.TrimSpace(rating.Name))
		value, scored := rating.score()
		if name == "" || !scored || value <= 0 {
			continue
		}
		held[name] = value
	}
	if len(held) == 0 {
		return nil
	}
	return held
}
