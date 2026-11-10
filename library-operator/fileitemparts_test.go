package main

// What these tests read: the place of each episode in a file of several
// episodes, as the shipped schema holds it on the link from the file to
// the episode.

import (
	"context"
	"testing"
)

// A link that no scan has written since the columns were added reads as
// a file of one work, so the two columns default to 0 and refuse a null.
func TestTheFileItemsLinkHoldsThePartAndThePartsWithTheirDefaults(t *testing.T) {
	_, agent := newSQLiteCatalog(t)
	if _, err := agent.db.Exec(`INSERT INTO file_items (library, path, item) VALUES ('house/series', 'a.mkv', 'episode:a')`); err != nil {
		t.Fatal(err)
	}

	var part, parts int
	if err := agent.db.QueryRow(`SELECT part, parts FROM file_items`).Scan(&part, &parts); err != nil {
		t.Fatal(err)
	}
	if part != 0 || parts != 0 {
		t.Errorf("part, parts = %d, %d, want 0, 0 for a link that names neither", part, parts)
	}
	if _, err := agent.db.Exec(`UPDATE file_items SET part = NULL`); err == nil {
		t.Error("the schema took a null part, want the column to refuse one")
	}
}

// The link of the second episode of a double, as the scanner writes it.
func doubleLink(part, parts int) []fileRow {
	return []fileRow{{Library: "house/series", Path: "Coastline - S04E10-E11.mkv",
		Items: []fileItem{{Item: "episode:tvdb:7:s04e11", Part: part, Parts: parts}}}}
}

// Every walk writes every link, so a link written again with the parts it
// already holds changes no row, and the agent broadcasts nothing. A link
// written with other parts, such as the first walk after the columns were
// added, takes the new parts.
func TestUpsertFileItemsChangesALinkOnlyWhenItsPartsDiffer(t *testing.T) {
	cases := []struct {
		name      string
		before    []fileRow
		after     []fileRow
		changed   int
		wantPart  int
		wantParts int
	}{
		{name: "a new link", after: doubleLink(2, 2), changed: 1, wantPart: 2, wantParts: 2},
		{name: "the same parts again", before: doubleLink(2, 2), after: doubleLink(2, 2), changed: 0, wantPart: 2, wantParts: 2},
		{name: "a link from before the columns", before: doubleLink(0, 0), after: doubleLink(2, 2), changed: 1, wantPart: 2, wantParts: 2},
		{name: "another part", before: doubleLink(2, 2), after: doubleLink(2, 3), changed: 1, wantPart: 2, wantParts: 3},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			if _, err := catalog.UpsertFileItems(context.Background(), testCase.before); err != nil {
				t.Fatal(err)
			}

			changed, err := catalog.UpsertFileItems(context.Background(), testCase.after)
			if err != nil {
				t.Fatal(err)
			}
			if changed != testCase.changed {
				t.Errorf("changed rows = %d, want %d", changed, testCase.changed)
			}
			var part, parts int
			if err := agent.db.QueryRow(`SELECT part, parts FROM file_items`).Scan(&part, &parts); err != nil {
				t.Fatal(err)
			}
			if part != testCase.wantPart || parts != testCase.wantParts {
				t.Errorf("part, parts = %d, %d, want %d, %d", part, parts, testCase.wantPart, testCase.wantParts)
			}
		})
	}
}
