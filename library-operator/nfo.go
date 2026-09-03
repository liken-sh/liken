package main

// The episode sidecar's reader is in nfoepisode.go.

import (
	"bytes"
	"encoding/xml"
	"strconv"
	"strings"
)

// nfoUniqueID is one uniqueid element, the provider and the id it assigns, as
// in a uniqueid of type tmdb with the value 603.
type nfoUniqueID struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

// nfoActor is one actor element, a name and the part played.
// The thumb is read because the credits fact rewrites the actor group, and a
// picture another writer put there stays only where this reader keeps it.
// The order is read because Jellyfin writes the actor elements in no
// particular order and puts the billing in each one, so document order alone
// puts a lead last.
type nfoActor struct {
	Name  string `xml:"name"`
	Role  string `xml:"role"`
	Thumb string `xml:"thumb"`
	Order *int   `xml:"order"`
}

// nfoSet is a set element, either a plain name or a nested name element.
// tmdbcolid is the collection id Jellyfin writes on the element, and it
// scopes the set's id where the sidecar carries one.
type nfoSet struct {
	TMDBColID string `xml:"tmdbcolid,attr"`
	Name      string `xml:"name"`
	Value     string `xml:",chardata"`
}

// nfoVideo is one video stream in streamdetails: the width, height, codec, and
// duration a media probe would otherwise read.
type nfoVideo struct {
	Width    int     `xml:"width"`
	Height   int     `xml:"height"`
	Codec    string  `xml:"codec"`
	Duration float64 `xml:"durationinseconds"`
}

// nfoAudio is one audio stream: the codec.
type nfoAudio struct {
	Codec string `xml:"codec"`
}

// nfoStreamDetails is the technical block a sidecar carries for a file.
type nfoStreamDetails struct {
	Video []nfoVideo `xml:"video"`
	Audio []nfoAudio `xml:"audio"`
}

// nfoFileInfo wraps streamdetails, the shape Jellyfin and Kodi both use.
type nfoFileInfo struct {
	StreamDetails nfoStreamDetails `xml:"streamdetails"`
}

// movieNFO mirrors movie.nfo. Every field the movie body carries has a source
// here, and the convenience id tags are read beside uniqueid because older
// sidecars wrote them.
type movieNFO struct {
	XMLName       xml.Name      `xml:"movie"`
	Title         string        `xml:"title"`
	Year          int           `xml:"year"`
	Premiered     string        `xml:"premiered"`
	Runtime       int           `xml:"runtime"`
	Plot          string        `xml:"plot"`
	Tagline       string        `xml:"tagline"`
	Genres        []string      `xml:"genre"`
	Studios       []string      `xml:"studio"`
	Directors     []string      `xml:"director"`
	Writers       []string      `xml:"writer"`
	Credits       []string      `xml:"credits"`
	Actors        []nfoActor    `xml:"actor"`
	Set           nfoSet        `xml:"set"`
	Country       string        `xml:"country"`
	MPAA          string        `xml:"mpaa"`
	Certification string        `xml:"certification"`
	Ratings       nfoRatings    `xml:"ratings"`
	UniqueIDs     []nfoUniqueID `xml:"uniqueid"`
	IMDBID        string        `xml:"imdbid"`
	TMDBID        string        `xml:"tmdbid"`
	TVDBID        string        `xml:"tvdbid"`
	ID            string        `xml:"id"`
	FileInfo      nfoFileInfo   `xml:"fileinfo"`
}

// seriesNFO mirrors tvshow.nfo, the series-level fields.
type seriesNFO struct {
	XMLName       xml.Name      `xml:"tvshow"`
	Title         string        `xml:"title"`
	Year          int           `xml:"year"`
	Premiered     string        `xml:"premiered"`
	Plot          string        `xml:"plot"`
	Tagline       string        `xml:"tagline"`
	Genres        []string      `xml:"genre"`
	Studios       []string      `xml:"studio"`
	Creators      []string      `xml:"creator"`
	Actors        []nfoActor    `xml:"actor"`
	Country       string        `xml:"country"`
	MPAA          string        `xml:"mpaa"`
	Certification string        `xml:"certification"`
	Ratings       nfoRatings    `xml:"ratings"`
	UniqueIDs     []nfoUniqueID `xml:"uniqueid"`
	IMDBID        string        `xml:"imdbid"`
	TMDBID        string        `xml:"tmdbid"`
	TVDBID        string        `xml:"tvdbid"`
	ID            string        `xml:"id"`
}

// streamDetailsNFO is the one block a per-file sidecar is read for, under
// whatever root the sidecar carries. It names no root element on purpose, so
// encoding/xml takes the document's own: movie in a movies library,
// episodedetails in a series library.
type streamDetailsNFO struct {
	FileInfo nfoFileInfo `xml:"fileinfo"`
}

// parseStreamNFO reads the stream details out of a sidecar of either root. It
// reads that block alone, because a per-file sidecar has nothing else a file
// row takes, and a movie and an episode sidecar answer the same way.
func parseStreamNFO(data []byte) (streamInfo, error) {
	var raw streamDetailsNFO
	if err := lenientXML(data).Decode(&raw); err != nil {
		return streamInfo{}, err
	}
	return streamFrom(raw.FileInfo), nil
}

// streamInfo is the technical attributes a file carries: the resolution, the
// codecs, and the duration in milliseconds.
type streamInfo struct {
	Width      int
	Height     int
	VideoCodec string
	AudioCodec string
	DurationMs int64
}

// present reports whether the stream holds anything a file can take.
func (s streamInfo) present() bool {
	return s.Width > 0 || s.Height > 0 || s.VideoCodec != "" || s.AudioCodec != "" || s.DurationMs > 0
}

// movieMeta is what parseMovieNFO reads: the identity, the release, the provider
// ids, the movie body, the item duration, the file stream, and the id of the
// set the sidecar names, empty where it names none.
type movieMeta struct {
	Title       string
	Year        int
	Released    string
	ProviderIDs map[string]string
	Body        movieBody
	Duration    int64
	Stream      streamInfo
	SetID       string
	// The nfo facts this sidecar already answers, in the form the nfo_facts
	// column holds.
	NFOFacts string
}

// parseMovieNFO reads movie.nfo into a movieMeta. The art and the provider-id
// copy on the body are filled by the walk, because they come from the folder
// beside the sidecar.
func parseMovieNFO(data []byte) (movieMeta, error) {
	var raw movieNFO
	if err := lenientXML(data).Decode(&raw); err != nil {
		return movieMeta{}, err
	}
	providers := collectProviders(raw.UniqueIDs, raw.IMDBID, raw.TMDBID, raw.TVDBID, raw.ID)
	year, released := releaseFields(raw.Year, raw.Premiered)
	stream := streamFrom(raw.FileInfo)
	collection := collectionName(raw.Set)
	return movieMeta{
		Title:       strings.TrimSpace(raw.Title),
		Year:        year,
		Released:    released,
		ProviderIDs: providers,
		Body: movieBody{
			Plot:          strings.TrimSpace(raw.Plot),
			Tagline:       strings.TrimSpace(raw.Tagline),
			Cast:          castMembers(raw.Actors),
			Directors:     trimAll(raw.Directors),
			Writers:       mergeDedup(raw.Writers, raw.Credits),
			Studios:       trimAll(raw.Studios),
			Genres:        trimAll(raw.Genres),
			Collection:    collection,
			ProviderIDs:   providers,
			Country:       strings.TrimSpace(raw.Country),
			ContentRating: contentRating(raw.MPAA, raw.Certification),
			Ratings:       bodyRatings(raw.Ratings.Ratings),
		},
		Duration: itemDuration(stream, raw.Runtime),
		Stream:   stream,
		SetID:    setID(strings.TrimSpace(raw.Set.TMDBColID), collection),
		NFOFacts: nfoFactsAnswered(raw.Plot, contentRating(raw.MPAA, raw.Certification),
			raw.Ratings.Ratings),
	}, nil
}

// seriesMeta is what parseSeriesNFO reads from tvshow.nfo.
type seriesMeta struct {
	Title       string
	Year        int
	Released    string
	ProviderIDs map[string]string
	Body        seriesBody
	// The nfo facts this sidecar already answers, in the form the nfo_facts
	// column holds; a series sidecar answers the same facts a movie one does.
	NFOFacts string
}

// parseSeriesNFO reads tvshow.nfo into a seriesMeta.
func parseSeriesNFO(data []byte) (seriesMeta, error) {
	var raw seriesNFO
	if err := lenientXML(data).Decode(&raw); err != nil {
		return seriesMeta{}, err
	}
	providers := collectProviders(raw.UniqueIDs, raw.IMDBID, raw.TMDBID, raw.TVDBID, raw.ID)
	year, released := releaseFields(raw.Year, raw.Premiered)
	return seriesMeta{
		Title:       strings.TrimSpace(raw.Title),
		Year:        year,
		Released:    released,
		ProviderIDs: providers,
		Body: seriesBody{
			Plot:          strings.TrimSpace(raw.Plot),
			Tagline:       strings.TrimSpace(raw.Tagline),
			Cast:          castMembers(raw.Actors),
			Creators:      trimAll(raw.Creators),
			Studios:       trimAll(raw.Studios),
			Genres:        trimAll(raw.Genres),
			ProviderIDs:   providers,
			Country:       strings.TrimSpace(raw.Country),
			ContentRating: contentRating(raw.MPAA, raw.Certification),
			Ratings:       bodyRatings(raw.Ratings.Ratings),
		},
		NFOFacts: nfoFactsAnswered(raw.Plot, contentRating(raw.MPAA, raw.Certification),
			raw.Ratings.Ratings),
	}, nil
}

// collectProviders reads every uniqueid and the convenience id tags into one
// map, keyed by the lowercased provider. A uniqueid wins over a convenience tag
// for the same provider, and a bare id element that reads like an IMDB id fills
// imdb.
func collectProviders(uids []nfoUniqueID, imdb, tmdb, tvdb, id string) map[string]string {
	providers := map[string]string{}
	for _, u := range uids {
		provider := strings.ToLower(strings.TrimSpace(u.Type))
		value := strings.TrimSpace(u.Value)
		if provider != "" && value != "" {
			providers[provider] = value
		}
	}
	add := func(provider, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, held := providers[provider]; !held {
			providers[provider] = value
		}
	}
	add("imdb", imdb)
	add("tmdb", tmdb)
	add("tvdb", tvdb)
	if strings.HasPrefix(strings.TrimSpace(id), "tt") {
		add("imdb", id)
	}
	if len(providers) == 0 {
		return nil
	}
	return providers
}

// castMembers keeps the credited people with a name, in the sidecar's own
// order, which is billing order.
func castMembers(actors []nfoActor) []castMember {
	var out []castMember
	for _, actor := range actors {
		name := strings.TrimSpace(actor.Name)
		if name == "" {
			continue
		}
		out = append(out, castMember{Name: name, Role: strings.TrimSpace(actor.Role)})
	}
	return out
}

// trimAll trims every entry and drops the empty ones.
func trimAll(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// mergeDedup joins two lists, trims them, and keeps the first of each name, so a
// writer named in both writer and credits appears once.
func mergeDedup(first, second []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range append(append([]string{}, first...), second...) {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// collectionName reads a set's name, whether it is a nested name element or the
// element's own text.
func collectionName(set nfoSet) string {
	if name := strings.TrimSpace(set.Name); name != "" {
		return name
	}
	return strings.TrimSpace(set.Value)
}

// contentRating prefers mpaa and falls back to certification, the two tags a
// sidecar writes the rating in.
func contentRating(mpaa, certification string) string {
	if r := strings.TrimSpace(mpaa); r != "" {
		return r
	}
	return strings.TrimSpace(certification)
}

// releaseFields resolves the year and the released column. premiered is an ISO
// date and the released column takes it as it stands; the year fills the slug
// and falls back to the leading digits of the date.
func releaseFields(year int, premiered string) (int, string) {
	released := strings.TrimSpace(premiered)
	if year == 0 {
		year = leadingYear(released)
	}
	if released == "" && year > 0 {
		released = strconv.Itoa(year)
	}
	return year, released
}

// leadingYear reads a four-digit year off the front of a date, or 0. The
// range is the release range in names.go, the same one a folder name is
// read against.
func leadingYear(date string) int {
	if len(date) < 4 {
		return 0
	}
	year, err := strconv.Atoi(date[:4])
	if err != nil || !plausibleYear(year) {
		return 0
	}
	return year
}

// streamFrom reads the first video and audio stream. A file has one of each in
// the ordinary case, and the first is the one the display plays.
func streamFrom(info nfoFileInfo) streamInfo {
	var stream streamInfo
	if len(info.StreamDetails.Video) > 0 {
		video := info.StreamDetails.Video[0]
		stream.Width = video.Width
		stream.Height = video.Height
		stream.VideoCodec = normalizeCodec(video.Codec)
		if video.Duration > 0 {
			stream.DurationMs = int64(video.Duration * 1000)
		}
	}
	if len(info.StreamDetails.Audio) > 0 {
		stream.AudioCodec = normalizeCodec(info.StreamDetails.Audio[0].Codec)
	}
	return stream
}

// normalizeCodec lowercases a codec name, so h264 and H264 read the same in the
// catalog.
func normalizeCodec(codec string) string {
	return strings.ToLower(strings.TrimSpace(codec))
}

// itemDuration is the item's runtime in seconds: the stream's own duration where
// the sidecar carried one, or runtime in minutes.
func itemDuration(stream streamInfo, runtimeMinutes int) int64 {
	if stream.DurationMs > 0 {
		return stream.DurationMs / 1000
	}
	if runtimeMinutes > 0 {
		return int64(runtimeMinutes) * 60
	}
	return 0
}

// Every read of a sidecar is lenient. Jellyfin writes a bare ampersand in a
// URL, as in a thumb that names an image server's id, and the strict reader
// stops at the first one, which failed a rating on a third of the series.
// A lenient reader passes an unknown entity through as text, and every
// element the facts read or edit is found the same way.
func lenientXML(data []byte) *xml.Decoder {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = false
	return decoder
}
