package main

// workgap.go is one heavy fact's gap as the close container of a library Job
// lists it: the videos that still need the fact, read from the Job's copy of
// the catalog with the gap query the reporter counts the gap with. The gap is
// sorted by path, so the videos of one title folder sit at neighboring
// indexes of the list (worklist.go).

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// One video of a gap, and the payload of its message on the bus. The size is
// the size the walk read, which is the size the probe recorded wherever the
// length comes from a probe record, because the walk takes no length from a
// record of another size. The worker compares it with the file on the volume
// before it opens the file. Listed is the time the gap counts from, so the
// worker can tell an attempt that the catalog had not read when the gap was
// taken from one the gap already took into account. The worker reads it from
// its Job, so the message does not carry it.
type workItem struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	DurationMs int64     `json:"durationMs"`
	Listed     time.Time `json:"-"`
}

// The length of the video, which says how many thumbnails cover it.
func (i workItem) duration() time.Duration {
	return time.Duration(i.DurationMs) * time.Millisecond
}

// One fact's gap out of the local copy of the catalog. A worker fact's gap
// query selects the path, the size, and the length of each video, in that
// order.
func (c *Catalog) workItems(ctx context.Context, fact, library string, now, refresh time.Time) ([]workItem, error) {
	var items []workItem
	err := c.stream(ctx, gapQueries[fact], gapParams(fact, library, now, refresh), func(cells []any) error {
		if len(cells) < 3 {
			return nil
		}
		path, _ := cells[0].(string)
		if path == "" {
			return nil
		}
		items = append(items, workItem{
			Path:       path,
			Size:       cellNumber(cells[1]),
			DurationMs: cellNumber(cells[2]),
			Listed:     now,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the %s gap of %s: %w", fact, library, err)
	}
	slices.SortFunc(items, func(a, b workItem) int { return strings.Compare(a.Path, b.Path) })
	return items, nil
}
