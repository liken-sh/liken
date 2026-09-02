package main

// These tests read the .nfo sidecars against inline XML, so
// the movie, series, and episode fields, the provider ids, and the
// streamdetails a file's attributes come from are proved with no volume.

import (
	"reflect"
	"testing"
)

func TestParseMovieNFO(t *testing.T) {
	data := []byte(`<?xml version="1.0"?>
<movie>
  <title>The Matrix</title>
  <year>1999</year>
  <premiered>1999-03-31</premiered>
  <plot>A hacker learns the truth.</plot>
  <tagline>Free your mind.</tagline>
  <runtime>136</runtime>
  <genre>Action</genre>
  <genre>Science Fiction</genre>
  <studio>Warner Bros.</studio>
  <country>United States</country>
  <mpaa>R</mpaa>
  <set><name>The Matrix Collection</name></set>
  <director>Lana Wachowski</director>
  <writer>Lilly Wachowski</writer>
  <credits>Lilly Wachowski</credits>
  <actor><name>Keanu Reeves</name><role>Neo</role></actor>
  <actor><name></name></actor>
  <uniqueid type="tmdb">603</uniqueid>
  <uniqueid type="imdb">tt0133093</uniqueid>
  <fileinfo><streamdetails>
    <video><codec>H264</codec><width>1920</width><height>1080</height><durationinseconds>8160</durationinseconds></video>
    <audio><codec>DTS</codec></audio>
  </streamdetails></fileinfo>
</movie>`)

	meta, err := parseMovieNFO(data)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "The Matrix" || meta.Year != 1999 || meta.Released != "1999-03-31" {
		t.Errorf("identity = %q %d %q", meta.Title, meta.Year, meta.Released)
	}
	if meta.ProviderIDs["tmdb"] != "603" || meta.ProviderIDs["imdb"] != "tt0133093" {
		t.Errorf("providers = %v", meta.ProviderIDs)
	}
	if meta.Body.Collection != "The Matrix Collection" {
		t.Errorf("collection = %q", meta.Body.Collection)
	}
	if meta.Body.ContentRating != "R" || meta.Body.Country != "United States" {
		t.Errorf("rating/country = %q %q", meta.Body.ContentRating, meta.Body.Country)
	}
	if !reflect.DeepEqual(meta.Body.Writers, []string{"Lilly Wachowski"}) {
		t.Errorf("writers = %v, want one deduplicated name", meta.Body.Writers)
	}
	if !reflect.DeepEqual(meta.Body.Cast, []castMember{{Name: "Keanu Reeves", Role: "Neo"}}) {
		t.Errorf("cast = %v, want the one named actor", meta.Body.Cast)
	}
	if meta.Duration != 8160 {
		t.Errorf("duration = %d, want the stream's 8160 seconds", meta.Duration)
	}
	wantStream := streamInfo{Width: 1920, Height: 1080, VideoCodec: "h264", AudioCodec: "dts", DurationMs: 8160000}
	if meta.Stream != wantStream {
		t.Errorf("stream = %+v, want %+v", meta.Stream, wantStream)
	}
}

func TestParseMovieNFOFallsBackToRuntimeAndYear(t *testing.T) {
	meta, err := parseMovieNFO([]byte(`<movie><title>Old Film</title><year>1975</year><runtime>90</runtime><set>Loose Set</set><certification>PG</certification></movie>`))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Released != "1975" {
		t.Errorf("released = %q, want the year when there is no premiered date", meta.Released)
	}
	if meta.Duration != 5400 {
		t.Errorf("duration = %d, want runtime minutes as seconds", meta.Duration)
	}
	if meta.Body.Collection != "Loose Set" {
		t.Errorf("collection = %q, want the bare set text", meta.Body.Collection)
	}
	if meta.Body.ContentRating != "PG" {
		t.Errorf("rating = %q, want the certification fallback", meta.Body.ContentRating)
	}
	if meta.Stream.present() {
		t.Errorf("stream = %+v, want none without streamdetails", meta.Stream)
	}
}

func TestParseSeriesNFO(t *testing.T) {
	meta, err := parseSeriesNFO([]byte(`<tvshow><title>Breaking Bad</title><year>2008</year><creator>Vince Gilligan</creator><genre>Drama</genre><uniqueid type="tvdb">81189</uniqueid><uniqueid type="tmdb">1396</uniqueid></tvshow>`))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Breaking Bad" || meta.Year != 2008 {
		t.Errorf("identity = %q %d", meta.Title, meta.Year)
	}
	if !reflect.DeepEqual(meta.Body.Creators, []string{"Vince Gilligan"}) {
		t.Errorf("creators = %v", meta.Body.Creators)
	}
	if meta.ProviderIDs["tvdb"] != "81189" || meta.ProviderIDs["tmdb"] != "1396" {
		t.Errorf("providers = %v", meta.ProviderIDs)
	}
}

func TestParseEpisodeNFO(t *testing.T) {
	metas, err := parseEpisodeNFOs([]byte(`<episodedetails><title>Breakage</title><season>2</season><episode>5</episode><aired>2009-04-05</aired><credits>Moira Walley-Beckett</credits><uniqueid type="tvdb">340124</uniqueid><fileinfo><streamdetails><video><codec>h264</codec><width>1280</width><height>720</height></video></streamdetails></fileinfo></episodedetails>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 {
		t.Fatalf("blocks = %d, want the one the sidecar holds", len(metas))
	}
	meta := metas[0]
	if meta.Season != 2 || meta.Episode != 5 || meta.Title != "Breakage" {
		t.Errorf("placement = s%02de%02d %q", meta.Season, meta.Episode, meta.Title)
	}
	if meta.Released != "2009-04-05" {
		t.Errorf("released = %q, want the aired date", meta.Released)
	}
	if !reflect.DeepEqual(meta.Body.Writers, []string{"Moira Walley-Beckett"}) {
		t.Errorf("writers = %v, want the credits fallback", meta.Body.Writers)
	}
	if meta.ProviderIDs["tvdb"] != "340124" {
		t.Errorf("providers = %v", meta.ProviderIDs)
	}
	if meta.Stream.Width != 1280 || meta.Stream.Height != 720 {
		t.Errorf("stream = %+v, want the episode resolution", meta.Stream)
	}
}

func TestParseEpisodeNFOFallsBackToPremiered(t *testing.T) {
	metas, err := parseEpisodeNFOs([]byte(`<episodedetails><title>Pilot</title><season>1</season><episode>1</episode><premiered>2008-01-20</premiered></episodedetails>`))
	if err != nil {
		t.Fatal(err)
	}
	if metas[0].Released != "2008-01-20" {
		t.Errorf("released = %q, want premiered when aired is absent", metas[0].Released)
	}
}

// A sidecar beside a file of two episodes holds one episodedetails block per
// episode, and each block carries its own title and plot.
func TestParseEpisodeNFOsReadsEveryBlock(t *testing.T) {
	metas, err := parseEpisodeNFOs([]byte(`<?xml version="1.0" encoding="utf-8"?>
<episodedetails><title>The Long Way Down</title><season>4</season><episode>10</episode><plot>The crew descends.</plot></episodedetails>
<episodedetails><title>The Long Way Back</title><season>4</season><episode>11</episode><plot>The crew climbs.</plot></episodedetails>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("blocks = %d, want both episodes the sidecar names", len(metas))
	}
	if metas[0].Title != "The Long Way Down" || metas[0].Episode != 10 {
		t.Errorf("first block = %q e%02d", metas[0].Title, metas[0].Episode)
	}
	if metas[1].Title != "The Long Way Back" || metas[1].Episode != 11 {
		t.Errorf("second block = %q e%02d", metas[1].Title, metas[1].Episode)
	}
	if metas[1].Body.Plot != "The crew climbs." {
		t.Errorf("second plot = %q, want the second block's own plot", metas[1].Body.Plot)
	}
}

// A sidecar whose second block is unreadable keeps the blocks that read, so one
// broken block does not lose the episode before it.
func TestParseEpisodeNFOsKeepsTheBlocksItRead(t *testing.T) {
	metas, err := parseEpisodeNFOs([]byte(`<episodedetails><title>First</title><season>1</season><episode>1</episode></episodedetails><episodedetails`))
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Title != "First" {
		t.Errorf("blocks = %+v, want the one that read", metas)
	}
}

func TestParseNFORejectsBadXML(t *testing.T) {
	if _, err := parseMovieNFO([]byte(`<movie><title>`)); err == nil {
		t.Error("parseMovieNFO accepted truncated XML")
	}
	if _, err := parseSeriesNFO([]byte(`<tvshow`)); err == nil {
		t.Error("parseSeriesNFO accepted truncated XML")
	}
	if _, err := parseEpisodeNFOs([]byte(`<episodedetails`)); err == nil {
		t.Error("parseEpisodeNFOs accepted truncated XML")
	}
}

func TestCollectProviders(t *testing.T) {
	cases := []struct {
		name string
		uids []nfoUniqueID
		imdb string
		tmdb string
		tvdb string
		id   string
		want map[string]string
	}{
		{
			name: "uniqueid wins over convenience tag",
			uids: []nfoUniqueID{{Type: "TMDB", Value: "603"}},
			tmdb: "999",
			want: map[string]string{"tmdb": "603"},
		},
		{
			name: "convenience tags fill missing providers",
			imdb: "tt1", tmdb: "2", tvdb: "3",
			want: map[string]string{"imdb": "tt1", "tmdb": "2", "tvdb": "3"},
		},
		{
			name: "a bare tt id fills imdb",
			id:   "tt0133093",
			want: map[string]string{"imdb": "tt0133093"},
		},
		{
			name: "no ids is nil",
			want: nil,
		},
		{
			name: "empty values are dropped",
			uids: []nfoUniqueID{{Type: "tmdb", Value: "  "}},
			want: nil,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := collectProviders(testCase.uids, testCase.imdb, testCase.tmdb, testCase.tvdb, testCase.id)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("collectProviders = %v, want %v", got, testCase.want)
			}
		})
	}
}

// A movie with no year takes it off the premiered date's leading digits.
func TestParseMovieNFODerivesYearFromPremiered(t *testing.T) {
	meta, err := parseMovieNFO([]byte(`<movie><title>Dated</title><premiered>2001-05-05</premiered></movie>`))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Year != 2001 {
		t.Errorf("year = %d, want 2001 from the premiered date", meta.Year)
	}
}

// The set element comes in three shapes: the collection id Jellyfin writes
// on it, the nested name Kodi writes, and the bare name an older sidecar
// carries. The id scopes the set where there is one, and the name is the
// set's title in every shape.
func TestParseMovieNFOReadsTheSet(t *testing.T) {
	cases := []struct {
		name           string
		element        string
		wantID         string
		wantCollection string
	}{
		{
			name:           "a collection id scopes the set",
			element:        `<set tmdbcolid="1570"><name>Quiet Harbor Collection</name><overview>A harbor town.</overview></set>`,
			wantID:         "set:tmdb:1570",
			wantCollection: "Quiet Harbor Collection",
		},
		{
			name:           "a nested name with no id falls back to the slug",
			element:        `<set><name>Quiet Harbor Collection</name></set>`,
			wantID:         "set:name:quiet-harbor-collection",
			wantCollection: "Quiet Harbor Collection",
		},
		{
			name:           "a bare name reads the same way",
			element:        `<set>Quiet Harbor Collection</set>`,
			wantID:         "set:name:quiet-harbor-collection",
			wantCollection: "Quiet Harbor Collection",
		},
		{
			name:    "a sidecar with no set names none",
			element: "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			meta, err := parseMovieNFO([]byte(`<movie><title>Quiet Harbor</title><year>1998</year>` + testCase.element + `</movie>`))
			if err != nil {
				t.Fatal(err)
			}
			if meta.SetID != testCase.wantID {
				t.Errorf("setID = %q, want %q", meta.SetID, testCase.wantID)
			}
			if meta.Body.Collection != testCase.wantCollection {
				t.Errorf("collection = %q, want %q", meta.Body.Collection, testCase.wantCollection)
			}
		})
	}
}
