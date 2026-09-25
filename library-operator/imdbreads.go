package main

// imdbreads.go is the nfo container's read of the IMDb datasets. The
// container has its whole rating.imdb gap before it asks for anything, so it
// reads each file it needs once, from start to end, and keeps a row only
// when the row's id is in the gap. Memory holds the gap and the matching
// rows, and not the file. The read starts when the container starts, and
// the other nfo facts run while it works.

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The variable the container reads IMDb's address from. A test points it at
// a server of its own. An empty value is IMDb's own address.
const imdbEndpointVariable = "IMDB_ENDPOINT"

// One title of the gap: a movie or a series by its own IMDb id, or an episode
// by its own IMDb id or by its place under a series that has one.
type imdbTarget struct {
	id    string
	imdb  string
	title string
	// An episode's file, relative to the library root, and its place.
	path    string
	series  string
	season  int
	episode int
	// Whether the episode's IMDb id came from title.episode in this run, so
	// the fact writes it to the episode's ledger.
	found bool
}

func (t imdbTarget) isEpisode() bool { return strings.HasPrefix(t.id, scopeEpisode+":") }

// What the reads left: the rating of every IMDb id of the gap that
// title.ratings holds, and the Last-Modified time of the copy they came from.
// done closes when the reads end, and err is why they ended early.
type datasetReads struct {
	done     chan struct{}
	targets  map[string]*imdbTarget
	ratings  map[string]titleRating
	modified time.Time
	err      error
	// The one fetcher of the container, so its pace holds across reads.
	fetcher *datasetFetcher
}

// The reads end before the fact asks, or the fact's context ends first.
func (r *datasetReads) wait(ctx context.Context) error {
	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// startDatasetReads starts the reads of a container that runs rating.imdb
// and names the imdb block among its sources. A gap with no title sends no
// request, and a container that runs no dataset fact reads nothing.
func (e *enricher) startDatasetReads(ctx context.Context, facts []string) {
	sources := commaNames(os.Getenv(librarySourcesVariable))
	if !slices.Contains(facts, factRatingIMDb) || !slices.Contains(sources, providerBlockIMDb) {
		return
	}
	reads := &datasetReads{done: make(chan struct{}), targets: map[string]*imdbTarget{},
		ratings: map[string]titleRating{}, fetcher: e.datasetFetcher()}
	e.datasets = reads
	ids, err := e.gaps(ctx, factRatingIMDb, time.Now().UTC())
	if err == nil {
		reads.targets, err = e.imdbTargets(ctx, ids)
	}
	if err != nil || len(reads.targets) == 0 {
		reads.err = err
		close(reads.done)
		return
	}
	go func() {
		defer close(reads.done)
		if reads.err = reads.run(ctx, e.logf); reads.err != nil {
			e.logf("could not read the IMDb datasets, so this run leaves the rating.imdb gap: %v", reads.err)
		}
	}()
}

// coverDatasetGap reads the datasets again for the titles of a pass that the
// reads so far do not cover. A phase runs passes, and a title the identity
// phase names during the Job enters the gap after the container's first
// read. The read keeps the rows of those titles alone, and it runs inside the
// pass, because the pass answers them next. A read that fails ends the imdb
// block's work for the rest of the container, as a failed first read does.
func (e *enricher) coverDatasetGap(ctx context.Context, ids []string) {
	reads := e.datasets
	if reads == nil || reads.wait(ctx) != nil {
		return
	}
	var missing []string
	for _, id := range ids {
		if _, held := reads.targets[id]; !held {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return
	}
	targets, err := e.imdbTargets(ctx, missing)
	batch := &datasetReads{targets: targets, ratings: map[string]titleRating{}, fetcher: reads.fetcher}
	if err == nil {
		err = batch.run(ctx, e.logf)
	}
	if err != nil {
		e.logf("could not read the IMDb datasets, so this run leaves the rating.imdb gap: %v", err)
		reads.err = err
		return
	}
	maps.Copy(reads.targets, batch.targets)
	maps.Copy(reads.ratings, batch.ratings)
	if !batch.modified.IsZero() {
		reads.modified = batch.modified
	}
}

// The fetcher of this container, on the cache the operator mounted, or on
// none.
func (e *enricher) datasetFetcher() *datasetFetcher {
	base := os.Getenv(imdbEndpointVariable)
	if base == "" {
		base = imdbDatasetsBase
	}
	return &datasetFetcher{
		base:   strings.TrimSuffix(base, "/"),
		cache:  os.Getenv(datasetsCacheVariable),
		client: &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: time.Minute}},
		writer: e.writer,
		logf:   e.logf,
		pace:   blockOf(providerBlockIMDb).pace,
	}
}

// The two reads, in order: title.episode for the episodes that have no IMDb
// id yet, then title.ratings for every id the gap holds.
func (r *datasetReads) run(ctx context.Context, logf func(string, ...any)) error {
	fetcher := r.fetcher
	if unplaced := r.unplacedSeries(); len(unplaced) > 0 {
		started := time.Now()
		places := map[string]string{}
		if _, err := fetcher.read(ctx, datasetTitleEpisode, func(cells [][]byte) {
			if unplaced[datasetCell(cells, 1)] {
				places[episodePlace(datasetCell(cells, 1), datasetCell(cells, 2), datasetCell(cells, 3))] =
					datasetCell(cells, 0)
			}
		}); err != nil {
			return fmt.Errorf("reading %s: %w", datasetTitleEpisode, err)
		}
		found := r.place(places)
		logf("read %s in %s and found the IMDb id of %d episodes", datasetTitleEpisode,
			time.Since(started).Round(time.Millisecond), found)
	}
	wanted := map[string]bool{}
	for _, target := range r.targets {
		if target.imdb != "" {
			wanted[target.imdb] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	started := time.Now()
	modified, err := fetcher.read(ctx, datasetTitleRatings, func(cells [][]byte) {
		id := datasetCell(cells, 0)
		if !wanted[id] {
			return
		}
		value, err := strconv.ParseFloat(datasetCell(cells, 1), 64)
		if err != nil {
			return
		}
		votes, _ := strconv.Atoi(datasetCell(cells, 2))
		r.ratings[id] = titleRating{Value: value, Votes: votes}
	})
	if err != nil {
		return fmt.Errorf("reading %s: %w", datasetTitleRatings, err)
	}
	r.modified = modified
	logf("read %s in %s and kept %d of the %d ratings the gap asks for", datasetTitleRatings,
		time.Since(started).Round(time.Millisecond), len(r.ratings), len(wanted))
	return nil
}

// The series whose episodes need their ids from title.episode.
func (r *datasetReads) unplacedSeries() map[string]bool {
	series := map[string]bool{}
	for _, target := range r.targets {
		if target.isEpisode() && target.imdb == "" && target.series != "" {
			series[target.series] = true
		}
	}
	return series
}

// The key one episode's place takes in title.episode's rows.
func episodePlace(series, season, episode string) string {
	return series + "/" + season + "/" + episode
}

// The ids title.episode gave, onto the episodes whose place matches, and the
// count it placed.
func (r *datasetReads) place(places map[string]string) int {
	found := 0
	for _, target := range r.targets {
		if !target.isEpisode() || target.imdb != "" {
			continue
		}
		id := places[episodePlace(target.series, strconv.Itoa(target.season), strconv.Itoa(target.episode))]
		if id != "" {
			target.imdb, target.found = id, true
			found++
		}
	}
	return found
}

// Every title of the given rating.imdb gap, with the
// IMDb id the catalog's aliases hold for it. An episode with no id of its own
// takes the id an earlier run wrote to its ledger, and otherwise its series'
// id, which the read of title.episode places it under.
func (e *enricher) imdbTargets(ctx context.Context, ids []string) (map[string]*imdbTarget, error) {
	targets := map[string]*imdbTarget{}
	if len(ids) == 0 {
		return targets, nil
	}
	for _, id := range ids {
		targets[id] = &imdbTarget{id: id}
	}
	aliases := map[string]string{}
	err := e.catalog.stream(ctx, `SELECT item, alias FROM aliases WHERE library = ? AND alias LIKE '%:imdb:%'`,
		[]any{e.library}, func(cells []any) error {
			item, _ := cells[0].(string)
			alias, _ := cells[1].(string)
			aliases[item] = alias[strings.LastIndex(alias, ":")+1:]
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("reading the IMDb ids of %s: %w", e.library, err)
	}
	for id, target := range targets {
		target.imdb = aliases[id]
	}
	err = e.catalog.stream(ctx, `SELECT id, path, title, series, season, episode FROM episodes WHERE library = ?`,
		[]any{e.library}, func(cells []any) error {
			id, _ := cells[0].(string)
			target, held := targets[id]
			if !held {
				return nil
			}
			target.path, _ = cells[1].(string)
			target.title, _ = cells[2].(string)
			series, _ := cells[3].(string)
			target.series = aliases[series]
			target.season, target.episode = int(cellNumber(cells[4])), int(cellNumber(cells[5]))
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("reading the episodes of %s: %w", e.library, err)
	}
	for _, target := range targets {
		if target.isEpisode() && target.imdb == "" && target.path != "" {
			target.imdb = e.ledgerIMDbID(target.path)
		}
	}
	return targets, nil
}

// The IMDb id an earlier run wrote to the episode's ledger, or none.
func (e *enricher) ledgerIMDbID(path string) string {
	absolute := filepath.Join(e.root, path)
	ledger, err := readLikenLedger(filepath.Dir(absolute), factRatingIMDb)
	if err != nil {
		return ""
	}
	item, held := ledger.itemAt(filepath.Base(absolute))
	if !held {
		return ""
	}
	return item.ID[providerBlockIMDb]
}
