package main

// what these tests read: the nfo container reads a dataset file through the
// cache on the provider's claim, asks IMDb with If-None-Match on every run,
// keeps one copy for each file, lands a download only when it is complete,
// and reads straight from IMDb when the cache fails.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// a fetcher on the dataset server, with the cache directory the test names,
// and the log it writes.
func testFetcher(server *datasetServer, cache string) (*datasetFetcher, *bytes.Buffer) {
	log := &bytes.Buffer{}
	var mutex sync.Mutex
	return &datasetFetcher{
		base: server.URL, cache: cache, client: server.Client(),
		writer: newVolumeWriter("movies-enrich"),
		logf: func(format string, args ...any) {
			mutex.Lock()
			defer mutex.Unlock()
			fmt.Fprintf(log, format+"\n", args...)
		},
	}, log
}

// the ids of every row one read handed over.
func readIDs(t *testing.T, fetcher *datasetFetcher, name string) []string {
	t.Helper()
	var ids []string
	if _, err := fetcher.read(t.Context(), name, func(cells [][]byte) {
		ids = append(ids, datasetCell(cells, 0))
	}); err != nil {
		t.Fatal(err)
	}
	return ids
}

var fixtureRatingIDs = []string{"tt9000001", "tt9000002", "tt9000010", "tt9000011", "tt9000020"}

func TestAReadWithNoCacheStreamsTheFileAndWritesNothing(t *testing.T) {
	server := newDatasetServer(t, testNow)
	fetcher, _ := testFetcher(server, "")

	modified, err := fetcher.read(t.Context(), datasetTitleRatings, func([][]byte) {})
	if err != nil {
		t.Fatal(err)
	}
	if !modified.Equal(testNow) {
		t.Errorf("modified = %v, want the Last-Modified the server sent", modified)
	}
	if got := readIDs(t, fetcher, datasetTitleRatings); !slices.Equal(got, fixtureRatingIDs) {
		t.Errorf("rows = %v, want %v", got, fixtureRatingIDs)
	}
}

// The second run asks with the ETag of its copy, gets a 304, and reads the
// same rows off the disk.
func TestTheSecondReadGetsA304AndReadsTheCopy(t *testing.T) {
	server := newDatasetServer(t, testNow)
	cache := t.TempDir()
	fetcher, _ := testFetcher(server, cache)

	first := readIDs(t, fetcher, datasetTitleRatings)
	second := readIDs(t, fetcher, datasetTitleRatings)

	if !slices.Equal(first, fixtureRatingIDs) || !slices.Equal(second, fixtureRatingIDs) {
		t.Errorf("rows = %v then %v, want %v both times", first, second, fixtureRatingIDs)
	}
	want := []string{"GET title.ratings 200", "GET title.ratings 304"}
	if got := server.log(); !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	record, held := readDatasetRecord(filepath.Join(cache, datasetTitleRatings))
	if !held || record.ETag != server.etag(datasetTitleRatings) || record.Size != fixtureSize(t, datasetTitleRatings) {
		t.Errorf("record = %+v, want the ETag and the size of the download", record)
	}
}

// A new version replaces the copy, and the directory holds one copy after.
func TestANewVersionReplacesTheCopy(t *testing.T) {
	server := newDatasetServer(t, testNow)
	cache := t.TempDir()
	fetcher, _ := testFetcher(server, cache)
	readIDs(t, fetcher, datasetTitleRatings)

	server.versions[datasetTitleRatings] = 2
	readIDs(t, fetcher, datasetTitleRatings)

	dir := filepath.Join(cache, datasetTitleRatings)
	want := []string{datasetCopyName(server.etag(datasetTitleRatings)), datasetRecordName}
	slices.Sort(want)
	if got := namesIn(t, dir); !slices.Equal(got, want) {
		t.Errorf("cache = %v, want %v", got, want)
	}
}

// Two containers on one node read one file at once. One downloads it, and
// the other waits for the lock and reads the copy.
func TestTwoReadersDownloadAFileOnce(t *testing.T) {
	server := newDatasetServer(t, testNow)
	cache := t.TempDir()
	var readers sync.WaitGroup
	for range 2 {
		fetcher, _ := testFetcher(server, cache)
		readers.Go(func() { readIDs(t, fetcher, datasetTitleRatings) })
	}
	readers.Wait()

	downloads := 0
	for _, request := range server.log() {
		if strings.HasSuffix(request, " 200") {
			downloads++
		}
	}
	if downloads != 1 {
		t.Errorf("requests = %v, want one download", server.log())
	}
}

// A cache the container cannot write reads the file from IMDb and logs the
// filesystem's own text.
func TestACacheThatFailsStillReadsTheFile(t *testing.T) {
	server := newDatasetServer(t, testNow)
	missing := filepath.Join(t.TempDir(), "not-mounted")
	fetcher, log := testFetcher(server, missing)

	if got := readIDs(t, fetcher, datasetTitleRatings); !slices.Equal(got, fixtureRatingIDs) {
		t.Errorf("rows = %v, want %v", got, fixtureRatingIDs)
	}
	if !strings.Contains(log.String(), "no such file or directory") {
		t.Errorf("log = %q, want the filesystem's error", log.String())
	}
}

// A download that ends early is an error, and no copy lands.
func TestAnIncompleteDownloadLandsNoCopy(t *testing.T) {
	cache := t.TempDir()
	body, err := os.ReadFile(filepath.Join("testdata", "imdb", datasetTitleRatings+".tsv.gz"))
	if err != nil {
		t.Fatal(err)
	}
	server := newDatasetServer(t, testNow)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"short"`)
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = w.Write(body[:len(body)/2])
	})
	fetcher, _ := testFetcher(server, cache)

	if _, err := fetcher.read(t.Context(), datasetTitleRatings, func([][]byte) {}); err == nil {
		t.Fatal("read = nil, want the error of the short download")
	}
	if got := namesIn(t, filepath.Join(cache, datasetTitleRatings)); len(got) != 0 {
		t.Errorf("cache = %v, want nothing", got)
	}
}

// A directory the container cannot write, as on a full or read-only claim,
// ends the copy and not the read, and the log names the filesystem's error.
func TestADirectoryThatRefusesTheCopyStillReadsTheFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	server := newDatasetServer(t, testNow)
	cache := t.TempDir()
	dir := filepath.Join(cache, datasetTitleRatings)
	if err := os.Mkdir(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	fetcher, log := testFetcher(server, cache)

	if got := readIDs(t, fetcher, datasetTitleRatings); !slices.Equal(got, fixtureRatingIDs) {
		t.Errorf("rows = %v, want %v", got, fixtureRatingIDs)
	}
	if !strings.Contains(log.String(), "permission denied") {
		t.Errorf("log = %q, want the filesystem's error", log.String())
	}
}

// A status other than 200 or 304 is an error that names the file, with a
// cache and without one.
func TestAnAnswerOtherThanTheFileIsAnError(t *testing.T) {
	cases := []struct {
		name  string
		cache func(*testing.T) string
	}{
		{name: "with no cache", cache: func(*testing.T) string { return "" }},
		{name: "with a cache", cache: func(t *testing.T) string { return t.TempDir() }},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			server := newDatasetServer(t, testNow)
			server.statuses[datasetTitleRatings] = http.StatusNotFound
			fetcher, _ := testFetcher(server, one.cache(t))

			_, err := fetcher.read(t.Context(), datasetTitleRatings, func([][]byte) {})

			if err == nil || err.Error() != "IMDb answered 404 for title.ratings" {
				t.Errorf("read = %v, want the answer that names the file", err)
			}
		})
	}
}

// Two requests of one container are the pace apart.
func TestTwoRequestsKeepThePace(t *testing.T) {
	server := newDatasetServer(t, testNow)
	fetcher, _ := testFetcher(server, "")
	fetcher.pace = 50 * time.Millisecond
	started := time.Now()

	readIDs(t, fetcher, datasetTitleRatings)
	readIDs(t, fetcher, datasetTitleRatings)

	if elapsed := time.Since(started); elapsed < fetcher.pace {
		t.Errorf("two reads took %s, want at least the pace of %s", elapsed, fetcher.pace)
	}
}

// A container that waits for another container's lock stops waiting when its
// context ends, and the read fails with the context's error.
func TestAWaitForTheLockEndsWithTheContext(t *testing.T) {
	server := newDatasetServer(t, testNow)
	cache := t.TempDir()
	held, err := lockDatasetDirectory(t.Context(), filepath.Join(cache, datasetTitleRatings))
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	fetcher, log := testFetcher(server, cache)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	if _, err := fetcher.read(ctx, datasetTitleRatings, func([][]byte) {}); err == nil {
		t.Error("read = nil, want the context's error")
	}
	if !strings.Contains(log.String(), "context deadline exceeded") {
		t.Errorf("log = %q, want the lock's error", log.String())
	}
}

// A body whose byte count is not Content-Length is an error, and no copy
// lands, even when the gzip stream it holds is whole.
func TestADownloadShorterThanItsLengthLandsNoCopy(t *testing.T) {
	server := newDatasetServer(t, testNow)
	dir := filepath.Join(t.TempDir(), datasetTitleRatings)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("testdata", "imdb", datasetTitleRatings+".tsv.gz"))
	if err != nil {
		t.Fatal(err)
	}
	response := &http.Response{
		Header:        http.Header{"Etag": []string{`"long"`}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)) + 10,
	}
	fetcher, _ := testFetcher(server, filepath.Dir(dir))

	if _, err := fetcher.download(dir, datasetTitleRatings, datasetRecord{}, response, func([][]byte) {}); err == nil {
		t.Error("download = nil, want the error of the byte count")
	}
	if got := namesIn(t, dir); len(got) != 0 {
		t.Errorf("cache = %v, want nothing", got)
	}
}

// A record the container cannot read is no copy, so the run downloads the
// file again.
func TestARecordThatWillNotReadDownloadsAgain(t *testing.T) {
	server := newDatasetServer(t, testNow)
	cache := t.TempDir()
	writeFile(t, filepath.Join(cache, datasetTitleRatings, datasetRecordName), "not json")
	fetcher, _ := testFetcher(server, cache)

	readIDs(t, fetcher, datasetTitleRatings)

	if got := server.log(); !slices.Equal(got, []string{"GET title.ratings 200"}) {
		t.Errorf("requests = %v, want one download", got)
	}
}

// A request that waits for the pace stops waiting when the context ends.
func TestAWaitForThePaceEndsWithTheContext(t *testing.T) {
	server := newDatasetServer(t, testNow)
	fetcher, _ := testFetcher(server, "")
	fetcher.pace, fetcher.last = time.Hour, time.Now()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := fetcher.read(ctx, datasetTitleRatings, func([][]byte) {}); err == nil {
		t.Error("read = nil, want the context's error")
	}
}
