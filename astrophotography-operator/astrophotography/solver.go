package astrophotography

// A Solver holds astrometry.net's index files on a claim, and the
// operator runs one pod that solves the star lists of every target in
// its namespace that names it (plan 02).

import "github.com/liken-sh/liken/observatory-operator/observatory"

type Solver = Object[SolverSpec, SolverStatus]

type SolverSpec struct {
	IndexFiles IndexFiles `json:"indexFiles"`
}

// IndexFiles is a claim, and a directory in it, that holds index files.
// The solver reads every index-*.fits in the directory.
type IndexFiles struct {
	ClaimName string `json:"claimName"`
	Path      string `json:"path,omitempty"`
}

type SolverStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Endpoint is the solver's Service, while its pod is ready.
	Endpoint *observatory.Endpoint `json:"endpoint,omitempty"`
	// Indexes lists each index series in the claim, which the solver
	// reads from each file's headers when it starts.
	Indexes    []IndexSeries `json:"indexes,omitempty"`
	Conditions []Condition   `json:"conditions,omitempty"`
}

// IndexSeries is one series of index files, such as 4207, and the range
// of quad sizes it solves.
type IndexSeries struct {
	// ID is the series, from each file's INDEXID header.
	ID int32 `json:"id"`
	// Files counts the files of the series: one, or one for each
	// HEALPix tile.
	Files       int32      `json:"files"`
	ScaleArcmin ScaleRange `json:"scaleArcmin"`
}

// ScaleRange is the range of a series' quad sizes, in arcminutes.
type ScaleRange struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}
