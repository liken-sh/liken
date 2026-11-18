package astrophotography

// A Target's spec.fitsKeywords is the most specific level of declared
// FITS keywords. The less specific levels are the same field on
// observatory-operator's Observatory, Telescope, Camera, and
// Reservation. The Target CRD refuses every name that those refuse, and
// also OBJECT and FRAMEID, because the runner writes them into every
// frame from spec.object and the frame's identity.

import (
	"slices"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// OperatorFITSKeywords are the keywords that the runner writes into
// every frame after the Target's keywords.
var OperatorFITSKeywords = []string{"OBJECT", "FRAMEID"}

// RefusedFITSKeywords returns the names that the Target CRD refuses:
// observatory-operator's list, plus each of OperatorFITSKeywords that
// the list lacks.
func RefusedFITSKeywords() []string {
	refused := observatory.RefusedFITSKeywords()
	for _, name := range OperatorFITSKeywords {
		if !slices.Contains(refused, name) {
			refused = append(refused, name)
		}
	}
	return refused
}
