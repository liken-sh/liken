package main

// What these tests read: the provider keys the enricher's containers receive,
// one per block the Library's sources reach.

import (
	"testing"
)

// PROSE: the set a pass would answer with, from the providers of one
// namespace.
func providersOf(providers ...*MetadataProvider) providerSet {
	set := providerSet{}
	for _, provider := range providers {
		set[libraryKey(provider.Metadata.Namespace, provider.Metadata.Name)] = provider
	}
	return set
}

// PROSE: the Library that names those providers as its sources, in order.
func libraryWithSources(sources ...string) *Library {
	library := studioMovies()
	library.Spec.Sources = sources
	return library
}

// PROSE: the variable one block's key travels in. The identity fact reads
// TMDB_TOKEN, which is that rule for tmdb.
func TestTheVariableOfEachProviderKey(t *testing.T) {
	cases := []struct {
		block string
		want  string
	}{
		{block: providerBlockTMDb, want: tmdbTokenVariable},
		{block: providerBlockOMDb, want: "OMDB_TOKEN"},
		{block: providerBlockFanart, want: "FANART_TOKEN"},
	}
	for _, one := range cases {
		t.Run(one.block, func(t *testing.T) {
			if got := providerTokenVariable(one.block); got != one.want {
				t.Errorf("the variable of %s = %q, want %q", one.block, got, one.want)
			}
		})
	}
}

// The keys a Library's sources reach, in the order the sources name them. A
// provider that is not Ready contributes none, because a secretKeyRef to a
// Secret that does not exist holds the pod out of Running. TVmaze contributes
// none, because it takes no account.
func TestTheProviderKeysOfALibrary(t *testing.T) {
	tmdb := providerOfBlock("tmdb", providerBlockTMDb)
	omdb := providerOfBlock("omdb", providerBlockOMDb)
	fanart := providerOfBlock("fanart", providerBlockFanart)
	tvmaze := providerOfBlock("tvmaze", providerBlockTVmaze)
	second := providerOfBlock("second-omdb", providerBlockOMDb)
	refused := providerOfBlock("refused", providerBlockFanart)
	refused.Status.Conditions = []Condition{
		{Type: conditionReady, Status: ConditionFalse, Reason: reasonNoSecret},
	}

	cases := []struct {
		name    string
		sources []string
		want    []string
	}{
		{name: "no sources at all", want: []string{}},
		{name: "a source that does not exist", sources: []string{"nothing"}, want: []string{}},
		{name: "one account with each provider that takes a key",
			sources: []string{"tmdb", "omdb", "fanart"},
			want:    []string{tmdbTokenVariable, "OMDB_TOKEN", "FANART_TOKEN"}},
		{name: "the sources in the order they are asked",
			sources: []string{"omdb", "tmdb"},
			want:    []string{"OMDB_TOKEN", tmdbTokenVariable}},
		{name: "a provider that takes no key", sources: []string{"tvmaze"}, want: []string{}},
		{name: "a provider that is not Ready", sources: []string{"refused"}, want: []string{}},
		{name: "the first account of a block wins",
			sources: []string{"omdb", "second-omdb"}, want: []string{"OMDB_TOKEN"}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			keys := providerKeyEnv(libraryWithSources(one.sources...),
				providersOf(tmdb, omdb, fanart, tvmaze, second, refused))

			names := []string{}
			for _, key := range keys {
				names = append(names, key.Name)
				if key.ValueFrom == nil || key.ValueFrom.SecretKeyRef == nil {
					t.Errorf("%s reads %+v, want a secretKeyRef", key.Name, key.ValueFrom)
				}
			}
			if len(names) != len(one.want) {
				t.Fatalf("the keys are %v, want %v", names, one.want)
			}
			for index, want := range one.want {
				if names[index] != want {
					t.Errorf("key %d = %q, want %q", index, names[index], want)
				}
			}
		})
	}
}

// PROSE: the key names the Secret of its own provider and the key inside it,
// so the kubelet reads the credential and this operator never holds it.
func TestAProviderKeyNamesItsOwnSecret(t *testing.T) {
	omdb := providerOfBlock("omdb", providerBlockOMDb)

	keys := providerKeyEnv(libraryWithSources("omdb"), providersOf(omdb))

	if len(keys) != 1 {
		t.Fatalf("keys = %+v, want the one account", keys)
	}
	reference := keys[0].ValueFrom.SecretKeyRef
	if reference.Name != "omdb-key" || reference.Key != defaultProviderSecretKey {
		t.Errorf("secretKeyRef = %+v, want the provider's Secret and the default key", reference)
	}
}
