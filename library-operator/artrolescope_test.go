package main

// What these tests read: the counts the art line gives for a Job that works
// some folders of a library, so a Job that wrote nothing because its folders
// had no gap reads apart from one that wrote nothing where it had work.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

// Three films, each with a TMDb id and no art beside it, whose ids run from
// 1001 in the order of the folders.
var scopedArtFolders = []string{"Some Film (2014)", "Another Film (2015)", "Third Film (2016)"}

func seedScopedArtMovies(t *testing.T, catalog *Catalog, root string) map[string]string {
	t.Helper()
	seed := &walkResult{}
	answers := map[string]string{tmdbKey("/t/p/w780/scoped.jpg", "", ""): testImage}
	for index, folder := range scopedArtFolders {
		id := fmt.Sprintf("movie:tmdb:%d", 1001+index)
		writeFile(t, filepath.Join(root, folder, folder+".mkv"), "video")
		seed.movies = append(seed.movies, movieRow{Id: id, Library: artLibrary, Kind: libraryKindMovies,
			Path: folder, Title: folder})
		seed.aliases = append(seed.aliases, aliasRow{Alias: id, Library: artLibrary, Item: id, Source: aliasSourceProvider})
		answers[tmdbKey(fmt.Sprintf("/3/movie/%d/images", 1001+index), "", "")] =
			imagesAnswer(tmdbPosters, "/scoped.jpg", artLanguage)
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	return answers
}

func TestTheArtLineCountsTheJobsFoldersAndTheRestApart(t *testing.T) {
	cases := []struct {
		name      string
		scanPaths []string
		kept      []string
		want      string
	}{
		{
			name: "a Job over the whole library",
			want: "wrote 3 of the 3 poster files the library had none of\n",
		},
		{
			name:      "a Job over one film's video",
			scanPaths: []string{"/data/media/Some Film (2014)/Some Film (2014).mkv"},
			want:      "wrote 1 of the 1 poster files the library had none of, and left 2 outside this Job's folders\n",
		},
		{
			name:      "a Job over a film whose poster is already there",
			scanPaths: []string{"/data/media/Some Film (2014)/Some Film (2014).mkv"},
			kept:      []string{filepath.Join("Some Film (2014)", "poster.jpg")},
			want:      "wrote 0 of the 1 poster files the library had none of, and left 2 outside this Job's folders\n",
		},
		{
			name:      "a Job over two films",
			scanPaths: []string{"Some Film (2014)", "Third Film (2016)"},
			want:      "wrote 2 of the 2 poster files the library had none of, and left 1 outside this Job's folders\n",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				catalog, _ := newSQLiteCatalog(t)
				root := t.TempDir()
				answers := seedScopedArtMovies(t, catalog, root)
				for _, kept := range test.kept {
					writeFile(t, filepath.Join(root, kept), "the poster a person kept")
				}
				work, log := testEnricher(t, libraryKindMovies, root, catalog)
				work.scanPaths = test.scanPaths
				work.scopes = work.narrowedScopes()
				client, _ := newArtTMDb(t, answers)

				if err := work.artGap(t.Context(), factPoster, tmdbArtLine(client)); err != nil {
					t.Fatal(err)
				}

				if !strings.Contains(log.String(), "library.liken.sh: "+test.want) {
					t.Errorf("log = %q, want the line %q", log.String(), test.want)
				}
			})
		})
	}
}
