package main

// imdbratinggap.go is the gap of the rating.imdb fact, which differs from
// every other nfo gap in two ways. The imdb block rates episodes, and no other
// block does, so the gap holds episodes only where imdb answers the fact. And
// IMDb replaces title.ratings every day, so a rating the datasets wrote is a
// gap again after 30 days when a newer file exists. A rating another tool
// wrote into the .nfo file has no attempt, and the container reads it once.

import (
	"context"
	"os"
	"time"
)

// The two parameters the rating.imdb query binds after the release date: the
// dataset cutoff as ?6 and the episode switch as ?7. An attempt older than
// the retry window whose dataset time is older than the cutoff is a gap
// again, and a cutoff above 0 also opens a rating that has no attempt. A
// cutoff of 0 opens neither, because no dataset time is below zero.
type ratingGapScope struct {
	reopen   int64
	episodes bool
}

func imdbRatingGapParams(scope ratingGapScope) []any {
	return []any{scope.reopen, flag(scope.episodes)}
}

// The variable that carries the Last-Modified time of title.ratings into the
// enricher, from the provider's status.imdb.datasets. The operator writes it
// only where an imdb provider answers the rating for the Library, so its
// presence says the gap holds episodes and opens old ratings. An empty value
// is a provider with no time in its status yet, and the gap then opens a
// rating on the 30 days alone.
const imdbRatingsModifiedVariable = "IMDB_RATINGS_MODIFIED"

func imdbRatingEnv(library *Library, providers providerSet) []EnvVar {
	provider := imdbRatingAnswerer(library, providers)
	if provider == nil {
		return nil
	}
	value := ""
	if ratings, held := provider.Status.IMDb.dataset(datasetTitleRatings); held && !ratings.LastModified.IsZero() {
		value = ratings.LastModified.UTC().Format(time.RFC3339)
	}
	return []EnvVar{{Name: imdbRatingsModifiedVariable, Value: value}}
}

// The scope the container reads its gap with.
func ratingScopeFromEnvironment(now time.Time) ratingGapScope {
	value, held := os.LookupEnv(imdbRatingsModifiedVariable)
	if !held {
		return ratingGapScope{}
	}
	modified, err := time.Parse(time.RFC3339, value)
	if err != nil || modified.IsZero() {
		return ratingGapScope{reopen: now.Unix(), episodes: true}
	}
	return ratingGapScope{reopen: modified.Unix(), episodes: true}
}

// The gap query. A movie or a series needs a provider id, as every nfo gap
// does. An episode needs a series with a provider id, and it is left out
// where its file holds more than one episode, because the ledger of a season
// folder keys an attempt on the file and names the first episode alone.
func imdbRatingGapQuery() string {
	missing := `instr(nfo_facts, '` + nfoFactSeparator + factRatingIMDb + nfoFactSeparator + `') = 0`
	reopened := `id IN (SELECT item FROM attempts WHERE attempts.library = ?1 ` +
		`AND ` + attemptFactColumn + ` = '` + factRatingIMDb + `' AND at < ?2 AND dataset_modified < ?6)`
	// A rating another tool wrote into the .nfo file has no attempt, so the
	// 30 days never open it. The container reads it once, in a scope that
	// reopens, and the attempt it records then carries the dataset time.
	// The reporter binds no reopen, so it does not count these titles, and
	// a Library whose rating another block answers starts no Job for them.
	unattempted := `(?6 > 0 AND id NOT IN (SELECT item FROM attempts WHERE attempts.library = ?1 ` +
		`AND ` + attemptFactColumn + ` = '` + factRatingIMDb + `'))`
	return `SELECT id FROM (` +
		`SELECT library, id, nfo_facts FROM movies WHERE id NOT LIKE 'movie:path:%' ` +
		`UNION ALL SELECT library, id, nfo_facts FROM series WHERE id NOT LIKE 'series:path:%' ` +
		`UNION ALL SELECT library, id, nfo_facts FROM episodes WHERE ?7 = 1 AND library = ?1 ` +
		`AND series NOT LIKE 'series:path:%' ` +
		`AND path NOT IN (SELECT path FROM episodes WHERE library = ?1 GROUP BY path HAVING count(*) > 1)` +
		`) AS items ` +
		`WHERE library = ?1 AND ` + gapClause(factRatingIMDb, "id", missing+` OR `+reopened+` OR `+unattempted)
}

// The episodes among the rating.imdb gap the reporter counts. The operator
// takes them out of the count for a Library whose rating another block
// answers, because no container of that Library asks for them.
func (c *Catalog) episodeGapCounts(ctx context.Context, library string, now time.Time) (map[string]int, error) {
	count, err := c.queryInt(ctx, `SELECT count(*) FROM (`+gapQueries[factRatingIMDb]+`) WHERE id LIKE '`+
		scopeEpisode+`:%'`, gapParams(factRatingIMDb, library, now, time.Time{}))
	if err != nil {
		return nil, err
	}
	return map[string]int{factRatingIMDb: count}, nil
}

// The gap count the operator schedules on. The reporter reads no Library, so
// it counts the episodes of every Library, and a Library whose rating
// another block answers has no container that asks for them.
func scheduledGap(library *Library, report *libraryReport, providers providerSet, fact string) int {
	count := report.Gaps[fact]
	if fact == factRatingIMDb && imdbRatingAnswerer(library, providers) == nil {
		count -= report.EpisodeGaps[fact]
	}
	return count
}

// When a rating the datasets wrote became old enough to read again: 30 days
// after the oldest rating.imdb attempt, where an imdb provider answers the
// rating for this Library, and the zero time otherwise. The reporter counts
// the gap with no dataset time, so an old rating counts as filled, and the
// oldest attempt says whether one is past the 30 days, the way
// refreshHasWork reads a refresh. While the provider is Stale, IMDb has
// published no newer file, so a run would read the same file again for
// nothing, and the time is zero.
func datasetReopenCause(library *Library, report *libraryReport, providers providerSet,
	now time.Time) time.Time {
	provider := imdbRatingAnswerer(library, providers)
	if provider == nil || provider.stale() {
		return time.Time{}
	}
	oldest, held := report.OldestAttempts[factRatingIMDb]
	if !held {
		return time.Time{}
	}
	if reopened := oldest.Add(defaultRetryInterval); !reopened.After(now) {
		return reopened
	}
	return time.Time{}
}

// Whether the fact is rating.imdb and one of its ratings is old enough to
// read again.
func datasetRefreshHasWork(library *Library, report *libraryReport, providers providerSet,
	fact string, now time.Time) bool {
	return fact == factRatingIMDb && !datasetReopenCause(library, report, providers, now).IsZero()
}
