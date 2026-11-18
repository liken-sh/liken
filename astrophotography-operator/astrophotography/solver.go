package astrophotography

// A Solver is a set of astrometry.net index files on a claim. The
// operator runs one solver pod for each Solver, and the pod solves the
// star lists of every Target in the namespace that names the Solver.

import "github.com/liken-sh/liken/observatory-operator/observatory"

type Solver = Object[SolverSpec, SolverStatus]

type SolverSpec struct {
	IndexFiles IndexFiles `json:"indexFiles"`
	// Search sets how far from the expected position a solve looks. The
	// CRD supplies a default when it is omitted.
	Search Search `json:"search,omitzero"`
}

// Search is the order of attempts to solve one frame. The operator
// first asks for a solve within Radii[0] of the position it expects,
// then within each next radius while the frame does not solve, and
// last over the whole sky when Blind is true. A wider search spends
// more solver time on a frame that does not solve, and other requests
// wait in the solver's queue meanwhile.
type Search struct {
	// Radii are the radii of the attempts, in degrees, in order.
	Radii []float64 `json:"radii,omitempty"`
	// Blind adds a last attempt over the whole sky.
	Blind bool `json:"blind,omitempty"`
}

// IndexFiles is a claim, and a directory in it, that holds index files.
// The solver reads every index-*.fits file in the directory and watches
// the directory for changes.
type IndexFiles struct {
	ClaimName string `json:"claimName"`
	Path      string `json:"path,omitempty"`
}

type SolverStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Endpoint is the solver's Service. It is set only while the pod is
	// ready.
	Endpoint *observatory.Endpoint `json:"endpoint,omitempty"`
	// Indexes lists each index series in the claim. The solver reads the
	// series from each file's headers, and the list changes when a file
	// is added to or removed from the directory.
	Indexes []IndexSeries `json:"indexes,omitempty"`
	// Series joins the series IDs of Indexes into one string, such as
	// "4207,4208", for the INDEXES column of kubectl get. A printer
	// column over a list shows only the first value.
	Series     string      `json:"series,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// IndexSeries is one series of index files, such as 4207, and the range
// of quad sizes that it solves.
type IndexSeries struct {
	// ID is the series, from each file's INDEXID header.
	ID int32 `json:"id"`
	// Files is the number of files in the series: one, or one for each
	// HEALPix tile.
	Files       int32      `json:"files"`
	ScaleArcmin ScaleRange `json:"scaleArcmin"`
}

// ScaleRange is the range of a series' quad sizes, in arcminutes.
type ScaleRange struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}
