package main

// Every fact the design holds, so the table, the containers, the ledgers, and
// the CRD's own list read one vocabulary. probe and identity are in
// enrich.go, beside the gap queries they carry.

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

	// The trailerfile fact pulls one of those links into a file beside the
	// title. Its Job runs beside the enricher, so it is out of factVocabulary,
	// out of spec.refresh, and served by no provider.
	factTrailerFile = "trailerfile"

	factContributorIDs       = "contributor.ids"
	factContributorBiography = "contributor.biography"
	factContributorHeadshot  = "contributor.headshot"
)

// Every fact spec.refresh and a MetadataProvider may name, in the order the
// groups run. The CRD's spec.facts enum holds the same names, and a test reads
// the two against each other, so a person cannot name a fact the operator
// does not hold.
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
	factContributorIDs,
	factContributorBiography,
	factContributorHeadshot,
}
