package main

// tmdbtrailers.go is what the trailer fact asks TMDb: the videos of one
// title, and the trailer entry each one becomes.

import (
	"context"
	"strconv"
)

// One video TMDb holds for a title. TMDb hosts no video: the record is a
// key on another site and the words that say what the video is.
type tmdbVideo struct {
	Language    string `json:"iso_639_1"`
	Country     string `json:"iso_3166_1"`
	Name        string `json:"name"`
	Key         string `json:"key"`
	Site        string `json:"site"`
	Size        int    `json:"size"`
	Type        string `json:"type"`
	Official    bool   `json:"official"`
	PublishedAt string `json:"published_at"`
	ID          string `json:"id"`
}

type tmdbVideosAnswer struct {
	Results []tmdbVideo `json:"results"`
}

// The videos of one title, on the movie or the series path.
func (c *tmdbClient) videos(ctx context.Context, kind string, id int) ([]tmdbVideo, error) {
	var answer tmdbVideosAnswer
	if err := c.get(ctx, tmdbTitlePath(kind, id)+"/videos", nil, &answer); err != nil {
		return nil, err
	}
	return answer.Results, nil
}

// The sites TMDb names. This operator knows the address form of two of
// them and skips the rest.
const (
	tmdbVideoSiteYouTube = "YouTube"
	tmdbVideoSiteVimeo   = "Vimeo"
)

// The site of one video and the page it plays on, or nothing for a site
// this operator has no address form for.
func tmdbVideoSite(site, key string) (string, string) {
	switch site {
	case tmdbVideoSiteYouTube:
		return trailerSiteYouTube, "https://www.youtube.com/watch?v=" + key
	case tmdbVideoSiteVimeo:
		return trailerSiteVimeo, "https://vimeo.com/" + key
	}
	return "", ""
}

// The words TMDb names a video's type with. Three of them are kinds this
// fact ranks; every other type (Featurette, Behind the Scenes, Bloopers)
// is other.
const (
	tmdbVideoTypeTrailer = "Trailer"
	tmdbVideoTypeTeaser  = "Teaser"
	tmdbVideoTypeClip    = "Clip"
)

func tmdbVideoKind(kind string) string {
	switch kind {
	case tmdbVideoTypeTrailer:
		return trailerKindTrailer
	case tmdbVideoTypeTeaser:
		return trailerKindTeaser
	case tmdbVideoTypeClip:
		return trailerKindClip
	}
	return trailerKindOther
}

// TMDb's trailer answerer: one account, asked for the videos of one title.
type tmdbTrailerAnswerer struct {
	client *tmdbClient
}

func newTMDbTrailerAnswerer(client *tmdbClient) tmdbTrailerAnswerer {
	return tmdbTrailerAnswerer{client: client}
}

func (a tmdbTrailerAnswerer) providerBlock() string { return providerBlockTMDb }

// Every video TMDb holds for this title. The ask keys on the TMDb id, so a
// title with no TMDb id gets no call and no trailer.
func (a tmdbTrailerAnswerer) trailers(ctx context.Context, title trailerTitle) ([]trailerEntry, error) {
	id, err := strconv.Atoi(title.ids["tmdb"])
	if err != nil || id <= 0 {
		return nil, nil
	}
	videos, err := a.client.videos(ctx, title.kind, id)
	if err != nil {
		return nil, err
	}
	entries := []trailerEntry{}
	for _, video := range videos {
		site, address := tmdbVideoSite(video.Site, video.Key)
		if site == "" {
			continue
		}
		entry := trailerEntry{
			Path:      likenSelfPath,
			Provider:  providerBlockTMDb,
			Key:       video.Key,
			Site:      site,
			URL:       address,
			Name:      video.Name,
			Kind:      tmdbVideoKind(video.Type),
			Language:  video.Language,
			Official:  video.Official,
			Published: trailerDate(video.PublishedAt),
		}
		entry.Score, entry.Reason = scoreTrailer(entry, trailerName{}, title)
		entries = append(entries, entry)
	}
	return entries, nil
}
