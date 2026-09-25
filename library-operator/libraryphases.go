package main

// libraryphases.go is the table of the phases a library Job runs: what each
// phase fills, when a Library's sources serve it, and which phases it needs
// before its last pass. The Job builder reads the table to choose the
// containers, and the scheduler reads it to choose the phases of a Job that
// fills gaps.

import (
	"slices"
)

// One phase of a library Job. The name is the container's name, and every
// container of the Job knows its phase by that name.
type phaseSpec struct {
	name string
	// The facts the phase runs where the Library's sources serve them, in
	// the order the container runs them. An empty list is a phase this
	// Library does not run.
	facts func(library *Library, providers providerSet) []string
	// The phases whose rows open this phase's gap. A phase ends only after
	// each of them has ended.
	needs []string
}

// The phases, in the order the pod lists them. The order is for a person
// who reads the pod; every phase starts at the same time.
//
// A phase needs another where its gap query reads a row the other writes:
// nfo, art, and trailer ask a provider by the id identity writes, marks
// reads the id and the length the probe measures, contributors reads the
// people the credits fact writes, trickplay reads the length, and the trailer
// files read the addresses the trailer fact writes. Every phase needs the
// walk, through the phases it needs or on its own.
var libraryPhases = []phaseSpec{
	{name: factProbe, needs: []string{scanPhase}, facts: always(factProbe)},
	{name: arrivalContainerName, needs: []string{scanPhase}, facts: always(factArrival)},
	{name: factIdentity, needs: []string{scanPhase}, facts: servedFact(factIdentity)},
	{name: nfoContainerName, needs: []string{factIdentity}, facts: servedNFOFacts},
	{name: artContainerName, needs: []string{factIdentity}, facts: servedArtFacts},
	{name: trailerContainerName, needs: []string{factIdentity}, facts: servedFact(factTrailer)},
	{name: marksContainerName, needs: []string{factIdentity, factProbe}, facts: servedFact(factMarks)},
	{name: contributorsContainerName, needs: []string{nfoContainerName}, facts: servedContributorFacts},
	{name: trickplayContainerName, needs: []string{factProbe}, facts: trickplayFacts},
	{name: trailerFileContainerName, needs: []string{trailerContainerName}, facts: trailerFileFacts},
}

// A phase that asks no provider, which every Library runs.
func always(fact string) func(*Library, providerSet) []string {
	return func(*Library, providerSet) []string { return []string{fact} }
}

// A phase of one fact, which runs where a Ready source of the Library serves
// the fact.
func servedFact(fact string) func(*Library, providerSet) []string {
	return func(library *Library, providers providerSet) []string {
		if providers.serving(library.Metadata.Namespace, library.Spec.Sources, fact) == nil {
			return nil
		}
		return []string{fact}
	}
}

// The people facts, where a Ready source serves any of them.
func servedContributorFacts(library *Library, providers providerSet) []string {
	if providers.servingContributors(library.Metadata.Namespace, library.Spec.Sources) == nil {
		return nil
	}
	return contributorFactNames
}

// The trickplay fact, where the Library turns it on.
func trickplayFacts(library *Library, _ providerSet) []string {
	if !library.Spec.Trickplay.Enabled {
		return nil
	}
	return []string{factTrickplay}
}

// The trailer files, where the Library turns them on and a Ready source
// names a site the fact downloads from. A gap with no site to fetch from
// stands no phase, the rule every provider fact follows.
func trailerFileFacts(library *Library, providers providerSet) []string {
	if !library.Spec.Trailers.Enabled || !trailersFetchable(library, providers) {
		return nil
	}
	return []string{factTrailerFile}
}

// Whether any Ready source of this Library names a block that one of the
// trailerFetchers serves. LIBRARY_SOURCES carries those blocks into the
// container.
func trailersFetchable(library *Library, providers providerSet) bool {
	blocks := trailerFetchBlocks()
	for _, block := range sourceBlocks(library, providers) {
		if blocks[block] {
			return true
		}
	}
	return false
}

// One phase a Job includes: its spec and the facts it runs for this Library.
type servedPhase struct {
	phaseSpec
	served []string
}

// The phases the Library's sources serve, in the table's order.
func servedPhases(library *Library, providers providerSet) []servedPhase {
	var phases []servedPhase
	for _, phase := range libraryPhases {
		if facts := phase.facts(library, providers); len(facts) > 0 {
			phases = append(phases, servedPhase{phaseSpec: phase, served: facts})
		}
	}
	return phases
}

// Every phase one phase waits for, through the whole table, so a phase
// whose direct need is absent from the Job still waits for what that need
// waited for. Only the phases the Job includes are kept, because a phase
// the Job leaves out never writes a mark.
func phaseNeedsOf(name string, included []string) []string {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(phase string) {
		for _, spec := range libraryPhases {
			if spec.name != phase {
				continue
			}
			for _, need := range spec.needs {
				if !seen[need] {
					seen[need] = true
					walk(need)
				}
			}
		}
	}
	walk(name)
	var needs []string
	for _, phase := range included {
		if seen[phase] {
			needs = append(needs, phase)
		}
	}
	return needs
}

// Whether one phase has work in the last report: a fact whose gap the
// reporter counted, or whose refresh time has titles left to ask about.
func phaseGapOpen(library *Library, report *libraryReport, facts []string) bool {
	return slices.ContainsFunc(phaseGapNames(facts), func(gap string) bool {
		return report.Gaps[gap] > 0 || refreshHasWork(library, report, gap)
	})
}
