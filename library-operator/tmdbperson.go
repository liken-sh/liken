package main

// What the contributor facts and the credits fact ask TMDb: the person, the
// person's ids in the other databases, the headshot, and the TMDb id of an
// IMDb id. Each fact makes its own call, so a fact that fails leaves the
// others their answers.

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// The size the headshot fact fetches. TMDb serves a profile in w45, w185,
// h632, and the original. h632 is the one the browser can draw a person at
// full height from, and it is far under the 2 MiB a decode draws in bands at.
// w185 is a thumbnail the browser would have to enlarge.
const tmdbHeadshotSize = "h632"

// The person call, which answers the birth date, the death date, the
// biography, and the path of the headshot. Three facts read it, each for its
// own fields.
type tmdbPerson struct {
	Name        string `json:"name"`
	Biography   string `json:"biography"`
	Birthday    string `json:"birthday"`
	Deathday    string `json:"deathday"`
	ProfilePath string `json:"profile_path"`
}

func (c *tmdbClient) person(ctx context.Context, id string) (tmdbPerson, error) {
	var answer tmdbPerson
	err := c.get(ctx, tmdbPersonPath(id), nil, &answer)
	return answer, err
}

func tmdbPersonPath(id string) string {
	return "/3/person/" + id
}

// The ids of the same person in the other databases. The three below are the
// schemes that name a person; the social handles the same answer carries name
// an account and never a person, so the ids fact leaves them.
type tmdbPersonIDs struct {
	IMDbID     string `json:"imdb_id"`
	WikidataID string `json:"wikidata_id"`
	TVRageID   int    `json:"tvrage_id"`
}

func (ids tmdbPersonIDs) providerIDs() providerIDs {
	held := providerIDs{}
	if imdb := strings.TrimSpace(ids.IMDbID); imdb != "" {
		held["imdb"] = imdb
	}
	if wikidata := strings.TrimSpace(ids.WikidataID); wikidata != "" {
		held["wikidata"] = wikidata
	}
	if ids.TVRageID > 0 {
		held["tvrage"] = strconv.Itoa(ids.TVRageID)
	}
	return held
}

func (c *tmdbClient) personIDs(ctx context.Context, id string) (providerIDs, error) {
	var answer tmdbPersonIDs
	if err := c.get(ctx, tmdbPersonPath(id)+"/external_ids", nil, &answer); err != nil {
		return nil, err
	}
	return answer.providerIDs(), nil
}

// What TMDb's find call answers for one IMDb id. The call answers every kind
// of record that holds the id, and only the people are read here.
type tmdbFindAnswer struct {
	PersonResults []struct {
		ID int `json:"id"`
	} `json:"person_results"`
}

// The TMDb id of the person one IMDb id names, or an empty string where TMDb
// names no person for it. A credit or an entry that holds only an IMDb id is
// compared with the rest of the store by this id.
func (c *tmdbClient) personByIMDb(ctx context.Context, imdb string) (string, error) {
	var answer tmdbFindAnswer
	query := url.Values{"external_source": {"imdb_id"}}
	if err := c.get(ctx, "/3/find/"+url.PathEscape(imdb), query, &answer); err != nil {
		return "", err
	}
	for _, person := range answer.PersonResults {
		if person.ID > 0 {
			return strconv.Itoa(person.ID), nil
		}
	}
	return "", nil
}
