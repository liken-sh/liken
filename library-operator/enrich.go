package main

// enrich.go is the seam between the enricher Job's containers and the
// operator that creates the Job. Plan 29 builds both sides on the names
// here: the roles the binary runs, the facts those roles fill, the
// results an attempt can record, and the queries that say how much work
// each fact has left. The reporter counts a gap with the same query a
// container works from, so the count the operator schedules on and the
// rows the container finds are one set.

import "time"

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
// a key that was refused, or a file that would not open, and the next run
// tries again.
const (
	attemptFound      = "found"
	attemptCandidates = "candidates"
	attemptNothing    = "nothing"
	attemptError      = "error"
	attemptFight      = "fight"
)

// How long an attempt with a date stands before the fact that wrote it asks
// again. Providers gain ids and art over time, so a miss is retried. Thirty
// days is the guess plan 27 records.
const defaultRetryInterval = 30 * 24 * time.Hour

// The gap query per fact, keyed by fact. Every query takes the
// library as its first parameter and the retry cutoff in Unix seconds as
// its second, and selects the key of each row that needs work, so a
// count(*) over it is the reporter's number and the rows are the
// container's work list.
//
// A probe gap is a present video file with no duration, which is what a
// file with no streamdetails in its sidecar looks like in the catalog.
// An identity gap is an item whose id is its folder key, so no provider
// named it. Both exclude an item with an attempt inside the window,
// unless that attempt ended in an error. A probe whose details landed
// closes its gap through the duration on the next scan, and a probe whose
// details landed nowhere the scanner reads is tried again after the
// window.
var gapQueries = map[string]string{
	factProbe: `SELECT path FROM files ` +
		`WHERE library = ? AND type = 'video' AND present = 1 AND duration_ms = 0 ` +
		`AND path NOT IN (SELECT item FROM attempts WHERE library = files.library AND ` + attemptFactColumn + ` = 'probe' AND result != 'error' AND at >= ?)`,
	factIdentity: `SELECT id FROM (` +
		`SELECT library, id FROM movies WHERE id LIKE 'movie:path:%' ` +
		`UNION ALL SELECT library, id FROM series WHERE id LIKE 'series:path:%') AS items ` +
		`WHERE library = ? AND id NOT IN (SELECT item FROM attempts ` +
		`WHERE attempts.library = items.library AND ` + attemptFactColumn + ` = 'identity' AND result != 'error' AND at >= ?)`,
	factOverview:             nfoGapQuery(factOverview),
	factCertification:        nfoGapQuery(factCertification),
	factRatingTMDb:           nfoGapQuery(factRatingTMDb),
	factRatingIMDb:           nfoGapQuery(factRatingIMDb),
	factRatingRottenTomatoes: nfoGapQuery(factRatingRottenTomatoes),
	factRatingMetacritic:     nfoGapQuery(factRatingMetacritic),
	factCredits:              nfoGapQuery(factCredits),
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
