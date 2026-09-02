package main

// The sets a movies library derives: the id a sidecar's set element
// yields, the fold that picks a set's earliest member, and the walk and the
// rescan that write and prune the rows.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSetID(t *testing.T) {
	cases := []struct {
		name         string
		collectionID string
		set          string
		want         string
	}{
		{name: "the collection id scopes the set", collectionID: "1570", set: "Quiet Harbor Collection", want: "set:tmdb:1570"},
		{name: "the collection id wins over the name", collectionID: "1570", set: "Renamed Collection", want: "set:tmdb:1570"},
		{name: "a named set falls back to its slug", set: "Quiet Harbor Collection", want: "set:name:quiet-harbor-collection"},
		{name: "an accented name folds to ascii", set: "Amélie Collection", want: "set:name:amelie-collection"},
		{name: "no set at all is no id", want: ""},
		{name: "a name of punctuation alone is no id", set: "***", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := setID(testCase.collectionID, testCase.set); got != testCase.want {
				t.Errorf("setID(%q, %q) = %q, want %q", testCase.collectionID, testCase.set, got, testCase.want)
			}
		})
	}
}

// memberOf is one movie row that names a set, the input the fold reads.
func memberOf(id, set, name, released, art string, added int64) movieRow {
	return movieRow{
		Id:       id,
		Library:  "house/movies",
		Kind:     libraryKindMovies,
		Released: released,
		Art:      art,
		Added:    added,
		Body:     movieBody{Collection: name},
		SetID:    set,
	}
}

// A set carries its earliest member's release, art, and arrival, whatever
// order the walk read the members in, and a movie that names no set adds no
// row.
func TestASetIsDerivedFromItsEarliestMember(t *testing.T) {
	fold := setFold{}
	fold.add([]movieRow{
		memberOf("movie:tmdb:2", "set:tmdb:1570", "Quiet Harbor Collection", "2004-09-22", "Deep Water/poster.jpg", 200),
		memberOf("movie:tmdb:3", "", "", "1980", "Alone/folder.jpg", 300),
	})
	fold.add([]movieRow{
		memberOf("movie:tmdb:1", "set:tmdb:1570", "Quiet Harbor Collection", "1998-06-12", "Harbor/folder.jpg", 100),
	})

	rows := fold.rows()
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the one set the members name", rows)
	}
	want := setRow{
		Id:       "set:tmdb:1570",
		Library:  "house/movies",
		Kind:     libraryKindMovies,
		Title:    "Quiet Harbor Collection",
		SortKey:  "Quiet Harbor Collection",
		Slug:     "quiet-harbor-collection",
		Released: "1998-06-12",
		Added:    100,
		Art:      "Harbor/folder.jpg",
	}
	if rows[0] != want {
		t.Errorf("row = %+v, want %+v", rows[0], want)
	}
}

// Two members released on the same date resolve on the member's id, so a
// re-walk derives the same row whatever order it read them in.
func TestASetWithTwoMembersOfOneDateResolvesOnTheMemberID(t *testing.T) {
	fold := setFold{}
	fold.add([]movieRow{
		memberOf("movie:tmdb:9", "set:name:twins", "Twins", "1994", "Nine/folder.jpg", 900),
		memberOf("movie:tmdb:4", "set:name:twins", "Twins", "1994", "Four/folder.jpg", 400),
	})

	rows := fold.rows()
	if len(rows) != 1 || rows[0].Art != "Four/folder.jpg" {
		t.Errorf("rows = %+v, want the lower member id to hold the set", rows)
	}
}

// The body column of a set is empty, because a set holds nothing beyond the
// header every kind carries.
func TestASetWritesAnEmptyBody(t *testing.T) {
	catalog, recorder := recordingCatalog(t)

	if _, err := catalog.UpsertSets(t.Context(), []setRow{{Id: "set:tmdb:1570", Library: "house/movies"}}); err != nil {
		t.Fatal(err)
	}

	statements := recorder.all()
	if len(statements) != 1 {
		t.Fatalf("statements = %d, want the one set upsert", len(statements))
	}
	if body := statements[0].params[10]; body != "{}" {
		t.Errorf("body = %v, want an empty document", body)
	}
}

// The walk reads the set off each sidecar: the collection id Jellyfin
// writes on the element, the name a Kodi sidecar carries alone, and no set
// where the sidecar names none.
func TestAWalkCarriesTheSetOfEveryTitle(t *testing.T) {
	movies := moviesByTitle(walkMovies("testdata/sets", "house/movies", nil))

	cases := []struct {
		title string
		want  string
	}{
		{title: "Quiet Harbor", want: "set:tmdb:1570"},
		{title: "Quiet Harbor: Deep Water", want: "set:tmdb:1570"},
		{title: "Northwind", want: "set:name:northwind-trilogy"},
	}
	for _, testCase := range cases {
		t.Run(testCase.title, func(t *testing.T) {
			if got := movies[testCase.title].SetID; got != testCase.want {
				t.Errorf("setID = %q, want %q", got, testCase.want)
			}
		})
	}
	if got := moviesByTitle(walkMovies("testdata/movies", "house/movies", nil))["The Matrix"].SetID; got != "set:name:the-matrix-collection" {
		t.Errorf("setID = %q, want the named set of a sidecar with no collection id", got)
	}
}

// setTitle is one title folder a set drill writes: the folder's name, the
// title in its sidecar, its release date, and the set it names.
type setTitle struct {
	folder   string
	title    string
	released string
	tmdb     string
	set      string
	colID    string
}

// setTree writes a movies volume of title folders, each with a sidecar that
// names its set, so a walk of the root derives the sets from real files.
func setTree(t *testing.T, titles ...setTitle) string {
	t.Helper()
	root := t.TempDir()
	for _, title := range titles {
		writeSetTitle(t, root, title)
	}
	return root
}

func writeSetTitle(t *testing.T, root string, title setTitle) {
	t.Helper()
	element := fmt.Sprintf("<set tmdbcolid=%q><name>%s</name></set>", title.colID, title.set)
	if title.colID == "" {
		element = fmt.Sprintf("<set>%s</set>", title.set)
	}
	dir := filepath.Join(root, title.folder)
	writeFile(t, filepath.Join(dir, "movie.nfo"), fmt.Sprintf(
		`<movie><title>%s</title><premiered>%s</premiered>%s<uniqueid type="tmdb">%s</uniqueid></movie>`,
		title.title, title.released, element, title.tmdb))
	writeFile(t, filepath.Join(dir, "movie.mkv"), "video")
	writeFile(t, filepath.Join(dir, "folder.jpg"), "image")
}

// sqliteScanner is a scanner over a real database loaded with the shipped
// schema, so a walk and a rescan write and read the catalog's own columns.
func sqliteScanner(t *testing.T, root string) (*scanner, *sqliteAgent) {
	t.Helper()
	catalog, agent := newSQLiteCatalog(t)
	scan := &scanner{
		root:    root,
		library: "house/movies",
		kind:    libraryKindMovies,
		catalog: catalog,
		report:  libraryReport{LastWalk: time.Now().UTC(), LastChange: time.Now().UTC()},
		bus:     newBus("", "test", nil, nil, nil),
	}
	return scan, agent
}

// setRowOf reads one set row back out of the database.
func setRowOf(t *testing.T, agent *sqliteAgent, library, id string) setRow {
	t.Helper()
	row := setRow{Id: id, Library: library}
	err := agent.db.QueryRow(
		`SELECT kind, path, title, sort_key, released, art, body, slug FROM sets WHERE library = ? AND id = ?`,
		library, id).Scan(&row.Kind, &row.Path, &row.Title, &row.SortKey, &row.Released, &row.Art, new(string), &row.Slug)
	if err != nil {
		t.Fatalf("reading the set %s: %v", id, err)
	}
	return row
}

// The two members of one set arrive in different write batches, and the set
// still derives from the earlier of them.
func TestAWalkDerivesASetAcrossItsWriteBatches(t *testing.T) {
	batchWas := scanFlushBatch
	t.Cleanup(func() { scanFlushBatch = batchWas })
	scanFlushBatch = 1

	root := setTree(t,
		setTitle{folder: "Deep Water (2004)", title: "Deep Water", released: "2004-09-22", tmdb: "2", set: "Quiet Harbor Collection", colID: "1570"},
		setTitle{folder: "Quiet Harbor (1998)", title: "Quiet Harbor", released: "1998-06-12", tmdb: "1", set: "Quiet Harbor Collection", colID: "1570"},
	)
	scan, agent := sqliteScanner(t, root)

	scan.fullWalk(context.Background())

	got := setRowOf(t, agent, "house/movies", "set:tmdb:1570")
	want := setRow{
		Id:       "set:tmdb:1570",
		Library:  "house/movies",
		Kind:     libraryKindMovies,
		Title:    "Quiet Harbor Collection",
		SortKey:  "Quiet Harbor Collection",
		Slug:     "quiet-harbor-collection",
		Released: "1998-06-12",
		Art:      filepath.Join("Quiet Harbor (1998)", "folder.jpg"),
	}
	got.Added = 0
	if got != want {
		t.Errorf("set = %+v, want %+v", got, want)
	}
	if count := agent.rowCount(t, "sets"); count != 1 {
		t.Errorf("sets = %d, want the one set the two titles name", count)
	}
}

// A set whose last member left the volume leaves with it, because the walk
// marks only the sets its movies still name.
func TestAWalkPrunesASetWithNoMemberLeft(t *testing.T) {
	root := setTree(t,
		setTitle{folder: "Northwind (1970)", title: "Northwind", released: "1970-03-04", tmdb: "3", set: "Northwind Trilogy"},
		setTitle{folder: "Quiet Harbor (1998)", title: "Quiet Harbor", released: "1998-06-12", tmdb: "1", set: "Quiet Harbor Collection", colID: "1570"},
	)
	scan, agent := sqliteScanner(t, root)
	ctx := context.Background()

	scan.fullWalk(ctx)
	if count := agent.rowCount(t, "sets"); count != 2 {
		t.Fatalf("sets = %d, want the set of each title", count)
	}

	if err := os.RemoveAll(filepath.Join(root, "Northwind (1970)")); err != nil {
		t.Fatal(err)
	}
	scan.fullWalk(ctx)

	if agent.holdsItem(t, "sets", "house/movies", "set:name:northwind-trilogy") {
		t.Error("the set of the departed title stands")
	}
	if !agent.holdsItem(t, "sets", "house/movies", "set:tmdb:1570") {
		t.Error("the set of the title that stayed was swept")
	}
}

// A rescan reads one folder and not a set's other members, so the set
// derives again from the movie rows the catalog holds.
func TestARescanRederivesTheSetOfTheFolderItRead(t *testing.T) {
	root := setTree(t,
		setTitle{folder: "Quiet Harbor (1998)", title: "Quiet Harbor", released: "1998-06-12", tmdb: "1", set: "Quiet Harbor Collection", colID: "1570"},
		setTitle{folder: "Deep Water (2004)", title: "Deep Water", released: "2004-09-22", tmdb: "2", set: "Quiet Harbor Collection", colID: "1570"},
	)
	scan, agent := sqliteScanner(t, root)
	ctx := context.Background()

	scan.fullWalk(ctx)
	if got := setRowOf(t, agent, "house/movies", "set:tmdb:1570").Released; got != "1998-06-12" {
		t.Fatalf("released = %q, want the earlier member's", got)
	}

	// The earliest member left the volume, so the set's release and art move
	// to the member that is left.
	if err := os.RemoveAll(filepath.Join(root, "Quiet Harbor (1998)")); err != nil {
		t.Fatal(err)
	}
	scan.rescan(ctx, filepath.Join(root, "Quiet Harbor (1998)"))

	got := setRowOf(t, agent, "house/movies", "set:tmdb:1570")
	if got.Released != "2004-09-22" || got.Art != filepath.Join("Deep Water (2004)", "folder.jpg") {
		t.Errorf("set = %+v, want the surviving member's release and art", got)
	}
}

// A rescan of a folder that arrived writes the set it names, so a webhook
// for a new title needs no full walk to put its set in the catalog.
func TestARescanWritesTheSetOfATitleThatArrived(t *testing.T) {
	root := setTree(t,
		setTitle{folder: "Quiet Harbor (1998)", title: "Quiet Harbor", released: "1998-06-12", tmdb: "1", set: "Quiet Harbor Collection", colID: "1570"},
	)
	scan, agent := sqliteScanner(t, root)
	ctx := context.Background()

	scan.fullWalk(ctx)
	writeSetTitle(t, root, setTitle{folder: "Early Harbor (1990)", title: "Early Harbor", released: "1990-01-08", tmdb: "4", set: "Quiet Harbor Collection", colID: "1570"})

	scan.rescan(ctx, filepath.Join(root, "Early Harbor (1990)"))

	if got := setRowOf(t, agent, "house/movies", "set:tmdb:1570").Released; got != "1990-01-08" {
		t.Errorf("released = %q, want the arriving member's, which is the earliest", got)
	}
}

// A rescan that takes a set's last member deletes the set, because nothing
// else in the catalog holds it.
func TestARescanDeletesASetWithNoMemberLeft(t *testing.T) {
	root := setTree(t,
		setTitle{folder: "Northwind (1970)", title: "Northwind", released: "1970-03-04", tmdb: "3", set: "Northwind Trilogy"},
	)
	scan, agent := sqliteScanner(t, root)
	ctx := context.Background()

	scan.fullWalk(ctx)
	if count := agent.rowCount(t, "sets"); count != 1 {
		t.Fatalf("sets = %d, want the one set the title names", count)
	}

	if err := os.RemoveAll(filepath.Join(root, "Northwind (1970)")); err != nil {
		t.Fatal(err)
	}
	scan.rescan(ctx, filepath.Join(root, "Northwind (1970)"))

	if count := agent.rowCount(t, "sets"); count != 0 {
		t.Errorf("sets = %d, want none once the last member left", count)
	}
}

// A series library names no set, so a rescan of one reads none and writes
// none.
func TestARescanOfASeriesLibraryReadsNoSets(t *testing.T) {
	scan, recorder := testScanner(t, "testdata/series", libraryKindSeries)

	scan.rescan(context.Background(), filepath.Join("testdata", "series", "Breaking Bad"))

	if containsKind(sqlKinds(recorder), "INSERT SETS") {
		t.Error("a series rescan wrote a set row")
	}
}

// A catalog that cannot answer leaves the sets as they are, and the failure
// reaches the caller instead of a set row derived from nothing.
func TestReconcileSetsSurfacesACatalogFailure(t *testing.T) {
	catalog, recorder := recordingCatalog(t)
	recorder.status = 500

	if err := reconcileSets(t.Context(), catalog, "house/movies", []string{"set:tmdb:1570"}); err == nil {
		t.Error("reconcileSets hid a catalog failure")
	}
}
