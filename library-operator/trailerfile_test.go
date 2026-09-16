package main

// What these tests read: the gap, the two picks, and the landed name.

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// One trailers row as the catalog holds it.
func trailerRowOf(site, key string, score, resolution int, published string) trailerRow {
	return trailerRow{
		Library: "house/movies", Item: "movie:tmdb:603", Provider: site, Key: key,
		Site: site, URL: "https://" + site + ".example/" + key, Name: "Official Trailer",
		Kind: trailerKindTrailer, Score: score, Resolution: resolution, Published: published,
	}
}

// The pick takes score, then height, then the earliest published date, and
// only a site with a fetcher.
func TestThePickTakesTheBestTrailerItCanFetch(t *testing.T) {
	cases := []struct {
		name string
		rows []trailerRow
		want string
	}{
		{
			name: "the highest score",
			rows: []trailerRow{
				trailerRowOf(trailerSiteArchive, "low", 40, 1080, "2026-01-01"),
				trailerRowOf(trailerSiteArchive, "high", 90, 1080, "2026-01-01"),
			},
			want: "high",
		},
		{
			name: "the tallest of one score",
			rows: []trailerRow{
				trailerRowOf(trailerSitePeerTube, "short", 90, 720, "2026-01-01"),
				trailerRowOf(trailerSitePeerTube, "tall", 90, 2160, "2026-01-01"),
			},
			want: "tall",
		},
		{
			name: "the earliest published of one score and one height",
			rows: []trailerRow{
				trailerRowOf(trailerSitePeerTube, "later", 90, 1080, "2026-06-01"),
				trailerRowOf(trailerSitePeerTube, "earlier", 90, 1080, "2026-02-01"),
			},
			want: "earlier",
		},
		{
			name: "a site with no fetcher is passed over",
			rows: []trailerRow{
				trailerRowOf(trailerSiteYouTube, "best", 100, 2160, "2026-01-01"),
				trailerRowOf(trailerSiteArchive, "fetchable", 30, 480, "2026-01-01"),
			},
			want: "fetchable",
		},
		{
			name: "no row plays from a site with a fetcher",
			rows: []trailerRow{trailerRowOf(trailerSiteYouTube, "best", 100, 2160, "2026-01-01")},
		},
		{name: "the title has no trailer at all"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			row, held := pickTrailerRow(test.rows, testTrailerSources(t))

			if held != (test.want != "") {
				t.Fatalf("the pick took %+v, want %q", row, test.want)
			}
			if held && row.Key != test.want {
				t.Errorf("the pick took %q, want %q", row.Key, test.want)
			}
		})
	}
}

// The file is the tallest that fits under the feature, and the shortest above
// it where none fits.
func TestThePickTakesTheTallestFileUnderTheFeature(t *testing.T) {
	files := []trailerFile{
		{URL: "480", Height: 480}, {URL: "720", Height: 720},
		{URL: "1080", Height: 1080}, {URL: "2160", Height: 2160},
	}
	cases := []struct {
		name    string
		files   []trailerFile
		ceiling int
		want    string
	}{
		{name: "the tallest that fits", files: files, ceiling: 1080, want: "1080"},
		{name: "the ceiling falls between two", files: files, ceiling: 900, want: "720"},
		{name: "every file is above the ceiling", files: files, ceiling: 360, want: "480"},
		{name: "the feature is taller than every file", files: files, ceiling: 4320, want: "2160"},
		{name: "the site holds no file", ceiling: 1080},
		{
			name:  "a file whose height the site does not state",
			files: []trailerFile{{URL: "unstated"}}, ceiling: 1080, want: "unstated",
		},
		{
			name: "a file the site states is over the limit",
			files: []trailerFile{
				{URL: "1080", Height: 1080, Size: trailerPullLimit + 1},
				{URL: "720", Height: 720, Size: trailerPullLimit},
			},
			ceiling: 1080, want: "720",
		},
		{
			name: "every file is over the limit",
			files: []trailerFile{
				{URL: "1080", Height: 1080, Size: trailerPullLimit + 1},
				{URL: "2160", Height: 2160, Size: trailerPullLimit + 1},
			},
			ceiling: 1080,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			file, held := pickTrailerFile(test.files, test.ceiling)

			if held != (test.want != "") {
				t.Fatalf("the pick took %+v, want %q", file, test.want)
			}
			if held && file.URL != test.want {
				t.Errorf("the pick took %q, want %q", file.URL, test.want)
			}
		})
	}
}

// The name a trailer lands under.
func TestTheNameATrailerLandsUnder(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{name: "Official Trailer", want: "Official Trailer"},
		{name: "DUNE: PART THREE (2026) - Trailer [4K]", want: "DUNE PART THREE (2026) - Trailer [4K]"},
		{name: "a/b\\c", want: "a b c"},
		{name: "  spaced   out  ", want: "spaced out"},
		{name: "...hidden", want: "hidden"},
		{name: "///", want: fileRoleTrailer},
		{name: "", want: fileRoleTrailer},
		{
			name: strings.Repeat("Trailer ", 12),
			want: strings.TrimSpace(strings.Repeat("Trailer ", 10)),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := safeTrailerName(test.name); got != test.want {
				t.Errorf("the name is %q, want %q", got, test.want)
			}
		})
	}
}

// The library, the title, and the folder every gap case works on.
const (
	trailerFileLibrary = "house/movies"
	trailerFileItem    = "movie:tmdb:603"
	trailerFileFolder  = "The Signal (2014)"
)

// One identified movie with its feature and one trailers row per site.
func seedTrailerFileGap(t *testing.T, catalog *Catalog, sites []string, files []fileRow) {
	t.Helper()
	seed := &walkResult{
		movies: []movieRow{{
			Id: trailerFileItem, Library: trailerFileLibrary, Kind: libraryKindMovies,
			Path: trailerFileFolder, Title: "The Signal", Released: "2014-06-13",
		}},
		files: files,
	}
	for _, site := range sites {
		seed.trailers = append(seed.trailers, trailerRow{
			Library: trailerFileLibrary, Item: trailerFileItem, Provider: site, Key: "k-" + site,
			Site: site, URL: "https://" + site + ".example/k", Name: "Official Trailer",
			Kind: trailerKindTrailer, Score: 90,
		})
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

// The feature of that title, as the walk rows it.
func featureRow() fileRow {
	return fileRow{
		Library: trailerFileLibrary, Path: trailerFileFolder + "/" + trailerFileFolder + ".mkv",
		Present: true, Type: fileTypeVideo, Role: fileRolePrimary, Height: 2160,
		Items: []string{trailerFileItem},
	}
}

// One trailer file beside the feature, as the walk rows it.
func trailerFileRow(path string) fileRow {
	return fileRow{
		Library: trailerFileLibrary, Path: trailerFileFolder + "/" + path,
		Present: true, Type: fileTypeVideo, Role: fileRoleTrailer,
		Items: []string{trailerFileItem},
	}
}

// The gap is an identified title with a fetchable trailer and no trailer
// file.
func TestTheTrailerFileGapAgainstTheRealSchema(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name     string
		sites    []string
		files    []fileRow
		attempts []attemptRow
		want     []string
	}{
		{
			name:  "a fetchable trailer and no file",
			sites: []string{trailerSiteArchive}, files: []fileRow{featureRow()},
			want: []string{trailerFileItem},
		},
		{
			name:  "a trailer file already stands beside the feature",
			sites: []string{trailerSiteArchive},
			files: []fileRow{featureRow(), trailerFileRow(trailerFileFolder + "-trailer.mkv")},
		},
		{
			name:  "a trailer file under the title's trailers folder",
			sites: []string{trailerSitePeerTube},
			files: []fileRow{featureRow(), trailerFileRow("trailers/Official Trailer.mp4")},
		},
		{
			name:  "every trailer plays from a site with no fetcher",
			sites: []string{trailerSiteYouTube}, files: []fileRow{featureRow()},
		},
		{name: "the title has no trailer at all", files: []fileRow{featureRow()}},
		{
			name:  "a title asked about inside the window",
			sites: []string{trailerSiteArchive}, files: []fileRow{featureRow()},
			attempts: []attemptRow{{
				Library: trailerFileLibrary, Item: trailerFileItem, Fact: factTrailerFile,
				At: now.Unix(), Result: attemptNothing,
			}},
		},
		{
			name:  "a title asked about past the window",
			sites: []string{trailerSiteArchive}, files: []fileRow{featureRow()},
			attempts: []attemptRow{{
				Library: trailerFileLibrary, Item: trailerFileItem, Fact: factTrailerFile,
				At: now.Add(-2 * defaultRetryInterval).Unix(), Result: attemptNothing,
			}},
			want: []string{trailerFileItem},
		},
		{
			name:  "a title whose pull failed, inside the error window",
			sites: []string{trailerSiteArchive}, files: []fileRow{featureRow()},
			attempts: []attemptRow{{
				Library: trailerFileLibrary, Item: trailerFileItem, Fact: factTrailerFile,
				At: now.Unix(), Result: attemptError,
			}},
		},
		{
			name:  "a title whose pull failed, past the error window",
			sites: []string{trailerSiteArchive}, files: []fileRow{featureRow()},
			attempts: []attemptRow{{
				Library: trailerFileLibrary, Item: trailerFileItem, Fact: factTrailerFile,
				At: now.Add(-2 * errorRetryInterval).Unix(), Result: attemptError,
			}},
			want: []string{trailerFileItem},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seedTrailerFileGap(t, catalog, test.sites, test.files)
			if _, err := catalog.UpsertAttempts(t.Context(), test.attempts); err != nil {
				t.Fatal(err)
			}

			ids, err := catalog.queryStrings(t.Context(), gapQueries[factTrailerFile],
				gapParams(factTrailerFile, trailerFileLibrary, now, time.Time{}))
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(ids, test.want) {
				t.Errorf("gap = %v, want %v", ids, test.want)
			}
		})
	}
}

// Two titles whose folder names differ by one character, the first of them a
// LIKE metacharacter, with a trailer beside the second alone.
func seedTrailerFileSiblings(t *testing.T, catalog *Catalog, folder, sibling string) {
	t.Helper()
	seed := &walkResult{
		movies: []movieRow{
			{Id: "movie:tmdb:1", Library: trailerFileLibrary, Kind: libraryKindMovies,
				Path: folder, Title: "One"},
			{Id: "movie:tmdb:2", Library: trailerFileLibrary, Kind: libraryKindMovies,
				Path: sibling, Title: "Two"},
		},
		files: []fileRow{
			{Library: trailerFileLibrary, Path: sibling + "/" + sibling + ".mkv", Present: true,
				Type: fileTypeVideo, Role: fileRolePrimary, Height: 2160,
				Items: []string{"movie:tmdb:2"}},
			{Library: trailerFileLibrary, Path: sibling + "/" + sibling + "-trailer.mkv",
				Present: true, Type: fileTypeVideo, Role: fileRoleTrailer,
				Items: []string{"movie:tmdb:2"}},
		},
	}
	for _, item := range []string{"movie:tmdb:1", "movie:tmdb:2"} {
		seed.trailers = append(seed.trailers, trailerRow{
			Library: trailerFileLibrary, Item: item, Provider: trailerSiteArchive,
			Key: "k-" + item, Site: trailerSiteArchive, Name: "Official Trailer",
			Kind: trailerKindTrailer, Score: 90,
		})
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

// The folders the gap and the ceiling scope by, each holding one character a
// LIKE reads as a wildcard.
const (
	trailerFileMetaFolder  = "A_B (2020)"
	trailerFilePercentName = "50% Off (2021)"
)

// A title whose folder name holds a LIKE metacharacter reads the files of its
// own folder alone, so a sibling's trailer closes no gap of its own.
func TestTheTrailerFileGapReadsTheTitlesOwnFolder(t *testing.T) {
	cases := []struct {
		name    string
		folder  string
		sibling string
	}{
		{name: "an underscore", folder: trailerFileMetaFolder, sibling: "AXB (2020)"},
		{name: "a percent", folder: trailerFilePercentName, sibling: "50 Cents More Off (2021)"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seedTrailerFileSiblings(t, catalog, test.folder, test.sibling)

			ids, err := catalog.queryStrings(t.Context(), gapQueries[factTrailerFile],
				gapParams(factTrailerFile, trailerFileLibrary, time.Now().UTC(), time.Time{}))
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(ids, []string{"movie:tmdb:1"}) {
				t.Errorf("gap = %v, want the title with no trailer of its own", ids)
			}
		})
	}
}

// The ceiling reads the same way, so a sibling's feature is no ceiling.
func TestTheFeatureHeightReadsTheTitlesOwnFolder(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedTrailerFileSiblings(t, catalog, trailerFileMetaFolder, "AXB (2020)")

	height, err := catalog.featureHeight(t.Context(), trailerFileLibrary, trailerFileMetaFolder)
	if err != nil {
		t.Fatal(err)
	}

	if height != trailerFeatureHeight {
		t.Errorf("the feature is %dp, want the default %dp", height, trailerFeatureHeight)
	}
}

// An unidentified title is no gap.
func TestTheTrailerFileGapLeavesOutAnUnidentifiedTitle(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seed := &walkResult{
		movies: []movieRow{{
			Id: "movie:path:signal", Library: trailerFileLibrary, Kind: libraryKindMovies,
			Path: trailerFileFolder, Title: "The Signal",
		}},
		trailers: []trailerRow{{
			Library: trailerFileLibrary, Item: "movie:path:signal", Provider: trailerSiteArchive,
			Key: "k", Site: trailerSiteArchive, Score: 90,
		}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}

	ids, err := catalog.queryStrings(t.Context(), gapQueries[factTrailerFile],
		gapParams(factTrailerFile, trailerFileLibrary, time.Now().UTC(), time.Time{}))
	if err != nil {
		t.Fatal(err)
	}

	if len(ids) != 0 {
		t.Errorf("gap = %v, want no title", ids)
	}
}

// The ceiling the pick reads, and the default where the catalog states none.
func TestTheFeatureHeightOfATitle(t *testing.T) {
	cases := []struct {
		name  string
		files []fileRow
		want  int
	}{
		{name: "the feature's own height", files: []fileRow{featureRow()}, want: 2160},
		{name: "no feature at all", want: trailerFeatureHeight},
		{
			name: "a feature the probe has not read",
			files: []fileRow{{
				Library: trailerFileLibrary, Path: trailerFileFolder + "/One.mkv", Present: true,
				Type: fileTypeVideo, Role: fileRolePrimary, Items: []string{trailerFileItem},
			}},
			want: trailerFeatureHeight,
		},
		{
			name:  "a trailer beside the feature is no feature",
			files: []fileRow{trailerFileRow("One-trailer.mkv")},
			want:  trailerFeatureHeight,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seedTrailerFileGap(t, catalog, nil, test.files)

			height, err := catalog.featureHeight(t.Context(), trailerFileLibrary, trailerFileFolder)
			if err != nil {
				t.Fatal(err)
			}

			if height != test.want {
				t.Errorf("the feature is %d lines, want %d", height, test.want)
			}
		})
	}
}

// The trailers of one title, which is what the pick sorts.
func TestTheTrailersOfOneTitle(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedTrailerFileGap(t, catalog, []string{trailerSiteArchive, trailerSitePeerTube}, nil)

	rows, err := catalog.trailersOf(t.Context(), trailerFileLibrary, trailerFileItem)
	if err != nil {
		t.Fatal(err)
	}

	var sites []string
	for _, row := range rows {
		sites = append(sites, row.Site)
	}
	slices.Sort(sites)
	if !slices.Equal(sites, []string{trailerSiteArchive, trailerSitePeerTube}) {
		t.Errorf("the rows are %v, want one per site", sites)
	}
	if rows[0].Key == "" || rows[0].URL == "" || rows[0].Name == "" {
		t.Errorf("a row reads %+v, want the key, the address, and the name", rows[0])
	}
}
