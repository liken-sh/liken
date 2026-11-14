package main

// titlesfit.go decides when a Library fits a model of its end titles. The
// model is the file that `titles place` reads, .liken/titles/model.json
// under the Library's root, and `titles train` fits it on the Library's own
// videos: each video with a reading, which the titles worker wrote, and a
// provider's mark of where its end titles start, which is the label. Each
// Library fits its own model, as each fact of a Library works on that
// Library's volume alone.
//
// The reporter counts those videos in the catalog and publishes the count
// in the Library's report, so a report that arrives is the event a fit
// starts on, and nothing reads the count on a timer.

import (
	"context"
	"math"
)

// How many videos with a reading and a mark a Library holds, and how many
// films and series they are of.
type titlesMarked struct {
	Videos int `json:"videos"`
	Groups int `json:"groups"`
}

// The numbers the fit is decided by. Plan 96 measured each on one library of
// 1,297 marked videos, under "How many videos a model needs", and a person
// who has measured another library can change them here.
const (
	// The fewest videos, and the fewest films and series, of a first fit. A
	// model of about 100 videos of 32 films and series placed 89% of the
	// checked videos within 10 seconds, where the model of every video
	// placed 92%. The floor counts films and series as well as videos,
	// because the episodes of one series show one style of titles, and a
	// worker can read one long series first.
	titlesFitVideos = 100
	titlesFitGroups = 30
	// How far the count must grow before a Library fits again: by half. The
	// score stopped rising at about 300 videos, so a Library fits a few
	// times while the worker reads it for the first time, and seldom after.
	titlesFitGrowth = 1.5
)

// Whether a Library with this count fits a model, where the model it holds
// was fitted on fitted videos, and 0 where it holds none.
func titlesFitDue(marked titlesMarked, fitted int) bool {
	if marked.Videos < titlesFitVideos || marked.Groups < titlesFitGroups {
		return false
	}
	return fitted == 0 || float64(marked.Videos) >= titlesFitGrowth*float64(fitted)
}

// The step of the count that a pass can decide on. The count rises by one
// with each video the worker reads, and a pass for each video would decide
// nothing new. So the report desk wakes a pass when the count passes a
// floor of the first fit, and then each time it has grown by a quarter,
// which is finer than the growth a fit waits for.
func (m titlesMarked) step() int {
	if m.Videos < titlesFitVideos || m.Groups < titlesFitGroups {
		return 0
	}
	return 1 + int(math.Log(float64(m.Videos)/titlesFitVideos)/math.Log(1.25))
}

// The marks that are labels: a provider's mark of the end titles with a
// start. The estimate's rows and the rows this fact writes are derived from
// the labels and from the model, so they are none.
const titlesMarkedSQL = `SELECT count(DISTINCT m.path), count(DISTINCT COALESCE(e.series, i.item, m.path)) ` +
	`FROM marks AS m ` +
	`JOIN attempts AS a ON a.library = m.library AND a.item = m.path ` +
	`AND a.` + attemptFactColumn + ` = '` + factTitles + `' AND a.result = '` + attemptFound + `' ` +
	`LEFT JOIN file_items AS i ON i.library = m.library AND i.path = m.path ` +
	`LEFT JOIN episodes AS e ON e.library = i.library AND e.id = i.item ` +
	`WHERE m.library = ?1 AND m.kind = '` + markKindCredits + `' AND m.start_ms IS NOT NULL ` +
	`AND m.source NOT IN ('` + markSourceEstimate + `', '` + markSourceFaces + `', '` + markSourceTitles + `')`

// The source of the marks this fact writes: the placements of the model.
const markSourceTitles = "titles"

// The count of one Library's videos that a fit can learn from.
func (c *Catalog) titlesMarked(ctx context.Context, library string) (titlesMarked, error) {
	var marked titlesMarked
	err := c.stream(ctx, titlesMarkedSQL, []any{library}, func(cells []any) error {
		if len(cells) >= 2 {
			marked = titlesMarked{Videos: int(cellNumber(cells[0])), Groups: int(cellNumber(cells[1]))}
		}
		return nil
	})
	return marked, err
}
