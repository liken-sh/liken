package main

// The tests build a media folder under t.TempDir(), with the NFO and art
// files each case names, and check the block presentationBlock builds for it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("wanted no error, got %v", err)
	}
}

func mustFail(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("wanted an error, got none")
	}
}

func mustMatch[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// fixture writes a media folder under t.TempDir(). Each key is a name in the
// folder, and a name that ends with a slash becomes a directory.
func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if strings.HasSuffix(name, "/") {
			mustSucceed(t, os.MkdirAll(path, 0o755))
			continue
		}
		mustSucceed(t, os.WriteFile(path, []byte(content), 0o644))
	}
	return dir
}

func blockJSON(t *testing.T, media string) string {
	t.Helper()
	made, err := presentationBlock(media)
	mustSucceed(t, err)
	encoded, err := json.Marshal(made)
	mustSucceed(t, err)
	return string(encoded)
}

const episodeNFO = `<episodedetails>
  <title>Pilot</title>
  <showtitle>The Show</showtitle>
  <season>1</season>
  <episode>2</episode>
  <aired>2001-01-05</aired>
</episodedetails>`

const movieNFO = `<movie>
  <title>The Film</title>
  <year>1999</year>
</movie>`

func TestPresentationBlock(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		media string
		want  string
	}{
		{
			name:  "an NFO named for the episode is a series",
			files: map[string]string{"S01E02.mkv": "", "S01E02.nfo": episodeNFO},
			media: "S01E02.mkv",
			want:  `{"type":"video","hint":"series","series":"The Show","season":1,"episode":2,"episodeTitle":"Pilot","date":"2001-01-05"}`,
		},
		{
			name:  "a sibling movie.nfo is a movie",
			files: map[string]string{"film.mkv": "", "movie.nfo": movieNFO},
			media: "film.mkv",
			want:  `{"type":"video","hint":"movie","title":"The Film","year":1999}`,
		},
		{
			name:  "the episode NFO wins over movie.nfo",
			files: map[string]string{"S01E02.mkv": "", "S01E02.nfo": episodeNFO, "movie.nfo": movieNFO},
			media: "S01E02.mkv",
			want:  `{"type":"video","hint":"series","series":"The Show","season":1,"episode":2,"episodeTitle":"Pilot","date":"2001-01-05"}`,
		},
		{
			name:  "an NFO without a season is a movie",
			files: map[string]string{"film.mkv": "", "film.nfo": movieNFO},
			media: "film.mkv",
			want:  `{"type":"video","hint":"movie","title":"The Film","year":1999}`,
		},
		{
			name:  "movie.nfo with a season is still a movie",
			files: map[string]string{"film.mkv": "", "movie.nfo": `<movie><title>The Film</title><season>1</season></movie>`},
			media: "film.mkv",
			want:  `{"type":"video","hint":"movie","title":"The Film"}`,
		},
		{
			name:  "the year comes from premiered",
			files: map[string]string{"film.mkv": "", "movie.nfo": `<movie><title>The Film</title><premiered>1999-03-31</premiered></movie>`},
			media: "film.mkv",
			want:  `{"type":"video","hint":"movie","title":"The Film","year":1999}`,
		},
		{
			name:  "year wins over premiered",
			files: map[string]string{"film.mkv": "", "movie.nfo": `<movie><year>1999</year><premiered>2001-03-31</premiered></movie>`},
			media: "film.mkv",
			want:  `{"type":"video","hint":"movie","year":1999}`,
		},
		{
			name:  "empty elements are absent",
			files: map[string]string{"film.mkv": "", "movie.nfo": "<movie><title>   </title><year></year></movie>"},
			media: "film.mkv",
			want:  `{"type":"video","hint":"movie"}`,
		},
		{
			name:  "a season zero is kept",
			files: map[string]string{"S00E01.mkv": "", "S00E01.nfo": `<episodedetails><season>0</season><episode>1</episode></episodedetails>`},
			media: "S00E01.mkv",
			want:  `{"type":"video","hint":"series","season":0,"episode":1}`,
		},
		{
			name:  "no NFO leaves the block empty",
			files: map[string]string{"film.mkv": ""},
			media: "film.mkv",
			want:  `{}`,
		},
		{
			name:  "the logo named for the media wins",
			files: map[string]string{"film.mkv": "", "film-clearlogo.png": "", "film-logo.png": "", "clearlogo.png": "", "logo.png": ""},
			media: "film.mkv",
			want:  `{"logo":"{dir}/film-clearlogo.png"}`,
		},
		{
			name:  "the named logo wins over the folder art",
			files: map[string]string{"film.mkv": "", "film-logo.png": "", "clearlogo.png": "", "logo.png": ""},
			media: "film.mkv",
			want:  `{"logo":"{dir}/film-logo.png"}`,
		},
		{
			name:  "the folder clearlogo wins over the folder logo",
			files: map[string]string{"film.mkv": "", "clearlogo.png": "", "logo.png": ""},
			media: "film.mkv",
			want:  `{"logo":"{dir}/clearlogo.png"}`,
		},
		{
			name:  "the folder logo is the last candidate",
			files: map[string]string{"film.mkv": "", "logo.png": ""},
			media: "film.mkv",
			want:  `{"logo":"{dir}/logo.png"}`,
		},
		{
			name:  "a trickplay directory joins the block",
			files: map[string]string{"film.mkv": "", "film.trickplay/": ""},
			media: "film.mkv",
			want:  `{"trickplay":"{dir}/film.trickplay"}`,
		},
		{
			name:  "a trickplay file is not a directory",
			files: map[string]string{"film.mkv": "", "film.trickplay": ""},
			media: "film.mkv",
			want:  `{}`,
		},
		{
			name:  "the art needs no NFO",
			files: map[string]string{"film.mkv": "", "logo.png": "", "film.trickplay/": ""},
			media: "film.mkv",
			want:  `{"logo":"{dir}/logo.png","trickplay":"{dir}/film.trickplay"}`,
		},
		{
			name:  "the NFO and the art travel together",
			files: map[string]string{"film.mkv": "", "movie.nfo": movieNFO, "film-clearlogo.png": "", "film.trickplay/": ""},
			media: "film.mkv",
			want:  `{"type":"video","hint":"movie","title":"The Film","year":1999,"logo":"{dir}/film-clearlogo.png","trickplay":"{dir}/film.trickplay"}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := fixture(t, testCase.files)
			got := blockJSON(t, filepath.Join(dir, testCase.media))
			mustMatch(t, got, strings.ReplaceAll(testCase.want, "{dir}", dir))
		})
	}
}

func TestNonNumericSeasonFails(t *testing.T) {
	dir := fixture(t, map[string]string{
		"S01E02.mkv": "",
		"S01E02.nfo": `<episodedetails><season>many</season></episodedetails>`,
	})
	_, err := presentationBlock(filepath.Join(dir, "S01E02.mkv"))
	mustFail(t, err)
}

func TestBrokenXMLFails(t *testing.T) {
	dir := fixture(t, map[string]string{
		"film.mkv":  "",
		"movie.nfo": "<movie><title>The Film</movie>",
	})
	_, err := presentationBlock(filepath.Join(dir, "film.mkv"))
	mustFail(t, err)
}
