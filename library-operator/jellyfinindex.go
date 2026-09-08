package main

// jellyfinindex.go is the map between the catalog's identity of a work and
// Jellyfin's item id, and between a Person name and a Jellyfin user id.
// Jellyfin has no lookup by provider id, so the index is built from one
// recursive listing of every movie, episode, and series. It is the role's
// only state, and a start rebuilds it from nothing.

import (
	"context"
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
	itemsRead time.Time
	usersRead time.Time
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
// The read time is stamped before the call, so a Jellyfin that is down is
// asked once per age and not once per lookup.
func (i *jellyfinIndex) refreshItems(ctx context.Context, age time.Duration) bool {
	i.mutex.Lock()
	if !i.itemsRead.IsZero() && i.now().Sub(i.itemsRead) < age {
		i.mutex.Unlock()
		return false
	}
	i.itemsRead = i.now()
	i.mutex.Unlock()

	items, err := i.api.items(ctx)
	if err != nil {
		i.logf("could not read the items of jellyfin: %v", err)
		return false
	}
	i.hold(items)
	return true
}

// hold turns one listing into the two lookups. The series pass runs first,
// because an episode is keyed on its series' ids and the listing carries the
// two in no order.
func (i *jellyfinIndex) hold(items []jellyfinItem) {
	series := map[string]map[string]string{}
	for _, item := range items {
		if strings.EqualFold(item.Type, jellyfinSeriesType) {
			series[item.ID] = jellyfinProviders(item.ProviderIds)
		}
	}

	movies := map[string]string{}
	episodes := map[string]string{}
	for _, item := range items {
		if strings.EqualFold(item.Type, jellyfinMovieType) {
			for provider, id := range jellyfinProviders(item.ProviderIds) {
				movies[jellyfinAlias(provider, id)] = item.ID
			}
		}
		if strings.EqualFold(item.Type, jellyfinEpisodeType) {
			for provider, id := range series[item.SeriesID] {
				episodes[jellyfinEpisodeAlias(jellyfinAlias(provider, id),
					item.ParentIndexNumber, item.IndexNumber)] = item.ID
			}
		}
	}

	i.mutex.Lock()
	defer i.mutex.Unlock()
	i.movies, i.episodes, i.series = movies, episodes, series
}

// refreshUsers reads the users and replaces the map, under the same floor the
// listing takes.
func (i *jellyfinIndex) refreshUsers(ctx context.Context, age time.Duration) bool {
	i.mutex.Lock()
	if !i.usersRead.IsZero() && i.now().Sub(i.usersRead) < age {
		i.mutex.Unlock()
		return false
	}
	i.usersRead = i.now()
	i.mutex.Unlock()

	users, err := i.api.users(ctx)
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
