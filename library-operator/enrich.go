package main

// enrich.go is the seam between the enricher Job's containers and the
// operator that creates the Job. Plan 29 builds both sides on the names
// here: the roles the binary runs, the facts those roles fill, the
// results an attempt can record, and the queries that say how much work
// each fact has left. The reporter counts a gap with the same query a
// container works from, so the count the operator schedules on and the
// rows the container finds are one set.

import (
	"encoding/json"
	"slices"
	"time"
)

// The roles the enricher Job runs. Every fact container runs facts, which
// runs the facts its container names, in order, in one process. The one
// regular container runs enrich, which writes the runs row last and waits for
// the echo.
const (
	factsMode  = "facts"
	enrichMode = "enrich"
)

// The variable a facts container reads its work from: the facts it runs, by
// name, separated by commas, in the order it runs them. The container's own
// name is the phase, so kubectl get pod reads as the sequence.
const libraryFactsVariable = "LIBRARY_FACTS"

// The variable every enricher container reads the refresh times from:
// The Library's spec.refresh as one JSON object, fact name to an
// RFC 3339 time. The container holds no API credential, so the
// environment is where it reads a Library's field, as it reads the
// ignore list.
const libraryRefreshVariable = "LIBRARY_REFRESH"

// The refresh time of each fact a Library named, and the zero time for
// every fact it did not.
type refreshTimes map[string]time.Time

// A value this image cannot read is no refresh at all, because a
// container that read a bad value as a refresh would ask a provider
// about every title of the library.
func parseRefresh(raw string) refreshTimes {
	times := refreshTimes{}
	if raw == "" {
		return times
	}
	read := refreshTimes{}
	if err := json.Unmarshal([]byte(raw), &read); err != nil {
		return times
	}
	return read
}

// The worker name the enricher Job's runs row carries. One row per
// Library, whatever the Job's scope, so the operator reads one entry to
// know whether an enrich run is in flight.
const workerEnrich = "enrich"

// The environment an identity container reads its TMDb key from. The
// operator fills it from the Secret a MetadataProvider names, through a
// secretKeyRef, so the container never reads the API server.
const tmdbTokenVariable = "TMDB_TOKEN"

// The facts this image fills, in the order they run. A fact is one gap query,
// one name in a container's LIBRARY_FACTS, and one ledger file in .liken/. A
// container runs one fact or several.
const (
	factProbe    = "probe"
	factIdentity = "identity"
)

// What one attempt left behind. found, candidates, nothing, and fight are
// facts with a date, and the retry interval applies to them. A fight is a
// fact that read its element group on disk, found bytes it did not write, and
// left them; Library status counts it. An error is a provider that was down,
// a key that was refused, or a file that would not open. An error stands for
// its own shorter window, so a fault that lasts is tried again the next day
// and not on every run.
const (
	attemptFound      = "found"
	attemptCandidates = "candidates"
	attemptNothing    = "nothing"
	attemptError      = "error"
	attemptFight      = "fight"
)

// How long an attempt stands before the fact that wrote it asks again. A
// dated fact stands for thirty days, the guess plan 27 records, because
// providers gain ids and art over time. An error stands for a day, so a
// provider that is down or a volume that will not read is asked again the
// next day, and a fault that stands costs one try a day.
// An attempt an item's release date has outlived stands for neither
// window. beforeReleaseClause carries that rule.
const (
	defaultRetryInterval = 30 * 24 * time.Hour
	errorRetryInterval   = 24 * time.Hour
)

// The four parameters every gap query binds by number: the library, the
// cutoff a dated attempt stands until, the cutoff an error stands
// until, and the refresh time this fact carries.
// A fact the Library's spec.refresh does not name binds a refresh of
// zero, which every attempt is later than, so the query reads as it did
// before the field existed.
//
// A fact whose items carry a release date binds today as a fifth, in the
// form the released column holds, because an attempt made before an item
// was released stands only until that date. A fact whose items carry
// none names no fifth parameter, so it binds none.
func gapParams(fact, library string, now, refresh time.Time) []any {
	params := []any{library,
		now.Add(-defaultRetryInterval).Unix(),
		now.Add(-errorRetryInterval).Unix(),
		refreshSeconds(refresh, now)}
	if releaseDates(fact) == "" {
		return params
	}
	return append(params, now.UTC().Format(time.DateOnly))
}

// The refresh time in Unix seconds, and zero where the Library names none or
// names a time that has not come, because a zero time is a large negative
// second that reads as a refresh of the far past. A refresh in the future
// waits for its time and then runs once, where a bound future time would
// reopen the gap on every run until the clock passed it.
func refreshSeconds(refresh, now time.Time) int64 {
	if refresh.IsZero() || refresh.After(now) {
		return 0
	}
	return refresh.Unix()
}

// The attempt window every gap query carries. An item whose last attempt is a
// dated fact inside the retry window is no gap, and an item whose last
// attempt is an error is no gap until the error window has passed. The
// library is bound by number and never read off the outer row, because a
// subquery that reads the outer row runs again for every item.
//
// An attempt this fact made before the refresh time does not count, so
// it closes no gap.
func attemptClause(fact, column string) string {
	return column + ` NOT IN (SELECT item FROM attempts WHERE attempts.library = ?1 ` +
		`AND ` + attemptFactColumn + ` = '` + fact + `' AND at >= ?4 ` +
		`AND ((result != '` + attemptError + `' AND at >= ?2) ` +
		`OR (result = '` + attemptError + `' AND at >= ?3)))`
}

// The items this fact attempted before the refresh time. They are a gap
// again whatever the fact's own condition says, because the file the
// fact wrote and the rows it made are still there and the point of a
// refresh is to write them again.
func refreshedClause(fact, column string) string {
	return column + ` IN (SELECT item FROM attempts WHERE attempts.library = ?1 ` +
		`AND ` + attemptFactColumn + ` = '` + fact + `' AND at < ?4)`
}

// The items this fact attempted before the item was released, where the
// release date has arrived. A provider that held nothing for a title
// before its release day can hold something after it, so an attempt from
// before the release date stands only until that date, and an attempt
// from on or after it stands for the window its own kind carries.
//
// Both sides of the comparison are text, and both sort as dates: the
// catalog holds a release as an ISO date or as a year alone, and a year
// alone reads as the first day of that year. The subquery reads the
// release date off a join by item, never off the outer row, so SQLite
// builds it once.
func beforeReleaseClause(fact, column string) string {
	releases := releaseDates(fact)
	if releases == "" {
		return ""
	}
	return ` OR ` + column + ` IN (SELECT a.item FROM attempts AS a ` +
		`JOIN (` + releases + `) AS r ON r.library = a.library AND r.item = a.item ` +
		`WHERE a.library = ?1 AND a.` + attemptFactColumn + ` = '` + fact + `' ` +
		`AND r.released != '' AND date(a.at, 'unixepoch') < r.released ` +
		`AND ?5 >= r.released)`
}

// Where the items of one fact carry their release date, as the library,
// the item the fact's attempts key on, and the released column. A fact
// whose items have no release date in the catalog reads as an empty
// string, and the attempt window alone holds its items.
func releaseDates(fact string) string {
	if _, art := artTypes[fact]; art {
		return artReleaseDates(fact)
	}
	if fact == factIdentity || fact == factCredits || slices.Contains(nfoFacts, fact) {
		return titleReleaseDates("id")
	}
	return ""
}

// The release date of every movie and every series of one library, keyed
// on the item the fact names: the title's own id, or the file the title's
// art lands in.
func titleReleaseDates(item string) string {
	return `SELECT library, ` + item + ` AS item, released FROM movies WHERE library = ?1 ` +
		`UNION ALL SELECT library, ` + item + `, released FROM series WHERE library = ?1`
}

// The whole tail of a gap query: the fact's own condition for an item
// it has not filled, the refresh that opens an item it did fill, and
// the attempt window that holds an item it tried lately, less the
// attempts the item's own release date has outlived.
// No subquery reads the outer row, so SQLite builds each one once
// for the whole query.
func gapClause(fact, column, missing string) string {
	return `(` + missing + ` OR ` + refreshedClause(fact, column) + `) ` +
		`AND (` + attemptClause(fact, column) + beforeReleaseClause(fact, column) + `)`
}

// The gap query per fact, keyed by fact. Every query binds the library as ?1,
// the cutoff of a dated attempt in Unix seconds as ?2, and the cutoff of an
// error as ?3, and selects the key of each row that needs work, so a count(*)
// over it is the reporter's number and the rows are the container's work list.
//
// Every query binds the fact's refresh time as ?4. A query whose items
// carry a release date binds today's date as ?5, and no other query
// names a fifth parameter.
//
// A probe gap is a present video file with no duration, which is what a file
// with no streamdetails in its sidecar looks like in the catalog. An identity
// gap is an item whose id is its folder key, so no provider named it. Both
// exclude an item with an attempt inside that attempt's own window. A probe
// whose details landed closes its gap through the duration on the next scan,
// and a probe whose details landed nowhere the scanner reads is tried again
// after the window.
var gapQueries = map[string]string{
	// A video with a length and no tiles beside it.
	factTrickplay: trickplayGapSQL(),
	// A present video the arrival ledger holds no entry for.
	factArrival: arrivalGapSQL(),
	factProbe: `SELECT path FROM files ` +
		`WHERE library = ?1 AND type = 'video' AND present = 1 ` +
		`AND ` + gapClause(factProbe, "path", `duration_ms = 0`),
	// The folder key stays in the outer condition, not the source, so a
	// refresh opens a title a provider has already named.
	factIdentity: `SELECT id FROM (` +
		`SELECT library, id FROM movies ` +
		`UNION ALL SELECT library, id FROM series) AS items ` +
		`WHERE library = ?1 AND ` + gapClause(factIdentity, "id",
		`id LIKE 'movie:path:%' OR id LIKE 'series:path:%'`),
	factOverview:             nfoGapQuery(factOverview),
	factCertification:        nfoGapQuery(factCertification),
	factRatingTMDb:           nfoGapQuery(factRatingTMDb),
	factRatingIMDb:           nfoGapQuery(factRatingIMDb),
	factRatingRottenTomatoes: nfoGapQuery(factRatingRottenTomatoes),
	factRatingMetacritic:     nfoGapQuery(factRatingMetacritic),
	factCredits:              creditsGapQuery(),
	factPoster:               titleArtGapSQL(factPoster),
	factBackdrop:             titleArtGapSQL(factBackdrop),
	factLogo:                 titleArtGapSQL(factLogo),
	factClearart:             titleArtGapSQL(factClearart),
	factBanner:               titleArtGapSQL(factBanner),
	factLandscape:            titleArtGapSQL(factLandscape),
	factDiscart:              titleArtGapSQL(factDiscart),
	factSeasonPoster:         seasonArtGapSQL(factSeasonPoster),
	factSeasonBanner:         seasonArtGapSQL(factSeasonBanner),
	factEpisodeThumb:         episodeThumbGapSQL(),

	factContributorIDs:       contributorIDsGapSQL(),
	factContributorBiography: contributorFileGapSQL(factContributorBiography, "biography"),
	factContributorHeadshot:  contributorFileGapSQL(factContributorHeadshot, "headshot"),
}

// The two counts a person reads on the Library beside the gaps. Waiting
// is the identity attempts that ended in candidates for an item still
// unidentified, the titles that need a person. Unresolved is the attempts
// that ended in nothing for an item still unidentified, the titles no
// provider could name.
const (
	waitingQuery = `SELECT count(*) FROM attempts WHERE library = ? AND ` + attemptFactColumn + ` = 'identity' AND result = 'candidates' ` +
		`AND (item LIKE 'movie:path:%' OR item LIKE 'series:path:%')`
	unresolvedQuery = `SELECT count(*) FROM attempts WHERE library = ? AND ` + attemptFactColumn + ` = 'identity' AND result = 'nothing' ` +
		`AND (item LIKE 'movie:path:%' OR item LIKE 'series:path:%')`
)
