package main

// The FITS keywords of a frame. The camera's driver writes every
// keyword that a client sent into all later frames, until a client
// sends INDI_CLEAR, which also removes the keywords that
// observatory-operator wrote. So that a keyword of one target does not
// reach the frames of the next, the runner clears the keywords before
// each exposure and writes them all again as one merged list: the
// session's keywords for the camera, then the target's, OBJECT, and a
// new FRAMEID.

import "github.com/liken-sh/liken/observatory-operator/observatory"

// clearKeywords is the keyword name that tells libindi's CCD, in a
// write to FITS_HEADER, to remove all keywords that clients wrote
// earlier (libs/indibase/indiccd.cpp).
const clearKeywords = "INDI_CLEAR"

// frameKeywords returns the writes to FITS_HEADER before one exposure,
// in order: the clear, then the merged list. A later level replaces the
// value of a name that an earlier level has, at the earlier position.
// The CRDs refuse FRAMEID in spec.fitsKeywords, so it is the last card.
func frameKeywords(session, target []observatory.FITSKeyword, object, frameID string) []observatory.FITSKeyword {
	merged := observatory.MergeFITSKeywords(session, target,
		[]observatory.FITSKeyword{{Name: "OBJECT", Value: object, Comment: "observed object"}},
		[]observatory.FITSKeyword{{Name: "FRAMEID", Value: frameID, Comment: "frame identity"}},
	)
	return append([]observatory.FITSKeyword{{Name: clearKeywords}}, merged...)
}
