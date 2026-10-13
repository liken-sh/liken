package solver

// The index files. The solver reads the header of every index-*.fits
// in its claim's directory once, when it starts: the series from
// INDEXID, and the range of quad sizes from SCALE_L and SCALE_U. It
// names each file in the engine's configuration, so the engine opens
// exactly the files that GET /indexes reports. A person who adds files
// restarts the solver's Deployment, and the new pod reads them.

import (
	"cmp"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/fits"
)

// IndexFile is one index file and what its header says.
type IndexFile struct {
	Path string
	// ID is the series, such as 4207, from INDEXID.
	ID int32
	// Low and High bound the sizes of the file's quads, in arcminutes.
	// The header gives them in radians.
	Low, High float64
}

// ScanIndexes reads the header of every index-*.fits in dir, in the
// order of their names. A file whose header does not read is an error,
// because the engine would fail on it too.
func ScanIndexes(dir string) ([]IndexFile, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "index-*.fits"))
	if err != nil {
		return nil, err
	}
	files := make([]IndexFile, 0, len(paths))
	for _, path := range paths {
		file, err := readIndex(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		files = append(files, file)
	}
	return files, nil
}

func readIndex(path string) (IndexFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return IndexFile{}, err
	}
	defer func() { _ = f.Close() }()
	h, err := fits.ReadHeader(f)
	if err != nil {
		return IndexFile{}, err
	}
	id, err := h.Int("INDEXID")
	if err != nil {
		return IndexFile{}, err
	}
	low, err := h.Float("SCALE_L")
	if err != nil {
		return IndexFile{}, err
	}
	high, err := h.Float("SCALE_U")
	if err != nil {
		return IndexFile{}, err
	}
	return IndexFile{Path: path, ID: int32(id), Low: arcminutes(low), High: arcminutes(high)}, nil
}

// arcminutes converts radians, rounded to a hundredth of an arcminute,
// so 0.00639954 radians reads as the 22 that the series' name promises.
func arcminutes(radians float64) float64 {
	return math.Round(radians*180/math.Pi*60*100) / 100
}

// Series groups the files by series, in the order of the series, with
// the range that covers every file of each.
func Series(files []IndexFile) Indexes {
	var out Indexes
	for _, file := range files {
		at := slices.IndexFunc(out, func(s astrophotography.IndexSeries) bool { return s.ID == file.ID })
		if at < 0 {
			out = append(out, astrophotography.IndexSeries{ID: file.ID, ScaleArcmin: astrophotography.ScaleRange{Low: file.Low, High: file.High}})
			at = len(out) - 1
		}
		series := &out[at]
		series.Files++
		series.ScaleArcmin.Low = min(series.ScaleArcmin.Low, file.Low)
		series.ScaleArcmin.High = max(series.ScaleArcmin.High, file.High)
	}
	slices.SortFunc(out, func(a, b astrophotography.IndexSeries) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// EngineConfig is the configuration that astrometry-engine reads with
// -c: one index line for each file, by its full path, and the CPU limit
// of one field. The engine lowers a job's own limit, the ANCLIM of its
// .axy, to this one, so no .axy can hold the queue for longer.
func EngineConfig(files []IndexFile, cpuLimit int) string {
	var b strings.Builder
	for _, file := range files {
		fmt.Fprintf(&b, "index %s\n", file.Path)
	}
	fmt.Fprintf(&b, "cpulimit %d\n", cpuLimit)
	return b.String()
}
