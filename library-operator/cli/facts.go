package main

import "slices"

// The facts a MetadataProvider may name, in the operator's own order.
// The CLI shares no code with the operator, so this list copies the
// operator's factVocabulary, and a fact the operator adds is a fact
// added here. reenrich with no --only sets every one.
var factVocabulary = []string{
	"probe",
	"arrival",
	"trickplay",
	"identity",
	"overview",
	"certification",
	"rating.tmdb",
	"rating.imdb",
	"rating.rottentomatoes",
	"rating.metacritic",
	"credits",
	"alternate-titles",
	"poster",
	"backdrop",
	"logo",
	"clearart",
	"banner",
	"landscape",
	"discart",
	"season-poster",
	"season-banner",
	"episode-thumb",
	"trailer",
	"marks",
	"contributor.ids",
	"contributor.biography",
	"contributor.headshot",
}

// The facts no provider serves, a copy of the operator's unservedFacts.
// reenrich with no --only leaves them out, because a refresh of one of them
// works every video again: the appearances and titles workers decode each
// one. reenrich takes each of them with --only. A retry reopens only what
// a fact missed or failed on, so retry with no --only names them too.
var unservedFacts = []string{
	"trailerfile",
	"appearances",
	"titles",
}

// Every fact --only may name, for reenrich and for retry, and the facts
// retry sets with no --only. The walk records no attempt, so scan is out.
var retryFactVocabulary = append(slices.Clone(factVocabulary), unservedFacts...)
