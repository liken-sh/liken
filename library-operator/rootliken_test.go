package main

import (
	"path/filepath"
	"testing"
)

// The library root holds no title: neither the walk nor a rescan reads it as
// one, so no row and no ledger belong to it.

// A video loose at the root, a root trailers folder, and a title folder: the
// walk catalogs the title folder alone, and no file of the root.
func TestTheWalkCatalogsNoVideoAtTheRoot(t *testing.T) {
	cases := []struct {
		kind string
		walk func(root, library string, ignore ignoreSet) *walkResult
	}{
		{kind: libraryKindMovies, walk: walkMovies},
		{kind: libraryKindSeries, walk: walkSeries},
	}
	for _, one := range cases {
		t.Run(one.kind, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "Loose Film (1999).mkv"), "video")
			writeFile(t, filepath.Join(root, "trailers", "Loose Film Trailer.mp4"), "trailer")
			writeFile(t, filepath.Join(root, "Harbour Lights (1972)", "Season 01", "Harbour Lights - S01E01.mkv"), "video")

			result := one.walk(root, "house/library", nil)

			for _, file := range result.files {
				if filepath.Dir(file.Path) == "." || filepath.Dir(file.Path) == "trailers" {
					t.Errorf("the walk catalogs %s, a file of the root", file.Path)
				}
			}
			if len(result.files) == 0 {
				t.Error("the walk catalogs nothing, want the title folder's video")
			}
		})
	}
}

// A rescan of the root itself reads no title, so it writes no row.
func TestARescanOfTheRootReadsNoTitle(t *testing.T) {
	for _, kind := range []string{libraryKindMovies, libraryKindSeries} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "Loose Film (1999).mkv"), "video")
			writeFile(t, filepath.Join(root, "movie.nfo"), "<movie><title>Loose Film</title><year>1999</year></movie>")

			if result := readFolder(folderScan{root: root, library: "house/library", kind: kind}, root); result != nil {
				t.Errorf("the root reads as %+v, want no title", result)
			}
		})
	}
}
