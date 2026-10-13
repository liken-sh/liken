package main

// The keywords of a frame (plan 01). The camera's driver writes each
// keyword a client gave it into every later frame, until a client
// clears them all with INDI_CLEAR, and the clear removes the keywords
// of observatory-operator too. So before each exposure the runner
// clears the camera's keywords and writes one merged list, from the
// least specific level to the most: the session's keywords for the
// camera, the target's, OBJECT, and a new FRAMEID. A keyword of one
// target then never reaches the frames of the next, and no earlier
// write leaks into a frame.

import "github.com/liken-sh/liken/observatory-operator/observatory"

// clearKeywords is the name that libindi's CCD reads, in a write to
// FITS_HEADER, as a request to remove every keyword that a client wrote
// before (libs/indibase/indiccd.cpp).
const clearKeywords = "INDI_CLEAR"

// frameKeywords answers the writes to FITS_HEADER before one exposure,
// in order: the clear, then the merged list. The merge keeps one card
// for each name, at the place its name first appears, so FRAMEID, which
// no level declares, is the last write.
func frameKeywords(session, target []observatory.FITSKeyword, object, frameID string) []observatory.FITSKeyword {
	merged := observatory.MergeFITSKeywords(session, target,
		[]observatory.FITSKeyword{{Name: "OBJECT", Value: object, Comment: "observed object"}},
		[]observatory.FITSKeyword{{Name: "FRAMEID", Value: frameID, Comment: "frame identity"}},
	)
	return append([]observatory.FITSKeyword{{Name: clearKeywords}}, merged...)
}
