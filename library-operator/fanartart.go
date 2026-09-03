package main

// The art answerer Fanart.tv serves through. Its two lookup keys are the
// whole reason the identity fact writes every id it can: a movie reads on its
// TMDb id, and a series reads on its TheTVDB id, which the sidecar carries.

import (
	"context"
	"slices"
	"strconv"
	"strings"
)

// Fanart.tv's art answerer, which is one project key and nothing else.
type fanartArtAnswerer struct {
	client *fanartClient
}

func (a fanartArtAnswerer) providerBlock() string { return providerBlockFanart }

func (a fanartArtAnswerer) serves(fact string) bool {
	return slices.Contains(providerFacts[providerBlockFanart], fact)
}

func (a fanartArtAnswerer) fetchFile(ctx context.Context, address string) ([]byte, error) {
	return a.client.fetchFile(ctx, address)
}

// One call answers every art type of one title, and the fact reads the list
// of the type it writes. The kind of the library picks the endpoint, because
// Fanart.tv keeps a movie and a series apart.
func (a fanartArtAnswerer) candidates(ctx context.Context, fact string, gap artGap,
	title titleRef) ([]artCandidate, error) {
	if title.kind == libraryKindSeries {
		return a.seriesCandidates(ctx, fact, gap, title)
	}
	return a.movieCandidates(ctx, fact, gap, title)
}

// A movie reads on the TMDb id the gap carries, and on the IMDb id where the
// sidecar holds one and the gap holds no TMDb id, because Fanart.tv reads
// both as one lookup key.
func (a fanartArtAnswerer) movieCandidates(ctx context.Context, fact string, gap artGap,
	title titleRef) ([]artCandidate, error) {
	id := fanartMovieKey(gap, title)
	if id == "" {
		return nil, nil
	}
	movie, err := a.client.movie(ctx, id)
	if err != nil || movie == nil {
		return nil, err
	}
	return fanartCandidates(fanartMovieImages(movie, fact)), nil
}

func fanartMovieKey(gap artGap, title titleRef) string {
	for _, id := range []string{gap.tmdb, title.ids["tmdb"], title.ids["imdb"]} {
		if id = strings.TrimSpace(id); id != "" {
			return id
		}
	}
	return ""
}

// A series reads on the TheTVDB id alone, which is what the identity fact
// wrote into the sidecar. A series with none is no answer and not an error,
// because a title this provider cannot be asked about is the ordinary case.
func (a fanartArtAnswerer) seriesCandidates(ctx context.Context, fact string, gap artGap,
	title titleRef) ([]artCandidate, error) {
	id := strings.TrimSpace(title.ids["tvdb"])
	if id == "" {
		return nil, nil
	}
	series, err := a.client.series(ctx, id)
	if err != nil || series == nil {
		return nil, err
	}
	return fanartCandidates(fanartSeriesImages(series, fact, gap.season)), nil
}

// Which of the movie lists each art fact reads. The logo and the clearart
// name two lists because Fanart.tv keeps the high-definition art in a list of
// its own, and this project takes it where the provider holds any.
func fanartMovieImages(movie *fanartMovie, fact string) []fanartImage {
	switch fact {
	case factPoster:
		return movie.Posters
	case factBackdrop:
		return movie.Backgrounds
	case factLogo:
		return firstHeldImages(movie.HDLogos, movie.Logos)
	case factClearart:
		return firstHeldImages(movie.HDClearart, movie.Clearart)
	case factBanner:
		return movie.Banners
	case factLandscape:
		return movie.Thumbs
	case factDiscart:
		return movie.Discs
	}
	return nil
}

// Which of the series lists each art fact reads. The season art is narrowed
// to the season the gap names. The disc art has no series list, because a
// series carries no disc.
func fanartSeriesImages(series *fanartSeries, fact string, season int) []fanartImage {
	switch fact {
	case factPoster:
		return series.Posters
	case factBackdrop:
		return series.Backgrounds
	case factLogo:
		return firstHeldImages(series.HDLogos, series.Clearlogos)
	case factClearart:
		return firstHeldImages(series.HDClearart, series.Clearart)
	case factBanner:
		return series.Banners
	case factLandscape:
		return series.Thumbs
	case factSeasonPoster:
		return seasonImages(series.SeasonPosters, strconv.Itoa(season))
	case factSeasonBanner:
		return seasonImages(series.SeasonBanners, strconv.Itoa(season))
	}
	return nil
}

// The first list the provider holds any image in.
func firstHeldImages(lists ...[]fanartImage) []fanartImage {
	for _, list := range lists {
		if len(list) > 0 {
			return list
		}
	}
	return nil
}

// One list as the choice reads it. The likes are the votes and the lang is
// the language. Fanart.tv states the likes as a string, so a count it did not
// state reads as no votes.
func fanartCandidates(images []fanartImage) []artCandidate {
	candidates := []artCandidate{}
	for _, image := range images {
		if strings.TrimSpace(image.URL) == "" {
			continue
		}
		likes, _ := strconv.Atoi(strings.TrimSpace(image.Likes))
		candidates = append(candidates, artCandidate{
			URL:      image.URL,
			Language: image.Lang,
			Votes:    float64(likes),
		})
	}
	return candidates
}
