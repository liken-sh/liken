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
// where the worker removes it: in a directory it finds already there, which
// it answers with no decode.

// A directory that landed since the last walk is answered with no decode, and
// the map an earlier run left in it goes with the answer.
func TestADirectoryThatLandedSinceTheWalkLosesItsMap(t *testing.T) {
	root := t.TempDir()
	item := seedTrickplayItem(t, root, 100*time.Second)
	tiles := trickplayTilesUnder(root)
	writeFile(t, filepath.Join(tiles, "0.jpg"), "sheet")
	writeFile(t, filepath.Join(tiles, trickplayMapName), "WEBVTT\n")
	standInFFmpeg(t, -1)
	work, log := testFactWorker(t, trickplayWorker, libraryKindMovies, root)

	work.trickplayOne(t.Context(), item)

	if left := namesIn(t, tiles); !slices.Equal(left, []string{"0.jpg"}) {
		t.Errorf("the tiles folder holds %v, want the sheets alone", left)
	}
	if !strings.Contains(log.String(), "removed the trickplay map beside the sheets of") {
		t.Errorf("log = %q, want the line that names the map it removed", log)
	}
}

// A map the volume will not take back is logged and left, and the tiles are
// still the answer.
func TestAMapTheVolumeWillNotRemoveIsLogged(t *testing.T) {
	root := t.TempDir()
	item := seedTrickplayItem(t, root, 100*time.Second)
	tiles := trickplayTilesUnder(root)
	writeFile(t, filepath.Join(tiles, "0.jpg"), "sheet")
	writeFile(t, filepath.Join(tiles, trickplayMapName), "WEBVTT\n")
	if err := os.Chmod(tiles, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(tiles, 0o755) })
	work, log := testFactWorker(t, trickplayWorker, libraryKindMovies, root)

	work.trickplayOne(t.Context(), item)

	if !fileExistsInTest(t, filepath.Join(tiles, trickplayMapName)) {
		t.Error("the map is gone from a folder that takes no remove")
	}
	if !strings.Contains(log.String(), "could not remove") {
		t.Errorf("log = %q, want the line that names the remove it could not make", log)
	}
	ledger, err := readLikenLedger(filepath.Join(root, trickplayFolder), factTrickplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("attempts = %+v, want the tiles recorded as the answer", ledger.Attempts)
	}
}
