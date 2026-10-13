package astrophotography

// A Target's spec.fitsKeywords is the last level of the declared
// keywords, after the four of observatory-operator (plan 01). Its CRD
// refuses what observatory-operator's levels refuse, and two more names
// that this operator writes into every frame: OBJECT, from
// spec.object, and FRAMEID, the frame's identity.

import (
	"slices"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// OperatorFITSKeywords are the keywords that the runner writes into
// every frame after the merged list.
var OperatorFITSKeywords = []string{"OBJECT", "FRAMEID"}

// RefusedFITSKeywords answers the names that the Target's CRD refuses:
// observatory-operator's list, then each of OperatorFITSKeywords that
// the list does not hold already.
func RefusedFITSKeywords() []string {
	refused := observatory.RefusedFITSKeywords()
	for _, name := range OperatorFITSKeywords {
		if !slices.Contains(refused, name) {
			refused = append(refused, name)
		}
	}
	return refused
}
