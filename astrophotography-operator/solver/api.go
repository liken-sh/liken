// Package solver is the binary's solver mode. It runs in the pod of a
// Solver and runs astrometry-engine on each star list that the operator
// sends, one list at a time. This file defines the HTTP API that the
// operator calls.
//
// A request that fails gets a status other than 2xx and a line of text
// that gives the reason. A frame that cannot be solved is not a
// failure: the response is a SolveResponse with Solved false.
package solver

import (
	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
)

// Port is the port that the solver listens on and its Service exposes.
const Port = 8080

const (
	// PathSolve takes a POST whose body is one .axy file, and responds
	// with a SolveResponse when the engine exits. The engine runs one solve
	// at a time, so a second request waits for the first to finish.
	PathSolve = "/solve"
	// PathIndexes takes a GET and responds with the index series that the
	// solver has loaded. With the query watch=true, the response stays
	// open and sends one line of JSON with the current list, then one
	// line each time the list changes. The operator keeps one such
	// request open and copies each list into the Solver's
	// status.indexes.
	PathIndexes = "/indexes"
)

// SolveResponse is the solution of one star list, read from the WCS file
// that the engine wrote.
type SolveResponse struct {
	// Solved is false when the engine wrote no WCS file. Every other
	// field is then empty.
	Solved bool `json:"solved"`
	// Center is the center of the frame in degrees, J2000. It is CRVAL1
	// and CRVAL2 of a solve with --crpix-center.
	Center *sky.Position `json:"center,omitempty"`
	// Scale is the plate scale in arcseconds per pixel. Rotation is the
	// angle of the frame on the sky in degrees. Both come from the CD
	// matrix.
	Scale    float64 `json:"scale,omitempty"`
	Rotation float64 `json:"rotation,omitempty"`
	// Parity is ParityPositive when the determinant of the CD matrix is
	// negative, which means east is to the left of north as on the sky,
	// and ParityNegative otherwise.
	Parity string `json:"parity,omitempty"`
	// Index is the ID of the index series that solved the frame, such as
	// 4207.
	Index int32 `json:"index,omitempty"`
}

// The values of SolveResponse.Parity, as astrometry.net names them.
const (
	ParityPositive = "positive"
	ParityNegative = "negative"
)

type Indexes = []astrophotography.IndexSeries
