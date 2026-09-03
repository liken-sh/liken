package main

// The art answerer TVmaze serves through. TVmaze keys on its own show id, so
// a title reaches its images through one lookup on an id another provider
// gave, and that lookup is held for the rest of the container.

import (
	"context"
	"slices"
)

// TVmaze's art answerer, which needs no account. It holds the nfo answerer's
// lookup, so the art facts and the nfo facts read one rule for which id a
// show is found by, and one held answer per id.
type tvmazeArtAnswerer struct {
	shows tvmazeAnswerer
}

func newTVmazeArtAnswerer(client *tvmazeClient) *tvmazeArtAnswerer {
	return &tvmazeArtAnswerer{shows: newTVmazeAnswerer(client)}
}

func (a *tvmazeArtAnswerer) providerBlock() string { return providerBlockTVmaze }

func (a *tvmazeArtAnswerer) serves(fact string) bool {
	return slices.Contains(providerFacts[providerBlockTVmaze], fact)
}

func (a *tvmazeArtAnswerer) fetchFile(ctx context.Context, address string) ([]byte, error) {
	return a.shows.client.fetchFile(ctx, address)
}

// Which TVmaze type each art fact reads. TVmaze names three types this
// project writes, and a fact outside the three is no answer.
var tvmazeArtworkTypes = map[string]string{
	factPoster:   tvmazeArtworkPoster,
	factBackdrop: tvmazeArtworkBackground,
	factBanner:   tvmazeArtworkBanner,
}

// The images of one series, of the type the fact writes. TVmaze holds series
// alone, so a movie is no answer, and a show it answers nothing for is a miss
// and not an error.
func (a *tvmazeArtAnswerer) candidates(ctx context.Context, fact string, gap artGap,
	title titleRef) ([]artCandidate, error) {
	kind, held := tvmazeArtworkTypes[fact]
	if !held || title.kind != libraryKindSeries {
		return nil, nil
	}
	show, err := a.shows.show(ctx, title.ids)
	if err != nil || show == nil || show.ID == 0 {
		return nil, err
	}
	images, err := a.shows.client.images(ctx, show.ID)
	if err != nil {
		return nil, err
	}
	return tvmazeCandidates(artworkOfType(images, kind)), nil
}

// One list as the choice reads it. TVmaze gives two things less than the
// others: an image carries no language and no vote, so the order is the whole
// choice, and the image TVmaze marks as the main one leads the list.
func tvmazeCandidates(images []tvmazeArtwork) []artCandidate {
	candidates := []artCandidate{}
	for _, main := range []bool{true, false} {
		for _, image := range images {
			if image.Main != main || image.Resolutions.Original.URL == "" {
				continue
			}
			candidates = append(candidates, artCandidate{URL: image.Resolutions.Original.URL})
		}
	}
	return candidates
}
