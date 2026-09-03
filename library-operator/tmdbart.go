package main

// The art answerer TMDb serves through. It holds what the other two do not:
// the provider's own settings, which name the image host and the sizes, read
// once for the container and read at all only where a title has an image to
// fetch.

import (
	"context"
	"slices"
)

// TMDb's art answerer: one account and the settings it read. The settings are
// held here because every address hangs off the image host they name.
type tmdbArtAnswerer struct {
	client        *tmdbClient
	configuration tmdbConfiguration
	read          bool
}

func newTMDbArtAnswerer(client *tmdbClient) *tmdbArtAnswerer {
	return &tmdbArtAnswerer{client: client}
}

func (a *tmdbArtAnswerer) providerBlock() string { return providerBlockTMDb }

func (a *tmdbArtAnswerer) serves(fact string) bool {
	return slices.Contains(providerFacts[providerBlockTMDb], fact)
}

func (a *tmdbArtAnswerer) fetchFile(ctx context.Context, address string) ([]byte, error) {
	return a.client.fetchFile(ctx, address)
}

// The one read of the provider's own settings.
func (a *tmdbArtAnswerer) settings(ctx context.Context) (tmdbConfiguration, error) {
	if a.read {
		return a.configuration, nil
	}
	configuration, err := a.client.configuration(ctx)
	if err != nil {
		return tmdbConfiguration{}, err
	}
	a.configuration, a.read = configuration, true
	return configuration, nil
}

// The images of one gap, from the endpoint its fact reads, with the address
// of each one built from the size that fact asks for. A fact TMDb keeps no
// list for answers nothing. The settings are read after the images, so a
// title the provider has no image for costs one call.
func (a *tmdbArtAnswerer) candidates(ctx context.Context, fact string, gap artGap,
	title titleRef) ([]artCandidate, error) {
	art := artTypes[fact]
	if art.list == "" {
		return nil, nil
	}
	answer, err := a.client.images(ctx, title.kind, fact, gap)
	if err != nil {
		return nil, err
	}
	images := answer.list(art.list)
	if len(images) == 0 {
		return nil, nil
	}
	configuration, err := a.settings(ctx)
	if err != nil {
		return nil, err
	}
	size := configuration.sizeFor(art)
	candidates := []artCandidate{}
	for _, image := range images {
		if image.FilePath == "" {
			continue
		}
		candidates = append(candidates, artCandidate{
			URL:      configuration.imageURL(size, image.FilePath),
			Language: image.Language,
			Votes:    image.VoteAverage,
		})
	}
	return candidates, nil
}
