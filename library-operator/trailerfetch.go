package main

// trailerfetch.go is what a site answers about one trailer's own video files,
// and the paced stream that pulls the chosen one onto the volume.

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"
)

// How one file is pulled. A fetcher names the way its file needs, and
// trailerPullers holds one function per way. The steps after the temporary,
// which are the remux, the check, and the write, are one path every way
// feeds.
type trailerPull string

// The one way this release pulls: a GET of the whole file.
const trailerPullDirect trailerPull = "direct"

// One video file a site holds, in the facts the pick reads and the way the
// pull takes it.
type trailerFile struct {
	URL    string
	Height int
	Size   int64
	// The way the pull takes this file. Empty is the direct GET.
	Pull trailerPull
}

// What one site answers about a trailer's own video files.
type trailerFetcher interface {
	site() string
	files(ctx context.Context, entry trailerRow) ([]trailerFile, error)
}

// The fetcher of one site and the paced client its pull goes through.
type trailerSource struct {
	fetcher  trailerFetcher
	requests *providerRequests
}

// One site this fact can fetch from: the provider block whose settings reach
// the container, and how to build the source out of them.
type trailerFetcherEntry struct {
	block string
	build func(base, token string, record *tallies) trailerSource
}

// Every site this fact can fetch from, keyed by the site its trailers rows
// carry. A new site is one file with one trailerFetcher and one entry here.
// Nothing else names a site.
var trailerFetchers = map[string]trailerFetcherEntry{
	trailerSiteArchive: {
		block: providerBlockArchive,
		build: func(base, _ string, record *tallies) trailerSource {
			client := newArchiveClient(base)
			client.recordTo(record)
			return trailerSource{fetcher: archiveTrailerFetcher{client: client},
				requests: &client.providerRequests}
		},
	},
	trailerSitePeerTube: {
		block: providerBlockPeerTube,
		build: func(base, _ string, record *tallies) trailerSource {
			client := newPeertubeClient(base)
			client.recordTo(record)
			return trailerSource{fetcher: peertubeTrailerFetcher{client: client},
				requests: &client.providerRequests}
		},
	},
}

// The sites the gap query reads, straight off the table's own keys.
func trailerFetchSites() []string {
	return slices.Sorted(maps.Keys(trailerFetchers))
}

// The provider blocks that serve the table's sites. A Library's sources have
// to name one of them before this fact can pull anything.
func trailerFetchBlocks() map[string]bool {
	blocks := make(map[string]bool, len(trailerFetchers))
	for _, entry := range trailerFetchers {
		blocks[entry.block] = true
	}
	return blocks
}

// The sources one container holds, by site.
type trailerFetchLine struct {
	sources map[string]trailerSource
}

// The line applies the selection rules of recordingAnswerers over the blocks
// the table names, and the site each source answers for is the key it lands
// under. Every client the line builds counts its requests into the recorder
// the container holds.
func newTrailerFetchLine(blocks []string, value func(string) string,
	record *tallies) *trailerFetchLine {
	byBlock := make(map[string]func(base, token string, record *tallies) trailerSource,
		len(trailerFetchers))
	for _, entry := range trailerFetchers {
		byBlock[entry.block] = entry.build
	}
	line := &trailerFetchLine{sources: map[string]trailerSource{}}
	for _, source := range recordingAnswerers(blocks, value, record, byBlock) {
		line.sources[source.fetcher.site()] = source
	}
	return line
}

// How long one file's pull may run. It is a variable so that no test waits it
// out.
var trailerPullTimeout = 10 * time.Minute

// The most one pulled file may take on the volume. It is a variable so that
// no test answers half a gigabyte.
var trailerPullLimit int64 = 512 << 20

// One function per way a file is pulled. A way that needs a playlist read, a
// torrent, or a tool of its own is one entry here and one function, and the
// steps after it do not change.
var trailerPullers = map[trailerPull]func(ctx context.Context, source trailerSource,
	file trailerFile, path string) (int64, error){
	trailerPullDirect: func(ctx context.Context, source trailerSource,
		file trailerFile, path string) (int64, error) {
		return source.pull(ctx, file.URL, path)
	},
}

// The way the file names, or the direct GET where it names none. A way this
// image does not hold is an error the attempt records.
func pullTrailerBytes(ctx context.Context, source trailerSource,
	file trailerFile, path string) (int64, error) {
	way := file.Pull
	if way == "" {
		way = trailerPullDirect
	}
	puller, held := trailerPullers[way]
	if !held {
		return 0, fmt.Errorf("no puller of this image takes a %s file", way)
	}
	return puller(ctx, source, file, path)
}

// The stream of one chosen file into a temporary on the volume.
func (s trailerSource) pull(ctx context.Context, address, path string) (int64, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, volumeFilePerm)
	if err != nil {
		return 0, err
	}
	written, err := s.requests.fetchInto(ctx, address, file, trailerPullTimeout, trailerPullLimit)
	if err != nil {
		file.Close()
		return written, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return written, err
	}
	return written, file.Close()
}
