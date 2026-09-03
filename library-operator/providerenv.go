package main

// The provider keys the enricher's containers read. Every container that asks
// a provider reads the same set, so the Job builder calls one function and a
// new container gains the keys with it.

import "strings"

// The variable one provider block's key travels in: the block name in
// capitals, then _TOKEN. TMDB_TOKEN, the one the identity fact reads, is that
// rule for tmdb.
func providerTokenVariable(block string) string {
	return strings.ToUpper(block) + "_TOKEN"
}

// Every key the Library's sources reach, in the order spec.sources names
// them. A provider that is not Ready contributes none, because a secretKeyRef
// to a Secret that does not exist holds the pod out of Running. A provider
// that takes no key contributes none. The first account of a block wins,
// because two accounts cannot share one variable name.
func providerKeyEnv(library *Library, providers providerSet) []EnvVar {
	keys := []EnvVar{}
	held := map[string]bool{}
	for _, name := range library.Spec.Sources {
		provider, exists := providers[libraryKey(library.Metadata.Namespace, name)]
		if !exists || !provider.ready() {
			continue
		}
		block := provider.block()
		reference := provider.secretRef()
		if reference == nil || held[block] {
			continue
		}
		held[block] = true
		keys = append(keys, EnvVar{
			Name: providerTokenVariable(block),
			ValueFrom: &EnvVarSource{SecretKeyRef: &SecretKeySelector{
				Name: reference.Name,
				Key:  reference.secretKey(),
			}},
		})
	}
	return keys
}
