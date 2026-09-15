package main

// peertube.go is what the trailer fact asks one PeerTube instance: a
// search of its videos by title, and the trailer entry each result becomes.

import (
	"context"
	"net/url"
	"strconv"
)

// The path an instance searches its own videos on, and the path a person
// watches one video at.
const (
	peertubeSearchPath = "/api/v1/search/videos"
	peertubeWatchPath  = "/w/"
)

// How many results one search reads, newest first. A trailer channel
// publishes a few videos per title, so the newest twenty hold them all.
const (
	peertubeSearchCount = 20
	peertubeSearchSort  = "-publishedAt"
)

// One instance. It takes no account, so the client holds the address and
// nothing else.
type peertubeClient struct {
	providerRequests
}

func newPeertubeClient(base string) *peertubeClient {
	return &peertubeClient{newProviderRequests(providerBlockPeerTube, base, nil)}
}

// One video an instance holds: the long and the short id it answers on,
// and the name the person who published it wrote.
type peertubeVideo struct {
	UUID        string           `json:"uuid"`
	ShortUUID   string           `json:"shortUUID"`
	Name        string           `json:"name"`
	Duration    int              `json:"duration"`
	PublishedAt string           `json:"publishedAt"`
	URL         string           `json:"url"`
	Language    peertubeLanguage `json:"language"`
}

// The language of one video. An instance states null where nobody set
// one, which reads as an empty id.
type peertubeLanguage struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type peertubeSearchAnswer struct {
	Total int             `json:"total"`
	Data  []peertubeVideo `json:"data"`
}

func (c *peertubeClient) search(ctx context.Context, query string) ([]peertubeVideo, error) {
	values := url.Values{
		"search": {query},
		"count":  {strconv.Itoa(peertubeSearchCount)},
		"sort":   {peertubeSearchSort},
	}
	var answer peertubeSearchAnswer
	if err := c.get(ctx, peertubeSearchPath, values, &answer); err != nil {
		return nil, err
	}
	return answer.Data, nil
}

// The page one video plays on: the short id where the instance states one,
// and the long id where it does not.
func peertubeWatchURL(base string, video peertubeVideo) string {
	if video.ShortUUID != "" {
		return base + peertubeWatchPath + video.ShortUUID
	}
	return base + peertubeWatchPath + video.UUID
}

// The trailer answerer of one instance. It keys on the title's own name,
// because an instance holds no provider ids.
// A search answer states no resolution, so every entry this answerer makes
// carries none.
type peertubeTrailerAnswerer struct {
	client *peertubeClient
}

func newPeertubeTrailerAnswerer(client *peertubeClient) peertubeTrailerAnswerer {
	return peertubeTrailerAnswerer{client: client}
}

func (a peertubeTrailerAnswerer) providerBlock() string { return providerBlockPeerTube }

// Every result whose name carries this title. A name that states no year
// still matches on the title alone, at a lower score. A name that says clip
// or names no kind is dropped before it is scored, because a trailer song is
// no trailer of the title. A search for `Dune` answers every Dune video the
// instance holds, so a result that scores 0 is dropped and never recorded.
func (a peertubeTrailerAnswerer) trailers(ctx context.Context, title trailerTitle) ([]trailerEntry, error) {
	if title.title == "" {
		return nil, nil
	}
	videos, err := a.client.search(ctx, title.title)
	if err != nil {
		return nil, err
	}
	entries := []trailerEntry{}
	for _, video := range videos {
		parsed := parseTrailerName(video.Name)
		if !recordedTrailerKind(parsed.kind) {
			continue
		}
		entry := trailerEntry{
			Path:      likenSelfPath,
			Provider:  providerBlockPeerTube,
			Key:       video.UUID,
			Site:      trailerSitePeerTube,
			URL:       peertubeWatchURL(a.client.base, video),
			Name:      video.Name,
			Kind:      parsed.kind,
			Language:  video.Language.ID,
			Published: trailerDate(video.PublishedAt),
		}
		entry.Score, entry.Reason = scoreTrailer(entry, trailerMatch{
			title:     foldTitle(parsed.title) == foldTitle(title.title),
			year:      parsed.year == title.year,
			yearKnown: parsed.year != 0,
		}, title.languages)
		if entry.Score <= 0 {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
