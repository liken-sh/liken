package main

// What the art facts ask TMDb: the image lists of a title, a season, and an
// episode, the configuration that names the image host and the sizes, and the
// rule that picks one image out of a list. The download itself is the shared
// one in providerhttp.go, so a 429 from the image host waits the way a 429
// from the API does.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// The arrays TMDb answers images in. A movie and a series answer posters,
// backdrops, and logos, a season answers posters, and an episode answers
// stills.
const (
	tmdbPosters   = "posters"
	tmdbBackdrops = "backdrops"
	tmdbLogos     = "logos"
	tmdbStills    = "stills"
)

// One image as TMDb states it. The language is null for an image with no text
// in it, which is why the choice below reads a null language as its second
// preference and never as a miss.
type tmdbImage struct {
	FilePath    string  `json:"file_path"`
	Language    string  `json:"iso_639_1"`
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
}

// One images answer, with every array the four endpoints return, so one type
// reads a movie, a series, a season, and an episode.
type tmdbImageAnswer struct {
	Posters   []tmdbImage `json:"posters"`
	Backdrops []tmdbImage `json:"backdrops"`
	Logos     []tmdbImage `json:"logos"`
	Stills    []tmdbImage `json:"stills"`
}

func (a tmdbImageAnswer) list(name string) []tmdbImage {
	switch name {
	case tmdbPosters:
		return a.Posters
	case tmdbBackdrops:
		return a.Backdrops
	case tmdbLogos:
		return a.Logos
	default:
		return a.Stills
	}
}

// What /configuration says about images: the host every file path hangs off,
// and the sizes each kind of image is served in. The enricher reads it once
// per fact and never asks for a size TMDb does not serve.
type tmdbConfiguration struct {
	Images struct {
		SecureBaseURL string   `json:"secure_base_url"`
		PosterSizes   []string `json:"poster_sizes"`
		BackdropSizes []string `json:"backdrop_sizes"`
		LogoSizes     []string `json:"logo_sizes"`
		StillSizes    []string `json:"still_sizes"`
	} `json:"images"`
}

// The sizes of one list, as the configuration names them.
func (c tmdbConfiguration) sizes(list string) []string {
	switch list {
	case tmdbPosters:
		return c.Images.PosterSizes
	case tmdbBackdrops:
		return c.Images.BackdropSizes
	case tmdbLogos:
		return c.Images.LogoSizes
	default:
		return c.Images.StillSizes
	}
}

// The size the fetch asks for: the type's own size where TMDb serves it, and
// the original where it does not, because every kind of image is served in
// the original.
const tmdbOriginalSize = "original"

func (c tmdbConfiguration) sizeFor(art artType) string {
	for _, size := range c.sizes(art.list) {
		if size == art.size {
			return art.size
		}
	}
	return tmdbOriginalSize
}

// The address of one image: the host, the size, and the path TMDb gave.
func (c tmdbConfiguration) imageURL(size, filePath string) string {
	return strings.TrimSuffix(c.Images.SecureBaseURL, "/") + "/" + size + filePath
}

// The one read of the provider's own settings.
func (c *tmdbClient) configuration(ctx context.Context) (tmdbConfiguration, error) {
	var answer tmdbConfiguration
	if err := c.get(ctx, tmdbConfigurationPath, nil, &answer); err != nil {
		return tmdbConfiguration{}, err
	}
	if answer.Images.SecureBaseURL == "" {
		return tmdbConfiguration{}, fmt.Errorf("tmdb %s: the answer names no image host", tmdbConfigurationPath)
	}
	return answer, nil
}

// The images of one gap, from the endpoint its fact reads: a movie, a series,
// one of its seasons, or one of its episodes.
func (c *tmdbClient) images(ctx context.Context, kind, fact string, gap artGap) (tmdbImageAnswer, error) {
	var answer tmdbImageAnswer
	if err := c.get(ctx, tmdbImagesPath(kind, fact, gap), nil, &answer); err != nil {
		return tmdbImageAnswer{}, err
	}
	return answer, nil
}

// Which endpoint one fact reads. The season and the episode hang off the
// series, and the title's own art follows the kind of the library.
func tmdbImagesPath(kind, fact string, gap artGap) string {
	series := "/3/tv/" + gap.tmdb
	switch fact {
	case factSeasonPoster:
		return series + "/season/" + strconv.Itoa(gap.season) + "/images"
	case factEpisodeThumb:
		return series + "/season/" + strconv.Itoa(gap.season) +
			"/episode/" + strconv.Itoa(gap.episode) + "/images"
	}
	if kind == libraryKindSeries {
		return series + "/images"
	}
	return "/3/movie/" + gap.tmdb + "/images"
}

// The choice: the highest-voted image in the library's own language, then the
// highest-voted image with no language, then the highest-voted image of any
// language. Art with the title's own text is what a person reads on the
// screen, and art with no text reads in every language.
func chooseImage(images []tmdbImage, language string) (tmdbImage, bool) {
	for _, want := range []string{language, ""} {
		if image, held := bestImage(images, func(image tmdbImage) bool {
			return image.Language == want
		}); held {
			return image, true
		}
	}
	return bestImage(images, func(tmdbImage) bool { return true })
}

// The highest-voted image the test admits. The first of two equal votes wins,
// which keeps TMDb's own order.
func bestImage(images []tmdbImage, admits func(tmdbImage) bool) (tmdbImage, bool) {
	best := tmdbImage{}
	held := false
	for _, image := range images {
		if image.FilePath == "" || !admits(image) {
			continue
		}
		if !held || image.VoteAverage > best.VoteAverage {
			best, held = image, true
		}
	}
	return best, held
}
