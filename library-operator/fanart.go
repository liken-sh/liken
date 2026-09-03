package main

// What the art facts ask Fanart.tv: one call per title that answers every art
// type at once, keyed by the TMDb id of a movie and by the TheTVDB id of a
// series. Fanart.tv is the only provider of the clearart, the landscape, the
// discart, and the season banner.

import (
	"context"
	"net/http"
)

// The provider's own address, which only a test replaces.
var fanartAPIBase = "https://webservice.fanart.tv"

// The two paths, and the parameter the key travels in. The check calls a
// movie every Fanart.tv account can read, so a key that answers it is a key
// that works.
const (
	fanartMoviePath     = "/v3/movies/"
	fanartSeriesPath    = "/v3/tv/"
	fanartAPIKeyParam   = "api_key"
	fanartCheckPath     = fanartMoviePath + "550"
	fanartSeasonAllMark = "all"
)

// One account with Fanart.tv, which is the project key alone. A personal key
// on top of it earns fresher art, and the block holds no field for one yet.
type fanartClient struct {
	providerRequests
	key string
}

func newFanartClient(base, key string) *fanartClient {
	client := &fanartClient{key: key}
	client.providerRequests = newProviderRequests(providerBlockFanart, base,
		func(request *http.Request) { queryKey(fanartAPIKeyParam, client.key)(request) })
	return client
}

// One image, as every type list holds it. The season field is on the season-
// scoped types alone, and it carries a season number or the word all. The
// likes are a count of the votes the image took.
type fanartImage struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Lang     string `json:"lang"`
	Likes    string `json:"likes"`
	Season   string `json:"season,omitempty"`
	Disc     string `json:"disc,omitempty"`
	DiscType string `json:"disc_type,omitempty"`
}

// The art of one movie, one list per type. The names are Fanart.tv's own, and
// each art fact reads the list of the type it writes.
type fanartMovie struct {
	Name        string        `json:"name"`
	TMDbID      string        `json:"tmdb_id"`
	IMDbID      string        `json:"imdb_id"`
	Posters     []fanartImage `json:"movieposter"`
	Backgrounds []fanartImage `json:"moviebackground"`
	HDLogos     []fanartImage `json:"hdmovielogo"`
	Logos       []fanartImage `json:"movielogo"`
	HDClearart  []fanartImage `json:"hdmovieclearart"`
	Clearart    []fanartImage `json:"movieart"`
	Banners     []fanartImage `json:"moviebanner"`
	Thumbs      []fanartImage `json:"moviethumb"`
	Discs       []fanartImage `json:"moviedisc"`
}

// The art of one series, with the season-scoped lists beside the ones that
// cover the whole show.
type fanartSeries struct {
	Name          string        `json:"name"`
	TheTVDBID     string        `json:"thetvdb_id"`
	Posters       []fanartImage `json:"tvposter"`
	Backgrounds   []fanartImage `json:"showbackground"`
	HDLogos       []fanartImage `json:"hdtvlogo"`
	Clearlogos    []fanartImage `json:"clearlogo"`
	HDClearart    []fanartImage `json:"hdclearart"`
	Clearart      []fanartImage `json:"clearart"`
	Banners       []fanartImage `json:"tvbanner"`
	Thumbs        []fanartImage `json:"tvthumb"`
	SeasonPosters []fanartImage `json:"seasonposter"`
	SeasonBanners []fanartImage `json:"seasonbanner"`
	SeasonThumbs  []fanartImage `json:"seasonthumb"`
	Characterart  []fanartImage `json:"characterart"`
}

// The art of one movie, by its TMDb id or its IMDb id, which Fanart.tv reads
// as one lookup key. A title Fanart.tv does not hold answers 404, which is a
// miss and not an error.
func (c *fanartClient) movie(ctx context.Context, id string) (*fanartMovie, error) {
	answer := &fanartMovie{}
	if err := c.get(ctx, fanartMoviePath+id, nil, answer); err != nil {
		if answeredWith(err, http.StatusNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return answer, nil
}

// The art of one series, by its TheTVDB id. Fanart.tv's series endpoint reads
// that id alone, which is why the identity fact writes every id it can into
// the .nfo.
func (c *fanartClient) series(ctx context.Context, thetvdbID string) (*fanartSeries, error) {
	answer := &fanartSeries{}
	if err := c.get(ctx, fanartSeriesPath+thetvdbID, nil, answer); err != nil {
		if answeredWith(err, http.StatusNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return answer, nil
}

// The images of one season: the season-scoped list narrowed to that number.
// Fanart.tv marks an image that covers every season with the word all, and
// that image answers for any season.
func seasonImages(images []fanartImage, season string) []fanartImage {
	held := []fanartImage{}
	for _, image := range images {
		if image.Season == season || image.Season == fanartSeasonAllMark {
			held = append(held, image)
		}
	}
	return held
}
