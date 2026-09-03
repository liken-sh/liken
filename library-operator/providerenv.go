package main

// The provider keys the enricher's containers read. Every container that asks
// a provider reads the same set, so the Job builder calls one function and a
// new container gains the keys with it.

import "strings"

// The variable that carries the source order into every facts container: the
// block of each Ready source the Library names, in spec.sources order,
// separated by commas. A container needs it because the two rules for who
// answers read the Library's own order, and a container holds no API
// credential to read the Library itself.
const librarySourcesVariable = "LIBRARY_SOURCES"

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

// The whole provider environment of a facts container: the keys, and the
// order the blocks are asked in. Both come from one walk of spec.sources, so
// the container asks in the order a person wrote.
func providerEnv(library *Library, providers providerSet) []EnvVar {
	return append(providerKeyEnv(library, providers),
		EnvVar{Name: librarySourcesVariable, Value: strings.Join(sourceBlocks(library, providers), ",")})
}

// Which blocks reach the container: the block of every Ready source the
// Library names, in order, with the first account of a block winning, as the
// keys do. A block that takes no account is named here too, because a
// provider with no key still answers.
func sourceBlocks(library *Library, providers providerSet) []string {
	blocks := []string{}
	held := map[string]bool{}
	for _, name := range library.Spec.Sources {
		provider, exists := providers[libraryKey(library.Metadata.Namespace, name)]
		if !exists || !provider.ready() {
			continue
		}
		block := provider.block()
		if block == "" || held[block] {
			continue
		}
		held[block] = true
		blocks = append(blocks, block)
	}
	return blocks
}

// A comma-separated list with every empty name dropped, so a trailing comma
// or a space around a name names nothing.
func commaNames(list string) []string {
	var names []string
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}
