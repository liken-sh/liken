package main

// What these tests read: the block one spec names, the Secret that block
// holds, and which providers serve each fact of the vocabulary.

import (
	"slices"
	"testing"
)

// One provider of one block, checked and reachable, as a pass would leave it.
// The Secret is named after the provider.
func providerOfBlock(name, block string) *MetadataProvider {
	provider := &MetadataProvider{Metadata: ObjectMeta{Name: name, Namespace: "house"}}
	reference := SecretKeyRef{Name: name + "-key"}
	switch block {
	case providerBlockTMDb:
		provider.Spec.TMDb = &ProviderTMDb{SecretRef: reference}
	case providerBlockOMDb:
		provider.Spec.OMDb = &ProviderOMDb{SecretRef: reference}
	case providerBlockFanart:
		provider.Spec.Fanart = &ProviderFanart{SecretRef: reference}
	case providerBlockTVmaze:
		provider.Spec.TVmaze = &ProviderTVmaze{}
	}
	provider.Status.Conditions = []Condition{
		{Type: conditionReady, Status: ConditionTrue, Reason: reasonReachable},
	}
	return provider
}

// The block of a spec is the one block it names, and the Secret is the one
// that block holds. A provider that names no block has neither.
func TestTheBlockAndTheSecretOfOneSpec(t *testing.T) {
	cases := []struct {
		name       string
		block      string
		wantSecret string
	}{
		{name: "an account with TMDb", block: providerBlockTMDb, wantSecret: "one-key"},
		{name: "an account with OMDb", block: providerBlockOMDb, wantSecret: "one-key"},
		{name: "an account with Fanart.tv", block: providerBlockFanart, wantSecret: "one-key"},
		{name: "TVmaze, which takes no account", block: providerBlockTVmaze},
		{name: "a spec that names no block"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			provider := providerOfBlock("one", one.block)

			if got := provider.block(); got != one.block {
				t.Errorf("block = %q, want %q", got, one.block)
			}
			reference := provider.secretRef()
			if one.wantSecret == "" {
				if reference != nil {
					t.Errorf("secretRef = %+v, want none", reference)
				}
				return
			}
			if reference == nil || reference.Name != one.wantSecret {
				t.Errorf("secretRef = %+v, want the Secret %s", reference, one.wantSecret)
			}
		})
	}
}

// Every fact a row names is a fact of the vocabulary, so a provider cannot
// serve a name no container runs and no CRD admits.
func TestEveryRowNamesFactsOfTheVocabulary(t *testing.T) {
	for block, row := range providerFacts {
		for _, fact := range row {
			if !slices.Contains(factVocabulary, fact) {
				t.Errorf("%s serves %q, which is no fact of the vocabulary", block, fact)
			}
		}
	}
}

// Which providers serve each fact, which is the table of the design. The
// order is the order a Library's sources are asked in, so a fact with two
// providers reads them in the table's own order.
func TestWhichProvidersServeEachFact(t *testing.T) {
	cases := []struct {
		fact string
		want []string
	}{
		{fact: factProbe},
		{fact: factTrickplay},
		{fact: factIdentity, want: []string{providerBlockTMDb, providerBlockTVmaze}},
		{fact: factOverview, want: []string{providerBlockTMDb, providerBlockOMDb, providerBlockTVmaze}},
		{fact: factCertification, want: []string{providerBlockTMDb, providerBlockOMDb}},
		{fact: factRatingTMDb, want: []string{providerBlockTMDb}},
		{fact: factRatingIMDb, want: []string{providerBlockOMDb}},
		{fact: factRatingRottenTomatoes, want: []string{providerBlockOMDb}},
		{fact: factRatingMetacritic, want: []string{providerBlockOMDb}},
		{fact: factCredits, want: []string{providerBlockTMDb, providerBlockTVmaze}},
		{fact: factPoster, want: []string{providerBlockTMDb, providerBlockFanart, providerBlockTVmaze}},
		{fact: factBackdrop, want: []string{providerBlockTMDb, providerBlockFanart, providerBlockTVmaze}},
		{fact: factLogo, want: []string{providerBlockTMDb, providerBlockFanart}},
		{fact: factClearart, want: []string{providerBlockFanart}},
		{fact: factBanner, want: []string{providerBlockFanart, providerBlockTVmaze}},
		{fact: factLandscape, want: []string{providerBlockFanart}},
		{fact: factDiscart, want: []string{providerBlockFanart}},
		{fact: factSeasonPoster, want: []string{providerBlockTMDb, providerBlockFanart}},
		{fact: factSeasonBanner, want: []string{providerBlockFanart}},
		{fact: factEpisodeThumb, want: []string{providerBlockTMDb}},
		{fact: factContributorIDs, want: []string{providerBlockTMDb}},
		{fact: factContributorBiography, want: []string{providerBlockTMDb}},
		{fact: factContributorHeadshot, want: []string{providerBlockTMDb}},
	}
	blocks := []string{providerBlockTMDb, providerBlockOMDb, providerBlockFanart, providerBlockTVmaze}
	for _, one := range cases {
		t.Run(one.fact, func(t *testing.T) {
			serving := []string{}
			for _, block := range blocks {
				if providerOfBlock("one", block).serves(one.fact) {
					serving = append(serving, block)
				}
			}
			if !slices.Equal(serving, one.want) {
				t.Errorf("%s is served by %v, want %v", one.fact, serving, one.want)
			}
		})
	}
}
