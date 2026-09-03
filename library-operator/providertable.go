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
	providerBlockTMDb: {factIdentity},
}

// The block names of the table's rows, which are the field names of
// MetadataProviderSpec.
const providerBlockTMDb = "tmdb"

// The block this provider names. A provider that names none has no row in the
// table and serves no fact.
func (p *MetadataProvider) block() string {
	if p.Spec.TMDb != nil {
		return providerBlockTMDb
	}
	return ""
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
