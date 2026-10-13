// Package solver is the binary's solver mode: the pod of a Solver,
// which runs one astrometry-engine for each star list that the operator
// sends, one at a time (plan 02). This file holds the HTTP contract
// between the solver and the operator, its only client.
//
// Every answer to a request that fails is a status other than 2xx with
// a line of text that says why. A field that does not solve is no
// failure: it is a SolveAnswer with Solved false.
package solver

import (
	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
)

// Port is the solver's port, which its Service serves.
const Port = 8080

// The paths that the solver serves.
const (
	// PathSolve takes a POST whose body is one .axy file, with the
	// content type application/octet-stream, and answers a SolveAnswer
	// when the engine exits. Requests wait their turn in a queue.
	PathSolve = "/solve"
	// PathIndexes takes a GET and answers the index series that the
	// solver read from its claim when it started, as a list of
	// astrophotography.IndexSeries. The operator copies it into the
	// Solver's status.indexes.
	PathIndexes = "/indexes"
)

// SolveAnswer is the engine's solution of one star list, from the WCS
// file it wrote.
type SolveAnswer struct {
	// Solved is false when the engine wrote no WCS file, and every other
	// field is then empty.
	Solved bool `json:"solved"`
	// Center is the center of the frame, J2000, in degrees: CRVAL1 and
	// CRVAL2 of a solve with --crpix-center.
	Center *sky.Position `json:"center,omitempty"`
	// Scale is the plate scale, in arcseconds per pixel, and Rotation the
	// angle of the frame on the sky, in degrees, both from the CD matrix.
	Scale    float64 `json:"scale,omitempty"`
	Rotation float64 `json:"rotation,omitempty"`
	// Parity is ParityPositive or ParityNegative, the handedness of the
	// image on the sky.
	Parity string `json:"parity,omitempty"`
	// Index is the index series that solved it, such as 4207.
	Index int32 `json:"index,omitempty"`
}

// The two parities of an image, in astrometry.net's words.
const (
	ParityPositive = "positive"
	ParityNegative = "negative"
)

// Indexes is the answer of PathIndexes.
type Indexes = []astrophotography.IndexSeries
