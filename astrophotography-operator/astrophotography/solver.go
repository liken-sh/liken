package astrophotography

// A Solver holds astrometry.net's index files on a claim, and the
// operator runs one pod that solves the star lists of every target in
// its namespace that names it (plan 02).

import "github.com/liken-sh/liken/observatory-operator/observatory"

type Solver = Object[SolverSpec, SolverStatus]

type SolverSpec struct {
	IndexFiles IndexFiles `json:"indexFiles"`
	// Search is how far from the expected center a solve looks. The CRD
	// fills it in when a person leaves it out.
	Search Search `json:"search,omitzero"`
}

// Search is the order of the attempts to solve one frame. The operator
// asks for the first radius around the position it expects. While the
// frame does not solve, it asks for each next radius, and then, when
// Blind is true, for a solve of the whole sky. A wider search costs
// the solver's queue more time for a frame that does not solve.
type Search struct {
	// Radii are the radii of the attempts, in degrees, in order.
	Radii []float64 `json:"radii,omitempty"`
	// Blind adds an attempt with no position after the last radius.
	Blind bool `json:"blind,omitempty"`
}

// IndexFiles is a claim, and a directory in it, that holds index files.
// The solver reads every index-*.fits in the directory, and watches it.
type IndexFiles struct {
	ClaimName string `json:"claimName"`
	Path      string `json:"path,omitempty"`
}

type SolverStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Endpoint is the solver's Service, while its pod is ready.
	Endpoint *observatory.Endpoint `json:"endpoint,omitempty"`
	// Indexes lists each index series in the claim, which the solver
	// reads from each file's headers. It changes when a file joins or
	// leaves the directory.
	Indexes []IndexSeries `json:"indexes,omitempty"`
	// Series names the series of Indexes in one string, such as
	// "4207,4208", for the INDEXES column of kubectl get. The API server
	// prints only the first value of a column over a list.
	Series     string      `json:"series,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
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
