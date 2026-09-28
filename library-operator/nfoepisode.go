package main

// The episode .nfo file, the one document that places a file under its series.
// It is in a file apart from nfo.go because an episode file may hold two
// episodes, so the reader streams the document and keeps every block it holds.

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// episodeNFO mirrors an episodedetails .nfo, the fields that place an episode
// under its series and describe it.
type episodeNFO struct {
	XMLName   xml.Name      `xml:"episodedetails"`
	Title     string        `xml:"title"`
	Season    int           `xml:"season"`
	Episode   int           `xml:"episode"`
	Aired     string        `xml:"aired"`
	Premiered string        `xml:"premiered"`
	Plot      string        `xml:"plot"`
	Runtime   int           `xml:"runtime"`
	Directors []string      `xml:"director"`
	Writers   []string      `xml:"writer"`
	Credits   []string      `xml:"credits"`
	Actors    []nfoActor    `xml:"actor"`
	UniqueIDs []nfoUniqueID `xml:"uniqueid"`
	FileInfo  nfoFileInfo   `xml:"fileinfo"`
	Ratings   nfoRatings    `xml:"ratings"`
}

// episodeMeta is what parseEpisodeNFOs reads from an episode .nfo.
type episodeMeta struct {
	Title       string
	Season      int
	Episode     int
	Released    string
	ProviderIDs map[string]string
	Body        episodeBody
	Duration    int64
	Stream      streamInfo
	// The nfo facts the block answers, in the form the nfo_facts column holds.
	NFOFacts string
}

// parseEpisodeNFOs reads every episodedetails block an episode .nfo holds, in
// the order they appear in the .nfo file. A file that holds two episodes has
// one block for each, which is how Kodi and Jellyfin write it.
//
// It streams the decoder over the file rather than unmarshaling once, because
// those blocks are consecutive root elements and encoding/xml reads only the
// first of those. A block that fails after one has been read keeps what was
// read, so a truncated second block does not lose the first.
func parseEpisodeNFOs(data []byte) ([]episodeMeta, error) {
	decoder := lenientXML(data)
	var metas []episodeMeta
	for {
		var raw episodeNFO
		err := decoder.Decode(&raw)
		if errors.Is(err, io.EOF) {
			return metas, nil
		}
		if err != nil {
			if len(metas) > 0 {
				return metas, nil
			}
			return nil, err
		}
		metas = append(metas, episodeMetaFrom(raw))
	}
}

// episodeMetaFrom turns one episodedetails block into an episodeMeta.
func episodeMetaFrom(raw episodeNFO) episodeMeta {
	providers := collectProviders(raw.UniqueIDs, "", "", "", "")
	aired := strings.TrimSpace(raw.Aired)
	if aired == "" {
		aired = strings.TrimSpace(raw.Premiered)
	}
	stream := streamFrom(raw.FileInfo)
	return episodeMeta{
		Title:       strings.TrimSpace(raw.Title),
		Season:      raw.Season,
		Episode:     raw.Episode,
		Released:    aired,
		ProviderIDs: providers,
		Body: episodeBody{
			Plot:        strings.TrimSpace(raw.Plot),
			Directors:   trimAll(raw.Directors),
			Writers:     mergeDedup(raw.Writers, raw.Credits),
			Cast:        castMembers(raw.Actors),
			ProviderIDs: providers,
			Ratings:     bodyRatings(raw.Ratings.Ratings),
		},
		NFOFacts: nfoFactsAnswered(raw.Plot, "", raw.Ratings.Ratings),
		Duration: itemDuration(stream, raw.Runtime),
		Stream:   stream,
	}
}
