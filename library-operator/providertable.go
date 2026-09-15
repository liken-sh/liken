package main

// providertable.go is the one table of provider blocks: the facts a block
// serves, the pace its provider asks for, the call the check makes, the
// address it calls, and what an account of the block must name. A new
// provider is one row here and a client of its own.

import (
	"slices"
	"strings"
	"time"
)

// The block names of the table's rows, which are the field names of
// MetadataProviderSpec.
const (
	providerBlockTMDb   = "tmdb"
	providerBlockOMDb   = "omdb"
	providerBlockFanart = "fanart"
	providerBlockTVmaze = "tvmaze"
	// The peertube block is one instance, named by its address and not by an
	// account.
	providerBlockPeerTube = "peertube"
	// The archive block is the Internet Archive's movie_trailers collection, at
	// one fixed address and with no account.
	providerBlockArchive = "archive"
)

// What one account of a block holds, as the spec states it: the Secret that
// carries the key, and the address of the instance. A block may take neither.
type providerAccount struct {
	secret   *SecretKeyRef
	endpoint string
}

// One provider block. A MetadataProvider that names no facts of its own
// serves everything its facts hold, so a person who wants all of one provider
// writes the block and nothing else, and a person who wants less narrows it
// with spec.facts.
// The key and the endpoint say what an account must name before a container
// can ask the provider at all.
type providerBlock struct {
	name  string
	facts []string
	// The shortest time between two requests of this block.
	pace time.Duration
	// The one call the check makes, and the address it calls, which is empty for
	// a block whose own spec names one.
	reach providerReach
	base  string
	// Whether the block takes a Secret, and whether its spec names an address.
	key      bool
	endpoint bool
	// The account this spec names for the block, or nothing where the spec names
	// another block.
	account func(*MetadataProviderSpec) *providerAccount
}

// The table, one row per provider block, each row's facts in the order the
// facts run. A provider block with no row here serves nothing.
// The paces follow what each provider asks for. TMDb states about 50 requests
// a second and TVmaze 20 calls every 10 seconds, and these stay far under
// both. The archive states no number, so it gets the slowest pace.
var providerBlocks = []providerBlock{
	{
		name: providerBlockTMDb,
		facts: []string{
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
			factTrailer,
			factContributorIDs,
			factContributorBiography,
			factContributorHeadshot,
		},
		pace:  100 * time.Millisecond,
		reach: providerReach{path: tmdbConfigurationPath, authorize: authorizeTMDb},
		base:  tmdbAPIBase,
		key:   true,
		account: func(spec *MetadataProviderSpec) *providerAccount {
			if spec.TMDb == nil {
				return nil
			}
			return &providerAccount{secret: &spec.TMDb.SecretRef}
		},
	},
	// OMDb answers on an IMDb id and holds the ratings of three sites, the US
	// certification, and the plot. Its credits are names with no ids, so the
	// credits fact does not read them.
	{
		name: providerBlockOMDb,
		facts: []string{
			factOverview,
			factCertification,
			factRatingIMDb,
			factRatingRottenTomatoes,
			factRatingMetacritic,
		},
		pace:  250 * time.Millisecond,
		reach: providerReach{path: omdbCheckPath, authorize: authorizeParameter(omdbAPIKeyParameter)},
		base:  omdbAPIBase,
		key:   true,
		account: func(spec *MetadataProviderSpec) *providerAccount {
			if spec.OMDb == nil {
				return nil
			}
			return &providerAccount{secret: &spec.OMDb.SecretRef}
		},
	},
	// Fanart.tv holds art alone, and it is the only provider of the clearart,
	// the banner, the landscape, the discart, and the season banner.
	{
		name: providerBlockFanart,
		facts: []string{
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
		pace:  250 * time.Millisecond,
		reach: providerReach{path: fanartCheckPath, authorize: authorizeParameter(fanartAPIKeyParam)},
		base:  fanartAPIBase,
		key:   true,
		account: func(spec *MetadataProviderSpec) *providerAccount {
			if spec.Fanart == nil {
				return nil
			}
			return &providerAccount{secret: &spec.Fanart.SecretRef}
		},
	},
	// TVmaze holds series alone and needs no account. Its show call carries the
	// external ids, the summary and the genres, the cast, and the poster, the
	// background, and the banner.
	{
		name: providerBlockTVmaze,
		facts: []string{
			factIdentity,
			factOverview,
			factCredits,
			factPoster,
			factBackdrop,
			factBanner,
		},
		pace:  500 * time.Millisecond,
		reach: providerReach{path: tvmazeCheckPath},
		base:  tvmazeAPIBase,
		account: func(spec *MetadataProviderSpec) *providerAccount {
			if spec.TVmaze == nil {
				return nil
			}
			return &providerAccount{}
		},
	},
	// A PeerTube instance holds videos alone, so it serves the trailer fact
	// and nothing else. The block names no address of the service, because
	// every instance is a server of its own.
	{
		name:     providerBlockPeerTube,
		facts:    []string{factTrailer},
		pace:     500 * time.Millisecond,
		reach:    providerReach{path: peertubeCheckPath},
		endpoint: true,
		account: func(spec *MetadataProviderSpec) *providerAccount {
			if spec.PeerTube == nil {
				return nil
			}
			return &providerAccount{endpoint: spec.PeerTube.Endpoint}
		},
	},
	// The Internet Archive's movie_trailers collection holds trailers alone, so
	// it serves the trailer fact and nothing else.
	{
		name:  providerBlockArchive,
		facts: []string{factTrailer},
		pace:  time.Second,
		reach: providerReach{path: archiveCheckPath},
		base:  archiveAPIBase,
		account: func(spec *MetadataProviderSpec) *providerAccount {
			if spec.Archive == nil {
				return nil
			}
			return &providerAccount{}
		},
	},
}

// The row of one block, or an empty row for a name the table does not hold.
// The empty row serves no fact, paces at no interval, and calls no address.
func blockOf(name string) providerBlock {
	for _, block := range providerBlocks {
		if block.name == name {
			return block
		}
	}
	return providerBlock{}
}

// Whether one block's row holds a fact. An answerer reads it because it runs
// in a container that holds no MetadataProvider.
func blockServes(name, fact string) bool {
	return slices.Contains(blockOf(name).facts, fact)
}

// The address of each provider the check calls, which a test replaces with a
// server of its own.
// A block whose spec names its own address has no entry here.
func defaultProviderBases() map[string]string {
	bases := map[string]string{}
	for _, block := range providerBlocks {
		if block.base != "" {
			bases[block.name] = block.base
		}
	}
	return bases
}

// The block this provider names and the account its spec states, or an empty
// row and no account for a spec that names no block.
func (p *MetadataProvider) account() (providerBlock, *providerAccount) {
	for _, block := range providerBlocks {
		if account := block.account(&p.Spec); account != nil {
			return block, account
		}
	}
	return providerBlock{}, nil
}

// The block this provider names. A provider that names none has no row in the
// table and serves no fact.
func (p *MetadataProvider) block() string {
	block, _ := p.account()
	return block.name
}

// The Secret this provider's block names, or none for a provider that takes
// no key. The operator reads it for the check, and the enricher's containers
// read it through a secretKeyRef.
func (p *MetadataProvider) secretRef() *SecretKeyRef {
	_, account := p.account()
	if account == nil {
		return nil
	}
	return account.secret
}

// The address the provider's block names. Only a block that is one server
// among many names one, because every other block is one service at one fixed
// address, so this is empty for them.
// A trailing slash is cut, so a path joins to it cleanly.
func (p *MetadataProvider) endpoint() string {
	_, account := p.account()
	if account == nil {
		return ""
	}
	return strings.TrimSuffix(account.endpoint, "/")
}

// Every fact this provider serves: its row in the table, narrowed to the
// facts spec.facts names where it names any. The order is the table's, so two
// providers of one block report their facts in one order.
func (p *MetadataProvider) servedFacts() []string {
	block, _ := p.account()
	if len(p.Spec.Facts) == 0 {
		return slices.Clone(block.facts)
	}
	served := []string{}
	for _, fact := range block.facts {
		if slices.Contains(p.Spec.Facts, fact) {
			served = append(served, fact)
		}
	}
	return served
}
