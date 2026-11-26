package main

// A walk Job whose webhook named a video file: the phases work the title
// folder that holds the video, so the art the folder lacks lands in the body
// the close container posts.

import (
	"path/filepath"
	"testing"
	"testing/synctest"
)

func TestAWalkOfAVideoWritesTheArtOfItsTitleAndPostsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newChangeWorld(t, libraryKindMovies)
		folder := "The Long Survey (1999)"
		writeFile(t, filepath.Join(w.scan.root, folder, "webdl.mkv"), "video")
		writeFile(t, filepath.Join(w.scan.root, folder, movieNFOName),
			"<movie><title>The Long Survey</title><uniqueid type=\"tmdb\" default=\"true\">1001</uniqueid></movie>\n")
		w.title(survey)
		w.exec(`INSERT INTO aliases (library, alias, item, source) VALUES ('house/movies', ?, ?, ?)`,
			survey, survey, aliasSourceProvider)
		w.video(filepath.Join(folder, "bluray.mkv"), 700, survey)
		scanPaths := []string{"/data/media/movies/" + folder + "/webdl.mkv"}
		w.notePaths(scanPaths...)
		w.remove(filepath.Join(folder, "bluray.mkv"))
		w.video(filepath.Join(folder, "webdl.mkv"), 800, survey)
		art, _ := testEnricher(t, libraryKindMovies, w.scan.root, w.catalog)
		art.scanPaths = scanPaths
		art.scopes = art.narrowedScopes()
		client, _ := newArtTMDb(t, map[string]string{
			tmdbKey("/3/movie/1001/images", "", ""): imagesAnswer(tmdbPosters, "/survey.jpg", artLanguage),
			tmdbKey("/t/p/w780/survey.jpg", "", ""): testImage,
		})

		if err := art.artGap(t.Context(), factPoster, tmdbArtLine(client)); err != nil {
			t.Fatal(err)
		}

		poster := filepath.Join(folder, "poster.jpg")
		if got := readFileString(t, filepath.Join(w.scan.root, poster)); got != testImage {
			t.Errorf("%s holds %d bytes, want the image", poster, len(got))
		}
		titles := w.titles()
		if len(titles) != 1 || titles[0].Row["art"] != poster {
			t.Errorf("titles = %v, want the one title with its poster as the art", titles)
		}
	})
}
