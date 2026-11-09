package main

// appearancesfacelessrows_test.go proves that the walk copies the faceless
// end of a video from the appearances ledger into the video's file row, and
// that an answer written before the worker read the faceless end opens the
// video to the worker again.

import (
	"slices"
	"testing"
)

// The file row of one path that a folder read wrote.
func fileRowOf(t *testing.T, result *walkResult, path string) fileRow {
	t.Helper()
	at := slices.IndexFunc(result.files, func(row fileRow) bool { return row.Path == path })
	if at < 0 {
		t.Fatalf("no file row at %s", path)
	}
	return result.files[at]
}

func TestTheWalkReadsTheFacelessEndFromTheAppearancesLedger(t *testing.T) {
	encode := int64(len("the first encode"))
	cases := []struct {
		name     string
		entry    appearancesEntry
		faceless int64
		lifted   bool
	}{
		{
			name:     "a faceless end",
			entry:    appearancesEntry{Size: encode, Faceless: &timeSpan{Start: 1000000, End: 1400000}},
			faceless: 1000000, lifted: true,
		},
		{
			name:  "a record that ends on a face",
			entry: appearancesEntry{Size: encode, Faceless: &timeSpan{Start: 1400000, End: 1400000}},
			// An empty run is no faceless end.
			faceless: 0, lifted: true,
		},
		{
			// The size is the evidence that the answer came from this file.
			name:     "an answer of another file",
			entry:    appearancesEntry{Size: encode + 1, Faceless: &timeSpan{Start: 1000000, End: 1400000}},
			faceless: 0, lifted: true,
		},
		{
			// The answer predates the read of the faceless end, so the
			// found attempt no longer closes the gap, and the worker reads
			// the record on the volume again.
			name:     "an answer with no faceless end",
			entry:    appearancesEntry{Size: encode},
			faceless: 0, lifted: false,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			episode := seedEnrichedEpisode(t)
			test.entry.Path = replacedEntry
			writeFactLedger(t, episode.season, factAppearances, likenLedger{
				Appearances: []appearancesEntry{test.entry},
				Attempts:    []likenAttempt{{Path: replacedEntry, At: episode.made, Result: attemptFound}}})

			result := readFolder(episode.scan(), episode.series)

			if got := fileRowOf(t, result, episode.path).FacelessTailMs; got != test.faceless {
				t.Errorf("faceless = %d, want %d", got, test.faceless)
			}
			if got := slices.Contains(liftedFacts(result, episode.path), factAppearances); got != test.lifted {
				t.Errorf("appearances lifted = %v, want %v", got, test.lifted)
			}
		})
	}
}
