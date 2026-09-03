package main

// What the series facts ask TVmaze: a lookup by an id another provider gave,
// the show itself, its cast, and its images. TVmaze serves series alone and
// takes no account, so a MetadataProvider of this block names no Secret. Its
// limit is 20 calls every 10 seconds per address, and a 429 takes the
// cooldown every provider takes.

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// The provider's own address, which only a test replaces.
var tvmazeAPIBase = "https://api.tvmaze.com"

// The paths TVmaze answers on, and the show the check reads, which is the
// first show TVmaze holds.
const (
	tvmazeLookupPath = "/lookup/shows"
	tvmazeShowsPath  = "/shows/"
	tvmazeCheckPath  = tvmazeShowsPath + "1"
)

// The schemes a lookup takes, which are the ids the identity fact writes into
// the .nfo.
const (
	tvmazeSchemeIMDb    = "imdb"
	tvmazeSchemeTheTVDB = "thetvdb"
)

// TVmaze needs no account, so this client holds the address and nothing else.
type tvmazeClient struct {
	providerRequests
}

func newTVmazeClient(base string) *tvmazeClient {
	return &tvmazeClient{newProviderRequests(providerBlockTVmaze, base, nil)}
}

// One show, with what the overview fact reads beside the ids the identity
// fact reads. The summary is HTML, which the nfo strips.
type tvmazeShow struct {
	ID             int             `json:"id"`
	Name           string          `json:"name"`
	Premiered      string          `json:"premiered"`
	Genres         []string        `json:"genres"`
	Runtime        int             `json:"runtime"`
	AverageRuntime int             `json:"averageRuntime"`
	Summary        string          `json:"summary"`
	Rating         tvmazeRating    `json:"rating"`
	Network        *tvmazeNetwork  `json:"network"`
	WebChannel     *tvmazeNetwork  `json:"webChannel"`
	Image          tvmazeImage     `json:"image"`
	Externals      tvmazeExternals `json:"externals"`
}

type tvmazeRating struct {
	Average float64 `json:"average"`
}

// Who broadcast the show. A show on a streaming service names a webChannel
// and no network.
type tvmazeNetwork struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type tvmazeImage struct {
	Medium   string `json:"medium"`
	Original string `json:"original"`
}

// The ids of the other databases, which is what makes a TVmaze answer an
// identity.
type tvmazeExternals struct {
	IMDb    string `json:"imdb"`
	TheTVDB int    `json:"thetvdb"`
	TVRage  int    `json:"tvrage"`
}

// One person in the cast, and the character they play.
type tvmazeCastMember struct {
	Person    tvmazePerson    `json:"person"`
	Character tvmazeCharacter `json:"character"`
}

type tvmazePerson struct {
	ID    int         `json:"id"`
	Name  string      `json:"name"`
	Image tvmazeImage `json:"image"`
}

type tvmazeCharacter struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// One image of a show. The type is poster, banner, background, or typography,
// and every image carries its original resolution.
type tvmazeArtwork struct {
	ID          int               `json:"id"`
	Type        string            `json:"type"`
	Main        bool              `json:"main"`
	Resolutions tvmazeResolutions `json:"resolutions"`
}

type tvmazeResolutions struct {
	Original tvmazeResolution `json:"original"`
	Medium   tvmazeResolution `json:"medium"`
}

type tvmazeResolution struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// The art types TVmaze names, which the art facts read.
const (
	tvmazeArtworkPoster     = "poster"
	tvmazeArtworkBanner     = "banner"
	tvmazeArtworkBackground = "background"
)

// The show one id names, under the scheme that id belongs to. TVmaze answers
// 404 for an id it does not hold, which is a miss and not an error.
func (c *tvmazeClient) lookup(ctx context.Context, scheme, id string) (*tvmazeShow, error) {
	answer := &tvmazeShow{}
	if err := c.get(ctx, tvmazeLookupPath, url.Values{scheme: {id}}, answer); err != nil {
		if answeredWith(err, http.StatusNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return answer, nil
}

// One show by TVmaze's own id, which is what the lookup answered.
func (c *tvmazeClient) show(ctx context.Context, id int) (*tvmazeShow, error) {
	answer := &tvmazeShow{}
	if err := c.get(ctx, tvmazeShowsPath+strconv.Itoa(id), nil, answer); err != nil {
		return nil, err
	}
	return answer, nil
}

// The cast of one show, in the order TVmaze holds it, which the credits fact
// writes as the actors.
func (c *tvmazeClient) cast(ctx context.Context, id int) ([]tvmazeCastMember, error) {
	answer := []tvmazeCastMember{}
	if err := c.get(ctx, tvmazeShowsPath+strconv.Itoa(id)+"/cast", nil, &answer); err != nil {
		return nil, err
	}
	return answer, nil
}

// Every image of one show, of every type. Each art fact narrows the list to
// the type it writes.
func (c *tvmazeClient) images(ctx context.Context, id int) ([]tvmazeArtwork, error) {
	answer := []tvmazeArtwork{}
	if err := c.get(ctx, tvmazeShowsPath+strconv.Itoa(id)+"/images", nil, &answer); err != nil {
		return nil, err
	}
	return answer, nil
}

// The images of one type, in the order TVmaze holds them.
func artworkOfType(images []tvmazeArtwork, kind string) []tvmazeArtwork {
	held := []tvmazeArtwork{}
	for _, image := range images {
		if image.Type == kind {
			held = append(held, image)
		}
	}
	return held
}
