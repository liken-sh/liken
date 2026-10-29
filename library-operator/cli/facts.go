package main

import "slices"

// The facts spec.refresh may name, in the operator's own order.
// The CLI shares no code with the operator, so this list copies the
// operator's factVocabulary, and a fact the operator adds is a fact
// added here. reenrich with no --only sets every one; trailerfile is
// out, because it is out of spec.refresh there too.
var refreshFactVocabulary = []string{
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

// The worker facts spec.retry may name beside the facts, a copy of the
// operator's workerRefreshFacts. A retry reopens only the titles a fact
// missed, so the appearances worker, which decodes every frame, costs only
// those titles, and retry names it where reenrich leaves it out.
var workerRetryFacts = []string{
	"appearances",
}

// The facts spec.retry may name. The walk records no attempt, so scan is
// out.
var retryFactVocabulary = append(slices.Clone(refreshFactVocabulary), workerRetryFacts...)
