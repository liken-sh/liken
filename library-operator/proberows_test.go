package main

// The probe writes the probed column of each file it read, so the file
// leaves the probe gap at once and no walk has to read the ledger first.

import (
	"path/filepath"
	"testing"
)

func TestTheProbeWritesTheProbedColumn(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err != nil {
		t.Fatal(err)
	}

	matched, err := catalog.queryInt(t.Context(),
		`SELECT count(*) FROM files WHERE library = ? AND path = ? AND probed = modified AND probed != 0`,
		[]any{"house/movies", filepath.Join("The Thing (1982)", "The Thing (1982).mkv")})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Errorf("files whose probed column is their modified stamp = %d, want 1", matched)
	}
}
