package main

// jellyfinindex.go is the map between the catalog's identity of a work and
// Jellyfin's item id, and between a Person name and a Jellyfin user id.
// Jellyfin has no lookup by provider id, so the index is built from two
// recursive listings: every series, then every movie and episode. It is the
// role's only state, and a start rebuilds it from nothing.
//
// A build folds each item into the lookups as it arrives and holds no
// listing. So the memory a build takes is the lookups it ends with. A
// larger library costs the role a larger index and no transient beside it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
)

// How long a build of the index stands, and the floor between two builds. A
// lookup that misses asks for a rebuild at most once a minute, so a work
// Jellyfin does not hold costs one listing a minute and not one a play.
const (
	jellyfinIndexLifetime = time.Hour
	jellyfinIndexFloor    = time.Minute
)

// The wait after one read that failed. A failed read installs nothing, so
// it does not count as a read under the floor: a Jellyfin that refused the
// read at the role's start is read again after this wait, which is shorter
// than one pass of the write loop. Each failure in a row doubles the wait,
// up to the floor.
const jellyfinIndexRetry = 5 * time.Second

// The time one whole build may take, over every page of both listings.
// A build past it fails and installs nothing, so the index stays as it
// was. The budget is many times what a build needs. It bounds a server
// that stopped answering, not one that is slow.
const jellyfinIndexBudget = 2 * time.Minute

// One index: the two lookups the outbound writes need, the series ids the
// inbound webhook needs, the users, and when each half was last read.
type jellyfinIndex struct {
	api *jellyfinAPI
	now func() time.Time
	log io.Writer

	mutex     sync.Mutex
	movies    map[string]string
	episodes  map[string]string
	series    map[string]map[string]string
	users     map[string]string
	itemsRead jellyfinReads
	usersRead jellyfinReads
}

// newJellyfinIndex builds an empty index. Nothing is read until a caller
// asks, and prime is what reads both halves at the role's start.
func newJellyfinIndex(api *jellyfinAPI, now func() time.Time, log io.Writer) *jellyfinIndex {
	return &jellyfinIndex{
		api:      api,
		now:      now,
		log:      log,
		movies:   map[string]string{},
		episodes: map[string]string{},
		series:   map[string]map[string]string{},
		users:    map[string]string{},
	}
}

// prime reads both halves once, so the first webhook and the first write find
// what the server already holds.
func (i *jellyfinIndex) prime(ctx context.Context) {
	i.refreshItems(ctx, 0)
	i.refreshUsers(ctx, 0)
}

// One alias as a key: the provider in lower case and the id it gave the work.
func jellyfinAlias(provider, id string) string {
	return strings.ToLower(provider) + ":" + id
}

// One episode as a key: the series' alias, the season, and the episode. The
// catalog gives an episode play the series' identity with the two numbers
// beside it, so that is what the index is keyed on.
func jellyfinEpisodeAlias(alias string, season, episode int) string {
	return fmt.Sprintf("%s/%d/%d", alias, season, episode)
}

// The provider ids of one item, with the provider names in lower case and
// every empty id dropped. Jellyfin writes Tmdb, Imdb, and Tvdb, and the
// catalog writes tmdb, imdb, and tvdb.
func jellyfinProviders(ids map[string]string) map[string]string {
	held := map[string]string{}
	for provider, id := range ids {
		if id == "" {
			continue
		}
		held[strings.ToLower(provider)] = id
	}
	if len(held) == 0 {
		return nil
	}
	return held
}

// itemFor answers the Jellyfin item id of one work. It rebuilds an index
// older than an hour, tries every alias, and on a miss rebuilds once more
// under the floor and tries again.
func (i *jellyfinIndex) itemFor(ctx context.Context, aliases map[string]string, season, episode int) (string, bool) {
	i.refreshItems(ctx, jellyfinIndexLifetime)
	if item, found := i.find(aliases, season, episode); found {
		return item, true
	}
	if !i.refreshItems(ctx, jellyfinIndexFloor) {
		return "", false
	}
	return i.find(aliases, season, episode)
}

// find takes the first alias that names an item. The aliases are tried in
// name order, so two providers that both name the work answer the same item
// every time.
func (i *jellyfinIndex) find(aliases map[string]string, season, episode int) (string, bool) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	for _, provider := range slices.Sorted(maps.Keys(aliases)) {
		key := jellyfinAlias(provider, aliases[provider])
		if season > 0 || episode > 0 {
			if item, held := i.episodes[jellyfinEpisodeAlias(key, season, episode)]; held {
				return item, true
			}
			continue
		}
		if item, held := i.movies[key]; held {
			return item, true
		}
	}
	return "", false
}

// userFor answers the Jellyfin user id of one Person name, without case. A
// miss reads the users again, at most once a minute, because a person added
// today is a miss today.
func (i *jellyfinIndex) userFor(ctx context.Context, person string) (string, bool) {
	if user, known := i.user(person); known {
		return user, true
	}
	if !i.refreshUsers(ctx, jellyfinIndexFloor) {
		return "", false
	}
	return i.user(person)
}

func (i *jellyfinIndex) user(person string) (string, bool) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	user, known := i.users[strings.ToLower(person)]
	return user, known
}

// seriesAliases answers the provider ids of one series. The webhook carries
// an episode's own ids and the series' id alone, so the series is read once
// and held.
func (i *jellyfinIndex) seriesAliases(ctx context.Context, series string) map[string]string {
	if series == "" {
		return nil
	}
	i.mutex.Lock()
	aliases, held := i.series[series]
	i.mutex.Unlock()
	if held {
		return aliases
	}

	item, err := i.api.item(ctx, series)
	if err != nil {
		i.logf("could not read the series %s from jellyfin: %v", series, err)
		return nil
	}
	aliases = jellyfinProviders(item.ProviderIds)
	i.mutex.Lock()
	i.series[series] = aliases
	i.mutex.Unlock()
	return aliases
}

// refreshItems reads the listing and replaces the index, unless the last read
// is younger than the age the caller states. It answers whether it replaced
// the index, so a caller knows a second lookup is worth making.
// A read that fails waits out jellyfinReads' retry and not the age, so a
// Jellyfin that is down at the role's start is read again within seconds.
func (i *jellyfinIndex) refreshItems(ctx context.Context, age time.Duration) bool {
	i.mutex.Lock()
	if !i.itemsRead.begin(i.now(), age) {
		i.mutex.Unlock()
		return false
	}
	i.mutex.Unlock()

	built, err := i.build(ctx)
	i.mutex.Lock()
	i.itemsRead.end(i.now(), err == nil)
	i.mutex.Unlock()
	if err != nil {
		i.logf("could not read the items of jellyfin: %v", err)
		return false
	}
	i.hold(built)
	return true
}

// When one of the index's two reads last ran and how it ended. The floor
// counts from the start of the last read that succeeded. A read that fails
// installs nothing, so it does not count: the next read may start once the
// retry wait has run, and each failure in a row doubles that wait up to the
// floor. So a Jellyfin that refuses every read is asked at most once a
// minute after a few failures, and never in a loop.
//
// A read in flight holds off every other caller, so two lookups at once make
// one request and not two.
type jellyfinReads struct {
	read     time.Time
	started  time.Time
	running  bool
	failures int
	retryAt  time.Time
}

// begin answers whether a read may start now, for a caller that accepts a
// read as old as age, and marks the read in flight when it may. The caller
// holds the index's mutex.
func (r *jellyfinReads) begin(now time.Time, age time.Duration) bool {
	switch {
	case r.running:
		return false
	case r.failures > 0 && now.Before(r.retryAt):
		return false
	case r.failures == 0 && !r.read.IsZero() && now.Sub(r.read) < age:
		return false
	}
	r.running, r.started = true, now
	return true
}

// end records how the read that begin started ended. The caller holds the
// index's mutex.
func (r *jellyfinReads) end(now time.Time, succeeded bool) {
	r.running = false
	if succeeded {
		r.read, r.failures = r.started, 0
		return
	}
	r.failures++
	r.retryAt = now.Add(jellyfinRetryWait(r.failures))
}

// jellyfinRetryWait is the wait after this many failed reads in a row: the
// retry wait, doubled for each failure after the first, and never past the
// floor.
func jellyfinRetryWait(failures int) time.Duration {
	wait := jellyfinIndexRetry
	for range failures - 1 {
		if wait >= jellyfinIndexFloor {
			break
		}
		wait *= 2
	}
	return min(wait, jellyfinIndexFloor)
}

// The three lookups one build makes. A build fills these rather than the maps
// the role reads, so a listing the server cuts short leaves the index as it
// was. A half-built index would lack every work in the failed listing, and
// a lookup that misses writes nothing to Jellyfin.
type jellyfinLookups struct {
	movies   map[string]string
	episodes map[string]string
	series   map[string]map[string]string
}

// build reads the two listings and folds each item as it arrives. The series
// listing comes first, because an episode is keyed on its series' provider
// ids and only the series carries them.
func (i *jellyfinIndex) build(ctx context.Context) (jellyfinLookups, error) {
	budget, done := jellyfinBuildBudget(ctx)
	defer done()

	built := jellyfinLookups{
		movies:   map[string]string{},
		episodes: map[string]string{},
		series:   map[string]map[string]string{},
	}
	if err := i.api.items(budget, jellyfinSeriesListing, func(item jellyfinItem) {
		if strings.EqualFold(item.Type, jellyfinSeriesType) {
			built.series[item.ID] = jellyfinProviders(item.ProviderIds)
		}
	}); err != nil {
		return built, err
	}
	return built, i.api.items(budget, jellyfinWorksListing, built.fold)
}

// The context one build runs on. A build makes one request per page, and
// a caller's deadline is sized for one request. The budget drops the
// caller's deadline and keeps the caller's cancel. A build that ignored a
// cancel would keep the pod running past its stop signal.
func jellyfinBuildBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	budget, done := context.WithTimeout(context.WithoutCancel(ctx), jellyfinIndexBudget)
	// A caller's deadline that passed bounds the caller's own work, not
	// the build. Only a cancel ends the build.
	stop := context.AfterFunc(ctx, func() {
		if errors.Is(ctx.Err(), context.Canceled) {
			done()
		}
	})
	return budget, func() {
		stop()
		done()
	}
}

// fold adds one item to the lookups under its keys. A movie is keyed on
// every provider id it has, and an episode on its series' provider ids plus
// the season and episode numbers.
func (l jellyfinLookups) fold(item jellyfinItem) {
	switch {
	case strings.EqualFold(item.Type, jellyfinMovieType):
		for provider, id := range jellyfinProviders(item.ProviderIds) {
			l.movies[jellyfinAlias(provider, id)] = item.ID
		}
	case strings.EqualFold(item.Type, jellyfinEpisodeType):
		for provider, id := range l.series[item.SeriesID] {
			l.episodes[jellyfinEpisodeAlias(jellyfinAlias(provider, id),
				item.ParentIndexNumber, item.IndexNumber)] = item.ID
		}
	}
}

// hold puts one finished build in place of the index the role was reading.
func (i *jellyfinIndex) hold(built jellyfinLookups) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	i.movies, i.episodes, i.series = built.movies, built.episodes, built.series
}

// refreshUsers reads the users and replaces the map, under the same floor and
// the same retry the listing takes.
func (i *jellyfinIndex) refreshUsers(ctx context.Context, age time.Duration) bool {
	i.mutex.Lock()
	if !i.usersRead.begin(i.now(), age) {
		i.mutex.Unlock()
		return false
	}
	i.mutex.Unlock()

	users, err := i.api.users(ctx)
	i.mutex.Lock()
	i.usersRead.end(i.now(), err == nil)
	i.mutex.Unlock()
	if err != nil {
		i.logf("could not read the users of jellyfin: %v", err)
		return false
	}

	held := map[string]string{}
	for _, user := range users {
		held[strings.ToLower(user.Name)] = user.ID
	}
	i.mutex.Lock()
	defer i.mutex.Unlock()
	i.users = held
	return true
}

func (i *jellyfinIndex) logf(format string, args ...any) {
	if i.log == nil {
		return
	}
	fmt.Fprintf(i.log, "library.liken.sh: "+format+"\n", args...)
}
