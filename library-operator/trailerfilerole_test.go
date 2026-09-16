package main

// What these tests read: what one title's run leaves on the volume and in the
// ledger, what each failure leaves instead, and that nothing lands over a
// file that exists.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// One site that answers the files a case names and one body for every
// address it serves.
type scriptedFetcher struct {
	name string
	held []trailerFile
	err  error
}

func (s scriptedFetcher) site() string { return s.name }

func (s scriptedFetcher) files(context.Context, trailerRow) ([]trailerFile, error) {
	return s.held, s.err
}

// A line of one site, with a server behind it that answers every address with
// the body the case names. The site states a size of a thousand bytes for
// every line of the file.
func trailerFetchLineOf(t *testing.T, body string, heights []int, fail error) *trailerFetchLine {
	t.Helper()
	files := make([]trailerFile, len(heights))
	for at, height := range heights {
		files[at] = trailerFile{Height: height, Size: int64(height) * 1000}
	}
	line, _ := trailerFetchLineOfFiles(t, body, files, fail)
	return line
}

// The same line over the files a case states itself, with the address of each
// one on the server, and the bytes the server answered.
func trailerFetchLineOfFiles(t *testing.T, body string, files []trailerFile,
	fail error) (*trailerFetchLine, *atomic.Int64) {
	t.Helper()
	answered := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answered.Add(int64(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	client := newArchiveClient(server.URL)
	client.http = server.Client()
	client.interval = 0

	held := slices.Clone(files)
	for at := range held {
		held[at].URL = fmt.Sprintf("%s/download/one/%d.mp4", server.URL, at)
	}
	return &trailerFetchLine{sources: map[string]trailerSource{
		trailerSiteArchive: {
			fetcher:  scriptedFetcher{name: trailerSiteArchive, held: held, err: fail},
			requests: &client.providerRequests,
		},
	}}, answered
}

// One identified movie with its feature, and one archive trailers row.
func seedTrailerFileRun(t *testing.T, catalog *Catalog, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, trailerFileFolder, trailerFileFolder+".mkv"), "video")
	seedTrailerFileGap(t, catalog, []string{trailerSiteArchive}, []fileRow{featureRow()})
}

// The ledger the trailerfile fact left beside a title.
func trailerFileLedger(t *testing.T, root string) likenLedger {
	t.Helper()
	return artLedger(t, filepath.Join(root, trailerFileFolder), factTrailerFile)
}

// The names the title's trailers folder holds, sorted.
func trailersFolderHolds(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, trailerFileFolder, trailersFolderName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names
}

// The whole run of one title: the file lands under the trailer's own name,
// the ledger records it, and the attempt says it was found.
// A thirty-second file passes the check, because a TV spot is one of the
// kinds the trailer fact records and this fact pulls.
func TestATVSpotLandsLikeATrailer(t *testing.T) {
	standInFFmpegRemux(t)
	standInProbe(t, 1, 30)
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerFileRun(t, catalog, root)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	line := trailerFetchLineOf(t, "video bytes", []int{1080}, nil)

	if err := work.trailerFileGap(t.Context(), line); err != nil {
		t.Fatal(err)
	}

	if held := trailersFolderHolds(t, root); !slices.Equal(held, []string{"Official Trailer.mp4"}) {
		t.Errorf("the folder holds %v, want the spot the pull landed", held)
	}
}

func TestTheTrailerFileFactWritesTheFileAndTheLedger(t *testing.T) {
	standInFFmpegRemux(t)
	standInProbe(t, 1, 120)
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerFileRun(t, catalog, root)
	work, log := testEnricher(t, libraryKindMovies, root, catalog)
	line := trailerFetchLineOf(t, "video bytes", []int{720, 1080, 2160}, nil)

	if err := work.trailerFileGap(t.Context(), line); err != nil {
		t.Fatal(err)
	}

	if held := trailersFolderHolds(t, root); !slices.Equal(held, []string{"Official Trailer.mp4"}) {
		t.Errorf("the folder holds %v, want the one file the pull landed", held)
	}
	ledger := trailerFileLedger(t, root)
	if ledger.TrailerFile == nil {
		t.Fatal("the ledger records no file, want the one the pull landed")
	}
	if ledger.TrailerFile.File != filepath.Join(trailersFolderName, "Official Trailer.mp4") {
		t.Errorf("the record names %q, want the file under the trailers folder",
			ledger.TrailerFile.File)
	}
	if ledger.TrailerFile.Height != 2160 || ledger.TrailerFile.Size != int64(len("video bytes")) {
		t.Errorf("the record reads %+v, want the height and the size of the pull",
			ledger.TrailerFile)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound ||
		ledger.Attempts[0].Path != likenSelfPath {
		t.Errorf("attempts = %+v, want the one that found the file", ledger.Attempts)
	}
	if !strings.Contains(log.String(), "pulled the trailer of 1 of the 1 titles") {
		t.Errorf("log = %q, want the line that counts the run", log.String())
	}
}

// The ceiling is the feature's own height, so a file above it is taken only
// where nothing fits.
func TestTheTrailerFilePullTakesTheHeightTheFeatureAllows(t *testing.T) {
	standInFFmpegRemux(t)
	standInProbe(t, 1, 120)
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, trailerFileFolder, trailerFileFolder+".mkv"), "video")
	feature := featureRow()
	feature.Height = 1080
	seedTrailerFileGap(t, catalog, []string{trailerSiteArchive}, []fileRow{feature})
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	line := trailerFetchLineOf(t, "video bytes", []int{720, 1080, 2160}, nil)

	if err := work.trailerFileGap(t.Context(), line); err != nil {
		t.Fatal(err)
	}

	if height := trailerFileLedger(t, root).TrailerFile.Height; height != 1080 {
		t.Errorf("the pull took %d lines, want the feature's own 1080", height)
	}
}

// A title whose every trailer plays from a site with no fetcher records a
// miss.
func TestATitleWithNoFetchableTrailerRecordsAMiss(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerFileRun(t, catalog, root)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	line := &trailerFetchLine{sources: map[string]trailerSource{}}

	if work.trailerFileOne(t.Context(), line,
		identityItem{id: trailerFileItem, path: trailerFileFolder}, nil, 1080) {
		t.Error("the fact reported a pull, want none")
	}

	ledger := trailerFileLedger(t, root)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptNothing {
		t.Errorf("attempts = %+v, want the miss", ledger.Attempts)
	}
}

// A title whose every file the site states is over the limit records a miss,
// because the pull would fail on every one of them.
func TestATitleWhoseFilesAreAllOverTheLimitRecordsAMiss(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerFileRun(t, catalog, root)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	line, answered := trailerFetchLineOfFiles(t, "video bytes", []trailerFile{
		{Height: 1080, Size: trailerPullLimit + 1},
		{Height: 720, Size: trailerPullLimit + 1},
	}, nil)

	if err := work.trailerFileGap(t.Context(), line); err != nil {
		t.Fatal(err)
	}

	if answered.Load() != 0 {
		t.Errorf("the site answered %d bytes, want none", answered.Load())
	}
	ledger := trailerFileLedger(t, root)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptNothing {
		t.Errorf("attempts = %+v, want the miss", ledger.Attempts)
	}
}

// A title outside the Job's folder is left to the Job that covers it.
func TestTheTrailerFileFactSkipsATitleOutsideTheJobsScope(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerFileRun(t, catalog, root)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.scope = "Another Title (2020)"
	line := trailerFetchLineOf(t, "video bytes", []int{1080}, nil)

	if err := work.trailerFileGap(t.Context(), line); err != nil {
		t.Fatal(err)
	}

	if held := trailersFolderHolds(t, root); len(held) != 0 {
		t.Errorf("the folder holds %v, want nothing", held)
	}
}

// A container no site reached is a manifest to repair.
func TestTheTrailerFileFactNeedsASiteItCanFetchFrom(t *testing.T) {
	t.Setenv(librarySourcesVariable, providerBlockTMDb)
	t.Setenv(tmdbTokenVariable, "a-key")
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), nil)

	err := work.trailerFileFact(t.Context())

	if err == nil || !strings.Contains(err.Error(), factTrailerFile) {
		t.Errorf("err = %v, want the one that names the fact", err)
	}
}

// A run whose context ends reports the context's own error.
func TestTheTrailerFileGapEndsOnItsContext(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerFileRun(t, catalog, root)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	ctx, stop := context.WithCancel(t.Context())
	stop()

	err := work.trailerFileGap(ctx, trailerFetchLineOf(t, "video bytes", []int{1080}, nil))

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the context's own error", err)
	}
}

// A catalog read that fails ends the run, because the gap list is the work.
func TestTheTrailerFileGapEndsWhereTheCatalogRefusesATitle(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerFileRun(t, catalog, root)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	// The gap query is the first read, and the title read after it is refused.
	agent.queriesLeft = 1

	err := work.trailerFileGap(t.Context(), trailerFetchLineOf(t, "video bytes", []int{1080}, nil))

	if err == nil {
		t.Error("the run read every title, want the error the catalog gave")
	}
}

// The container builds its line out of the environment, the way the trailer
// container builds its own.
func TestTheTrailerFileFactBuildsItsLineOutOfTheEnvironment(t *testing.T) {
	standInFFmpegRemux(t)
	standInProbe(t, 1, 120)
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerFileRun(t, catalog, root)
	t.Setenv(librarySourcesVariable, providerBlockArchive)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trailerFileFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if work.trailerFiles == nil || len(work.trailerFiles.sources) != 1 {
		t.Errorf("the line holds %+v, want the one site the environment names", work.trailerFiles)
	}
}

// A catalog that refuses the trailers of a title ends the run.
func TestTheTrailerFileGapEndsWhereTheCatalogRefusesTheTrailers(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerFileRun(t, catalog, root)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	// The gap query and the title read answer, and the trailers read is refused.
	agent.queriesLeft = 2

	err := work.trailerFileGap(t.Context(), trailerFetchLineOf(t, "video bytes", []int{1080}, nil))

	if err == nil {
		t.Error("the run read every title, want the error the catalog gave")
	}
}
