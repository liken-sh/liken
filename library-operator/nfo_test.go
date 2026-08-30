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
	meta, err := parseEpisodeNFO([]byte(`<episodedetails><title>Breakage</title><season>2</season><episode>5</episode><aired>2009-04-05</aired><credits>Moira Walley-Beckett</credits><uniqueid type="tvdb">340124</uniqueid><fileinfo><streamdetails><video><codec>h264</codec><width>1280</width><height>720</height></video></streamdetails></fileinfo></episodedetails>`))
	if err != nil {
		t.Fatal(err)
	}
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
	meta, err := parseEpisodeNFO([]byte(`<episodedetails><title>Pilot</title><season>1</season><episode>1</episode><premiered>2008-01-20</premiered></episodedetails>`))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Released != "2008-01-20" {
		t.Errorf("released = %q, want premiered when aired is absent", meta.Released)
	}
}

func TestParseNFORejectsBadXML(t *testing.T) {
	if _, err := parseMovieNFO([]byte(`<movie><title>`)); err == nil {
		t.Error("parseMovieNFO accepted truncated XML")
	}
	if _, err := parseSeriesNFO([]byte(`<tvshow`)); err == nil {
		t.Error("parseSeriesNFO accepted truncated XML")
	}
	if _, err := parseEpisodeNFO([]byte(`<episodedetails`)); err == nil {
		t.Error("parseEpisodeNFO accepted truncated XML")
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
