package main

// The provider keys the enricher's containers read. Every container that asks
// a provider reads the same set, so the Job builder calls one function and a
// new container gains the keys with it.
// The rest of the set travels the same way: the source order, the languages,
// and the address of every source that names one. The container's own read of
// the set is the line of answerers it asks.

import (
	"slices"
	"strings"
)

// The variable that carries the source order into every facts container: the
// block of each Ready source the Library names, in spec.sources order,
// separated by commas. A container needs it because the two rules for who
// answers read the Library's own order, and a container holds no API
// credential to read the Library itself.
const librarySourcesVariable = "LIBRARY_SOURCES"

// The variable that carries the languages a container prefers, most preferred
// first, separated by commas.
const libraryLanguagesVariable = "LIBRARY_LANGUAGES"

// The language a container reads where the library and the household name
// none.
const defaultLanguage = "en"

// The variable one provider block's key travels in: the block name in
// capitals, then _TOKEN. TMDB_TOKEN, the one the identity fact reads, is that
// rule for tmdb.
func providerTokenVariable(block string) string {
	return strings.ToUpper(block) + "_TOKEN"
}

// The variable one provider block's address travels in: the block name in
// capitals, then _ENDPOINT. A block whose spec names an address names no
// Secret, so the address is the whole account.
func providerEndpointVariable(block string) string {
	return strings.ToUpper(block) + "_ENDPOINT"
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
// The languages travel with the keys, from the same call.
func providerEnv(library *Library, providers providerSet, languages []string) []EnvVar {
	env := append(providerKeyEnv(library, providers),
		EnvVar{Name: librarySourcesVariable, Value: strings.Join(sourceBlocks(library, providers), ",")},
		EnvVar{Name: libraryLanguagesVariable,
			Value: strings.Join(libraryLanguages(library, languages), ",")})
	return append(env, providerEndpointEnv(library, providers)...)
}

// The library's own languages first, then the household's that the library
// did not name, and English where neither names one.
func libraryLanguages(library *Library, household []string) []string {
	union := []string{}
	held := map[string]bool{}
	for _, tag := range slices.Concat(library.Spec.Languages, household) {
		key := strings.ToLower(tag)
		if key == "" || held[key] {
			continue
		}
		held[key] = true
		union = append(union, tag)
	}
	if len(union) == 0 {
		return []string{defaultLanguage}
	}
	return union
}

// The address of the first Ready source of each block that names one, in
// spec.sources order. The first one wins for the same reason the first key of
// a block wins: one variable holds one value.
// A block that is one service at one fixed address contributes none.
func providerEndpointEnv(library *Library, providers providerSet) []EnvVar {
	addresses := []EnvVar{}
	held := map[string]bool{}
	for _, name := range library.Spec.Sources {
		provider, exists := providers[libraryKey(library.Metadata.Namespace, name)]
		if !exists || !provider.ready() {
			continue
		}
		block := provider.block()
		if !blockOf(block).endpoint || held[block] {
			continue
		}
		held[block] = true
		if endpoint := provider.endpoint(); endpoint != "" {
			addresses = append(addresses, EnvVar{
				Name: providerEndpointVariable(block), Value: endpoint})
		}
	}
	return addresses
}

// The answerers of one container's line, in the order LIBRARY_SOURCES names
// the blocks, which is the Library's own spec.sources order, and the rules
// for who answers read that order. A block this line has no answerer for, a
// keyed block whose token did not reach the container, and an endpoint block
// whose address did not reach it, are all skipped with no error.
// Each line brings a table of its own, because the nfo facts, the art, and
// the trailers are three interfaces.
func answerersOf[A any](blocks []string, value func(string) string,
	answerers map[string]func(base, token string) A) []A {
	var line []A
	for _, name := range blocks {
		build, held := answerers[name]
		if !held {
			continue
		}
		block := blockOf(name)
		token := ""
		if block.key {
			if token = value(providerTokenVariable(name)); token == "" {
				continue
			}
		}
		base := block.base
		if block.endpoint {
			if base = value(providerEndpointVariable(name)); base == "" {
				continue
			}
		}
		line = append(line, build(base, token))
	}
	return line
}

// recordingAnswerers builds the same line from a table whose entries take the
// recorder their client counts requests into. The selection rules are the
// ones answerersOf applies, so a table of either shape reaches the container
// the same way.
func recordingAnswerers[A any](blocks []string, value func(string) string, record *tallies,
	answerers map[string]func(base, token string, record *tallies) A) []A {
	built := make(map[string]func(base, token string) A, len(answerers))
	for name, build := range answerers {
		built[name] = func(base, token string) A { return build(base, token, record) }
	}
	return answerersOf(blocks, value, built)
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
