package main

// The nfo facts this wave fills, the list a sidecar answers, and the gap
// query each fact works from. The full fact vocabulary lives in factnames.go.
// These names are the ones the nfo container runs today.

import "strings"

// The nfo facts this image runs today, in the order the container names them
// in LIBRARY_FACTS. overview runs first because it writes the elements every
// other reader looks for.
var nfoFacts = []string{
	factOverview, factCertification,
	factRatingTMDb, factRatingIMDb, factRatingRottenTomatoes, factRatingMetacritic,
	factCredits,
}

// The container that fills the .nfo body, which is the phase a person reads
// in kubectl get pod.
const nfoContainerName = "nfo"

// The nfo facts the Library's own sources serve, in the order the group runs
// them. A Library whose sources hold one provider asks for what that provider
// serves and no more.
func servedNFOFacts(library *Library, providers providerSet) []string {
	var served []string
	for _, fact := range nfoFacts {
		if providers.serving(library.Metadata.Namespace, library.Spec.Sources, fact) != nil {
			served = append(served, fact)
		}
	}
	return served
}

// How the list is written into the nfo_facts column: every name wrapped in
// commas, so instr() matches a whole name and never a prefix of one. An empty
// list is an empty string.
const nfoFactSeparator = ","

func nfoFactList(facts []string) string {
	if len(facts) == 0 {
		return ""
	}
	return nfoFactSeparator + strings.Join(facts, nfoFactSeparator) + nfoFactSeparator
}

// Which nfo facts a sidecar already answers, read from the elements the
// sidecar holds. The scanner writes the answer into the nfo_facts column. The
// lead element of each group is the test: a sidecar that holds the plot holds
// the group the overview fact wrote.
func nfoFactsAnswered(plot, certification string, ratings []nfoRating, actors []nfoActor) string {
	held := map[string]bool{
		factOverview:      strings.TrimSpace(plot) != "",
		factCertification: strings.TrimSpace(certification) != "",
		factCredits:       len(castMembers(actors)) > 0,
	}
	for fact, site := range ratingSites {
		held[fact] = ratingNamed(ratings, site.name) != nil
	}
	var answered []string
	for _, fact := range nfoFacts {
		if held[fact] {
			answered = append(answered, fact)
		}
	}
	return nfoFactList(answered)
}

// The gap query of one nfo fact. A title with no provider id is not a gap,
// because a fact cannot ask about a title no provider has named, and the
// identity fact fills that gap first. The query reads nfo_facts with instr(),
// and it excludes a title with an attempt inside that attempt's own window.
func nfoGapQuery(fact string) string {
	return `SELECT id FROM (` +
		`SELECT library, id, nfo_facts FROM movies WHERE id NOT LIKE 'movie:path:%' ` +
		`UNION ALL SELECT library, id, nfo_facts FROM series WHERE id NOT LIKE 'series:path:%') AS items ` +
		`WHERE library = ?1 AND instr(nfo_facts, '` + nfoFactSeparator + fact + nfoFactSeparator + `') = 0 ` +
		`AND ` + attemptClause(fact, "id")
}

// The count of fights every fact of one library recorded. The reporter
// publishes it and the operator folds it into Library status.
const fightsQuery = `SELECT count(*) FROM attempts WHERE library = ? AND result = '` + attemptFight + `'`
