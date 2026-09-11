package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The map earlier releases of the trickplay fact wrote beside the sheets, and
// where the fact removes it.

// One movie whose video already carries a trickplay directory with one sheet
// in it: what the sweep reads and what the gap leaves out.
func seedTiledVideo(t *testing.T, catalog *Catalog, root string) string {
	t.Helper()
	writeFile(t, filepath.Join(root, trickplayFolder, trickplayFile), "video")
	writeFile(t, filepath.Join(trickplayTilesUnder(root), "0.jpg"), "sheet")
	seed := &walkResult{
		movies: []movieRow{{Id: "movie:path:x", Library: trickplayLibrary, Kind: libraryKindMovies,
			Path: trickplayFolder, Title: trickplayFolder}},
		files: []fileRow{{Path: filepath.Join(trickplayFolder, trickplayFile), Library: trickplayLibrary,
			Present: true, Type: fileTypeVideo, DurationMs: 100000, VideoCodec: "h264",
			Items: []string{"movie:path:x"}, Trickplay: filepath.Join(trickplayFolder,
				strings.TrimSuffix(trickplayFile, ".mkv")+trickplayExtension)}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	return trickplayTilesUnder(root)
}

// A video an earlier run tiled keeps its sheets and loses the map.
func TestTheTrickplayFactRemovesTheMapAnEarlierRunLeft(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	tiles := seedTiledVideo(t, catalog, root)
	writeFile(t, filepath.Join(tiles, trickplayMapName), "WEBVTT\n")
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if left := namesIn(t, tiles); !slices.Equal(left, []string{"0.jpg"}) {
		t.Errorf("the tiles folder holds %v, want the sheets alone", left)
	}
	if !strings.Contains(log.String(), "removed the trickplay map of 1 of the 1 files") {
		t.Errorf("log = %q, want the count of the maps it removed", log)
	}
}

// A tiled video with no map is left as it is.
func TestATiledVideoWithNoMapIsLeftAsItIs(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	tiles := seedTiledVideo(t, catalog, root)
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if left := namesIn(t, tiles); !slices.Equal(left, []string{"0.jpg"}) {
		t.Errorf("the tiles folder holds %v, want the sheet it already held", left)
	}
	if !strings.Contains(log.String(), "removed the trickplay map of 0 of the 1 files") {
		t.Errorf("log = %q, want the count of the maps it removed", log)
	}
}

// A map the volume will not take back is logged and left, and the run carries on.
func TestAMapTheVolumeWillNotRemoveIsLogged(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	tiles := seedTiledVideo(t, catalog, root)
	writeFile(t, filepath.Join(tiles, trickplayMapName), "WEBVTT\n")
	if err := os.Chmod(tiles, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(tiles, 0o755) })
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if !fileExistsInTest(t, filepath.Join(tiles, trickplayMapName)) {
		t.Error("the map is gone from a folder that takes no remove")
	}
	if !strings.Contains(log.String(), "could not remove") {
		t.Errorf("log = %q, want the line that names the remove it could not make", log)
	}
}

// A directory that landed since the last walk is answered with no decode, and
// the map an earlier run left in it goes with the answer.
func TestADirectoryThatLandedSinceTheWalkLosesItsMap(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrickplayGap(t, catalog, root, 100*time.Second)
	tiles := trickplayTilesUnder(root)
	writeFile(t, filepath.Join(tiles, "0.jpg"), "sheet")
	writeFile(t, filepath.Join(tiles, trickplayMapName), "WEBVTT\n")
	standInFFmpeg(t, -1)
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if left := namesIn(t, tiles); !slices.Equal(left, []string{"0.jpg"}) {
		t.Errorf("the tiles folder holds %v, want the sheets alone", left)
	}
	if !strings.Contains(log.String(), "removed the trickplay map beside the sheets of") {
		t.Errorf("log = %q, want the line that names the map it removed", log)
	}
}
