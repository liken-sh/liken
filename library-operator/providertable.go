package main

// Which provider can serve which fact. A MetadataProvider that names no facts
// of its own serves everything its row here holds, so a person who wants all
// of one provider writes the block and nothing else, and a person who wants
// less narrows it with spec.facts.

import "slices"

// The table, one row per provider block, each row in the order the facts run.
// A row grows as this operator learns to ask its provider for more. A
// provider block with no row here serves nothing.
var providerFacts = map[string][]string{
	providerBlockTMDb: {
		factIdentity,
		factOverview,
		factCertification,
		factRatingTMDb,
		factCredits,
		factPoster,
		factBackdrop,
		factLogo,
		factSeasonPoster,
		factEpisodeThumb,
		factContributorIDs,
		factContributorBiography,
		factContributorHeadshot,
	},
	// OMDb answers on an IMDb id and holds the ratings of three sites, the US
	// certification, and the plot. Its credits are names with no ids, so the
	// credits fact does not read them.
	providerBlockOMDb: {
		factOverview,
		factCertification,
		factRatingIMDb,
		factRatingRottenTomatoes,
		factRatingMetacritic,
	},
	// Fanart.tv holds art alone, and it is the only provider of the clearart,
	// the banner, the landscape, the discart, and the season banner.
	providerBlockFanart: {
		factPoster,
		factBackdrop,
		factLogo,
		factClearart,
		factBanner,
		factLandscape,
		factDiscart,
		factSeasonPoster,
		factSeasonBanner,
	},
	// TVmaze holds series alone and needs no account. Its show call carries the
	// external ids, the summary and the genres, the cast, and the poster, the
	// background, and the banner.
	providerBlockTVmaze: {
		factIdentity,
		factOverview,
		factCredits,
		factPoster,
		factBackdrop,
		factBanner,
	},
}

// The block names of the table's rows, which are the field names of
// MetadataProviderSpec.
const (
	providerBlockTMDb   = "tmdb"
	providerBlockOMDb   = "omdb"
	providerBlockFanart = "fanart"
	providerBlockTVmaze = "tvmaze"
)

// The block this provider names. A provider that names none has no row in the
// table and serves no fact.
func (p *MetadataProvider) block() string {
	switch {
	case p.Spec.TMDb != nil:
		return providerBlockTMDb
	case p.Spec.OMDb != nil:
		return providerBlockOMDb
	case p.Spec.Fanart != nil:
		return providerBlockFanart
	case p.Spec.TVmaze != nil:
		return providerBlockTVmaze
	}
	return ""
}

// The Secret this provider's block names, or none for a provider that takes
// no key. The operator reads it for the check, and the enricher's containers
// read it through a secretKeyRef.
func (p *MetadataProvider) secretRef() *SecretKeyRef {
	switch p.block() {
	case providerBlockTMDb:
		return &p.Spec.TMDb.SecretRef
	case providerBlockOMDb:
		return &p.Spec.OMDb.SecretRef
	case providerBlockFanart:
		return &p.Spec.Fanart.SecretRef
	}
	return nil
}

// Every fact this provider serves: its row in the table, narrowed to the
// facts spec.facts names where it names any. The order is the table's, so two
// providers of one block report their facts in one order.
func (p *MetadataProvider) servedFacts() []string {
	table := providerFacts[p.block()]
	if len(p.Spec.Facts) == 0 {
		return slices.Clone(table)
	}
	served := []string{}
	for _, fact := range table {
		if slices.Contains(p.Spec.Facts, fact) {
			served = append(served, fact)
		}
	}
	return served
}
