package main

// What these tests read: the files each site answers for one trailer, the
// line the environment builds, and the stream one pull writes.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The two sources a pick reads, with no request behind them.
func testTrailerSources(t *testing.T) map[string]trailerSource {
	t.Helper()
	return newTrailerFetchLine(
		[]string{providerBlockArchive, providerBlockPeerTube},
		func(name string) string {
			if name == providerEndpointVariable(providerBlockPeerTube) {
				return "https://videos.example"
			}
			return ""
		}, nil).sources
}

// Every fetcher answers for the site it is keyed under, so the gap query's
// list of sites and the line's own keys are one set.
func TestEveryFetcherAnswersForTheSiteItIsKeyedUnder(t *testing.T) {
	for site, entry := range trailerFetchers {
		t.Run(site, func(t *testing.T) {
			if answered := entry.build("https://one.example", "", nil).fetcher.site(); answered != site {
				t.Errorf("the fetcher answers for %q, want %q", answered, site)
			}
		})
	}
	if !slices.IsSorted(trailerFetchSites()) || len(trailerFetchSites()) != len(trailerFetchers) {
		t.Errorf("the gap reads %v, want one sorted name per fetcher", trailerFetchSites())
	}
}

// A site with no fetcher yet is one map entry: the gap query names it and the
// line builds it, with nothing else in this fact changed.
func TestANewSitePlugsInThroughTheFetcherTable(t *testing.T) {
	const site, block = "drills.example", "drillsblock"
	registerTrailerFetcher(t, site, block)

	if held := trailerFileGapQuery(); !strings.Contains(held, "'"+site+"'") {
		t.Errorf("the gap reads %q, want the new site among its sites", held)
	}
	line := newTrailerFetchLine([]string{block}, func(string) string { return "" }, nil)
	source, held := line.sources[site]
	if !held {
		t.Fatalf("the line holds %v, want the new site", line.sources)
	}
	files, err := source.fetcher.files(t.Context(), trailerRow{Site: site, Key: "one"})
	if err != nil || len(files) != 1 || files[0].Pull != trailerPullDirect {
		t.Errorf("the site answers %+v, %v, want the one file it holds", files, err)
	}
}

// A fetcher of one site, held in the table for the length of one test.
func registerTrailerFetcher(t *testing.T, site, block string) {
	t.Helper()
	trailerFetchers[site] = trailerFetcherEntry{
		block: block,
		build: func(base, _ string, record *tallies) trailerSource {
			client := newArchiveClient(base)
			client.recordTo(record)
			return trailerSource{fetcher: drillFetcher{name: site},
				requests: &client.providerRequests}
		},
	}
	t.Cleanup(func() { delete(trailerFetchers, site) })
}

// One site that holds one file, which is all a new site has to answer.
type drillFetcher struct {
	name string
}

func (d drillFetcher) site() string { return d.name }

func (d drillFetcher) files(context.Context, trailerRow) ([]trailerFile, error) {
	return []trailerFile{{URL: "https://" + d.name + "/one.mp4", Height: 1080,
		Pull: trailerPullDirect}}, nil
}

// The line takes the sites the source order and the settings reach.
func TestTheFetchLineTakesTheSitesItCanReach(t *testing.T) {
	cases := []struct {
		name   string
		blocks []string
		env    map[string]string
		want   []string
	}{
		{
			name:   "the archive, which needs no setting of its own",
			blocks: []string{providerBlockArchive}, env: map[string]string{},
			want: []string{trailerSiteArchive},
		},
		{
			name: "peertube with its address", blocks: []string{providerBlockPeerTube},
			env:  map[string]string{providerEndpointVariable(providerBlockPeerTube): "https://videos.example"},
			want: []string{trailerSitePeerTube},
		},
		{
			name: "peertube with no address is skipped", blocks: []string{providerBlockPeerTube},
			env: map[string]string{}, want: []string{},
		},
		{
			name: "a block that holds no video is skipped", blocks: []string{providerBlockTMDb},
			env:  map[string]string{tmdbTokenVariable: "a-key"},
			want: []string{},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			line := newTrailerFetchLine(test.blocks,
				func(name string) string { return test.env[name] }, nil)

			sites := []string{}
			for site := range line.sources {
				sites = append(sites, site)
			}
			slices.Sort(sites)
			if !slices.Equal(sites, test.want) {
				t.Errorf("the line holds %v, want %v", sites, test.want)
			}
		})
	}
}

// One instance that answers the video its own file is on, so a line built
// from the environment reaches it for both calls.
func fakeTrailerInstance(t *testing.T, status int) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, peertubeVideoPath) {
			_, _ = fmt.Fprintf(w, `{"uuid":"7f2d3e89","files":[{"resolution":{"id":1080},`+
				`"size":11,"fileDownloadUrl":%q}]}`, "http://"+r.Host+"/download/one.mp4")
			return
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "video bytes")
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// Every request of a line built with a recorder counts under the provider
// that answered and the class of its answer: the call that reads the files,
// and the pull of the file itself.
func TestAFetchLineCountsTheRequestsOfItsSite(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   map[string]float64
	}{
		{name: "the site answers the file", status: http.StatusOK,
			want: map[string]float64{"2xx": 2}},
		{name: "the site refuses the file", status: http.StatusForbidden,
			want: map[string]float64{"2xx": 1, "4xx": 1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			address := fakeTrailerInstance(t, test.status)
			record := newTallies(nil, "house/movies", workerEnrich, "enrich-1",
				trailerFileContainerName, time.Now())
			line := newTrailerFetchLine([]string{providerBlockPeerTube}, func(name string) string {
				return map[string]string{
					providerEndpointVariable(providerBlockPeerTube): address}[name]
			}, record)
			source := line.sources[trailerSitePeerTube]

			files, err := source.fetcher.files(t.Context(),
				trailerRow{Site: trailerSitePeerTube, Key: "7f2d3e89"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = source.pull(t.Context(), files[0].URL, filepath.Join(t.TempDir(), ".pull"))
			if err != nil && test.status == http.StatusOK {
				t.Fatal(err)
			}

			held := record.totals()
			for class, want := range test.want {
				counted := tallyCell{metric: tallyProviderRequests,
					labels: "provider=" + providerBlockPeerTube + ",status=" + class}
				if held[counted] != want {
					t.Errorf("%v = %v, want %v", counted, held[counted], want)
				}
			}
		})
	}
}

// The archive's video files, with the height and the size the metadata states
// and the download address of each.
func TestTheArchiveFetcherReadsTheItemsVideoFiles(t *testing.T) {
	client, fake := newFakeArchive(t, "{}")
	fetcher := archiveTrailerFetcher{client: client}

	files, err := fetcher.files(t.Context(),
		trailerRow{Site: trailerSiteArchive, Key: "turner_video_47594"})
	if err != nil {
		t.Fatal(err)
	}

	want := []trailerFile{
		{URL: client.base + "/download/turner_video_47594/47594.mp4", Height: 1080, Size: 110550963},
		{URL: client.base + "/download/turner_video_47594/47594.ia.mp4", Height: 360, Size: 10550963},
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("the item holds %+v, want %+v", files, want)
	}
	if paths := fake.paths(); !slices.Equal(paths, []string{archiveMetadataPath + "turner_video_47594"}) {
		t.Errorf("the fetcher asked %v, want the item's metadata", paths)
	}
}

// An item the archive refuses is an error the pull records.
func TestTheArchiveFetcherReportsARefusedItem(t *testing.T) {
	client, fake := newFakeArchive(t, "{}")
	fake.items["gone"] = fakeArchiveItem{status: http.StatusNotFound, body: "no such item"}
	fetcher := archiveTrailerFetcher{client: client}

	_, err := fetcher.files(t.Context(), trailerRow{Site: trailerSiteArchive, Key: "gone"})

	if !answeredWith(err, http.StatusNotFound) {
		t.Errorf("err = %v, want the archive's own answer", err)
	}
}

// An instance's own files where it states any, and the files of its streaming
// playlist where it states none.
func TestThePeerTubeFetcherReadsTheVideosFiles(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		want    []trailerFile
	}{
		{
			name: "the instance states its own files", fixture: "peertube-video-files.json",
			want: []trailerFile{
				{URL: "https://trailers.example.net/download/videos/7f2d3e89-720.mp4",
					Height: 720, Size: 41234567},
				{URL: "https://trailers.example.net/download/videos/7f2d3e89-1080.mp4",
					Height: 1080, Size: 91234567},
			},
		},
		{
			name:    "the streaming playlist's files, less the audio the instance states as resolution 0",
			fixture: "peertube-video-hls.json",
			want: []trailerFile{
				{URL: "https://trailers.example.net/download/streaming-playlists/hls/videos/8a3e4f90-480-fragmented.mp4",
					Height: 480, Size: 12345678},
				{URL: "https://trailers.example.net/download/streaming-playlists/hls/videos/8a3e4f90-1080-fragmented.mp4",
					Height: 1080, Size: 88345678},
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakePeerTube(t, trailerFixture(t, test.fixture))
			fetcher := peertubeTrailerFetcher{client: client}

			files, err := fetcher.files(t.Context(),
				trailerRow{Site: trailerSitePeerTube, Key: "7f2d3e89"})
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(files, test.want) {
				t.Errorf("the video holds %+v, want %+v", files, test.want)
			}
			if asked := fake.requests[0].Path; asked != peertubeVideoPath+"7f2d3e89" {
				t.Errorf("the fetcher asked %q, want the video's own path", asked)
			}
		})
	}
}

// One server that answers a pull with the body, the status, and the pause a
// case names.
type fakeDownload struct {
	body   string
	status int
	pause  time.Duration
}

// The source whose pull reaches that server.
func newFakeDownload(t *testing.T, download *fakeDownload) (trailerSource, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if download.pause > 0 {
			select {
			case <-time.After(download.pause):
			case <-r.Context().Done():
				return
			}
		}
		if download.status != 0 {
			w.WriteHeader(download.status)
		}
		_, _ = io.WriteString(w, download.body)
	}))
	t.Cleanup(server.Close)

	client := newArchiveClient(server.URL)
	client.http = server.Client()
	client.interval = 0
	source := trailerSource{fetcher: archiveTrailerFetcher{client: client},
		requests: &client.providerRequests}
	return source, server.URL + "/download/one/one.mp4"
}

// The pull writes the answer into the temporary and states its size.
func TestAPullWritesTheAnswerIntoItsTemporary(t *testing.T) {
	source, address := newFakeDownload(t, &fakeDownload{body: "a whole trailer"})
	path := filepath.Join(t.TempDir(), ".pull")

	written, err := source.pull(t.Context(), address, path)
	if err != nil {
		t.Fatal(err)
	}

	if written != int64(len("a whole trailer")) {
		t.Errorf("the pull wrote %d bytes, want %d", written, len("a whole trailer"))
	}
	held, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(held) != "a whole trailer" {
		t.Errorf("the file holds %q, want the answer", held)
	}
}

// A site that asks for a slower pace is asked again, and the file the second
// answer holds is the one the pull writes.
func TestAPullTakesTheSitesCooldownAndAsksAgain(t *testing.T) {
	answers := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if answers.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, "a whole trailer")
	}))
	t.Cleanup(server.Close)
	client := newArchiveClient(server.URL)
	client.http = server.Client()
	client.interval = 0
	client.wait = func(context.Context, time.Duration) error { return nil }
	source := trailerSource{fetcher: archiveTrailerFetcher{client: client},
		requests: &client.providerRequests}
	path := filepath.Join(t.TempDir(), ".pull")

	written, err := source.pull(t.Context(), server.URL+"/download/one/one.mp4", path)
	if err != nil {
		t.Fatal(err)
	}

	if written != int64(len("a whole trailer")) || answers.Load() != 2 {
		t.Errorf("the pull wrote %d bytes over %d answers, want %d over 2",
			written, answers.Load(), len("a whole trailer"))
	}
	if held := readFileString(t, path); held != "a whole trailer" {
		t.Errorf("the file holds %q, want the second answer", held)
	}
}

// An answer outside 2xx is the site's own error.
func TestAPullReportsAnAnswerOutsideTwoHundred(t *testing.T) {
	source, address := newFakeDownload(t,
		&fakeDownload{status: http.StatusForbidden, body: "no"})
	path := filepath.Join(t.TempDir(), ".pull")

	_, err := source.pull(t.Context(), address, path)

	if !answeredWith(err, http.StatusForbidden) {
		t.Errorf("err = %v, want the site's own answer", err)
	}
}

// A site that stops answering costs its own timeout and no more.
func TestAPullEndsOnItsOwnTimeout(t *testing.T) {
	was := trailerPullTimeout
	t.Cleanup(func() { trailerPullTimeout = was })
	trailerPullTimeout = 50 * time.Millisecond
	source, address := newFakeDownload(t, &fakeDownload{body: "late", pause: 5 * time.Second})
	path := filepath.Join(t.TempDir(), ".pull")

	_, err := source.pull(t.Context(), address, path)

	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Errorf("err = %v, want the pull's own deadline", err)
	}
}
