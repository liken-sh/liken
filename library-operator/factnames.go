package main

// Every fact the design holds, so the table, the containers, the ledgers, and
// the CRD's own list read one vocabulary. probe and identity are in
// enrich.go, beside the gap queries they carry.

import "slices"

// The facts of the file group, the nfo group, the art group, and the people
// group. A fact is one gap in the catalog, one name in a container's
// LIBRARY_FACTS, and one ledger file in .liken/.
const (
	factArrival   = "arrival"
	factTrickplay = "trickplay"

	factOverview             = "overview"
	factCertification        = "certification"
	factRatingTMDb           = "rating.tmdb"
	factRatingIMDb           = "rating.imdb"
	factRatingRottenTomatoes = "rating.rottentomatoes"
	factRatingMetacritic     = "rating.metacritic"
	factCredits              = "credits"

	factPoster       = "poster"
	factBackdrop     = "backdrop"
	factLogo         = "logo"
	factClearart     = "clearart"
	factBanner       = "banner"
	factLandscape    = "landscape"
	factDiscart      = "discart"
	factSeasonPoster = "season-poster"
	factSeasonBanner = "season-banner"
	factEpisodeThumb = "episode-thumb"

	// The trailer group holds one fact: the trailers a provider names for a
	// title. The fact records ids and links. It plays nothing and downloads
	// nothing.
	factTrailer = "trailer"

	// The marks group holds one fact: where a video file's intro, recap,
	// credits, and preview are, as community databases record them. It is a
	// file fact, keyed on the file's path, because two releases of one work
	// place the same span at different times.
	factMarks = "marks"

	// The trailerfile fact pulls one of those links into a file beside the
	// title. Its Job runs beside the enricher, so it is out of factVocabulary,
	// out of spec.refresh, and served by no provider.
	factTrailerFile = "trailerfile"

	factContributorIDs       = "contributor.ids"
	factContributorBiography = "contributor.biography"
	factContributorHeadshot  = "contributor.headshot"
)

// Every fact a MetadataProvider may name, in the order the groups run. The
// CRD's spec.facts enum holds the same names, and a test reads the two
// against each other, so a person cannot name a fact the operator does not
// hold. spec.refresh takes one time for each of these, and one more for the
// walk below.
var factVocabulary = []string{
	factProbe,
	factArrival,
	factTrickplay,
	factIdentity,
	factOverview,
	factCertification,
	factRatingTMDb,
	factRatingIMDb,
	factRatingRottenTomatoes,
	factRatingMetacritic,
	factCredits,
	factPoster,
	factBackdrop,
	factLogo,
	factClearart,
	factBanner,
	factLandscape,
	factDiscart,
	factSeasonPoster,
	factSeasonBanner,
	factEpisodeThumb,
	factTrailer,
	factMarks,
	factContributorIDs,
	factContributorBiography,
	factContributorHeadshot,
}

// The one refresh target that is not a fact. A time under this key asks for
// one full walk of the library, and a walk that starts at or after the time
// answers it. The word is the scan worker's own, so a person reads the
// request, the Job, and the runs row as one thing.
const refreshWalk = "scan"

// Every key spec.refresh accepts: the facts, then the walk. The CRD's
// refresh rule holds these names, and a test reads the two together.
var refreshVocabulary = append(slices.Clone(factVocabulary), refreshWalk)

// Whether a refresh target is work a container runs. The walk is not, so
// the code that hands spec.refresh to an enricher, or reads it for an
// enrichment cause, skips it.
func isContainerFact(name string) bool {
	return slices.Contains(factVocabulary, name)
}
