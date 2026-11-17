package main

// titlesplace.go decides when a Library places its end titles with its
// model. The placing Job (titlesplacejob.go) runs `titles place` over every
// reading of the Library and writes each placement into the marks table
// (titlesplacerun.go). Each run places every reading and writes only the
// rows that changed. Plan 96 measured `place` fast enough that a whole
// library costs less than a run for its new folders would save, so the
// trigger only has to say that something changed since the last run.
//
// The reporter counts the Library's readings and its placement rows in the
// catalog and publishes them in the Library's report, so a report that
// arrives is the event a run starts on, and nothing reads the counts on a
// timer. The last run's result is in the Library's status and on the bus.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// How many readings must land since the last run before a run starts while a
// reading worker still reads. A worker that reads a whole library for the
// first time lands a reading every minute or so for days, and without this
// bound each one would start a placing Job. When no reading worker of the
// Library runs, any reading that landed starts a run, so the last videos of a
// backfill and a single new episode do not wait for the bound.
const titlesPlaceReadings = 50

// The Library's readings and placements, as the reporter counts them in the
// catalog and the placing run counts them in its copy.
type titlesReadings struct {
	// The primary videos with a reading: a found attempt of the titles fact.
	Readings int `json:"readings"`
	// When the newest of those readings landed. A file that is replaced and
	// read again leaves the count as it was and moves this time.
	NewestReading time.Time `json:"newestReading,omitzero"`
	// The rows of the marks table that hold a placement.
	Placements int `json:"placements"`
}

// The step of the counts that a pass can decide on. A reading lands every
// minute or so while a worker reads, and a pass for each one would decide
// nothing new while the worker runs. So the report desk wakes a pass each
// time titlesPlaceReadings more readings have landed. A worker that ends
// wakes a pass through its Job, and the walk that lands its last readings
// wakes one through its run.
func (c titlesReadings) step() int {
	return c.Readings / titlesPlaceReadings
}

// The primary videos of a Library that the titles worker read, with when
// each reading's attempt landed. The reporter's count and the run's list read
// the same rows, so a run's result and the next report agree.
const titlesReadingsFrom = `FROM files AS f ` +
	`JOIN attempts AS a ON a.library = f.library AND a.item = f.path ` +
	`AND a.` + attemptFactColumn + ` = '` + factTitles + `' AND a.result = '` + attemptFound + `' ` +
	`WHERE f.library = ?1 AND f.type = '` + fileTypeVideo + `' AND f.role = '` + fileRolePrimary + `' ` +
	`AND f.present = 1`

// The rows of the marks table that hold a placement.
var titlesPlacementsWhere = `WHERE library = ?1 AND ordinal = ` + strconv.Itoa(titlesPlacementOrdinal) +
	` AND kind = '` + markKindCreditsPlacement + `' AND source = '` + markSourceTitles + `'`

// The counts of one Library's readings and placements.
func (c *Catalog) titlesReadings(ctx context.Context, library string) (titlesReadings, error) {
	var counts titlesReadings
	err := c.stream(ctx, `SELECT (SELECT count(*) `+titlesReadingsFrom+`), `+
		`(SELECT IFNULL(max(a.at), 0) `+titlesReadingsFrom+`), `+
		`(SELECT count(*) FROM marks `+titlesPlacementsWhere+`)`,
		[]any{library}, func(cells []any) error {
			if len(cells) >= 3 {
				counts.Readings = int(cellNumber(cells[0]))
				if at := int64(cellNumber(cells[1])); at > 0 {
					counts.NewestReading = time.Unix(at, 0).UTC()
				}
				counts.Placements = int(cellNumber(cells[2]))
			}
			return nil
		})
	return counts, err
}

// What one placing run did, as the Library's status holds it. A run that
// failed records the counts it read, so the trigger does not start the same
// run again on the same readings.
type titlesPlaced struct {
	// When the run ended.
	At time.Time `json:"at"`
	// placed for a run that wrote its placements, and failed for one that
	// could not place, with the reason in a sentence.
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
	// When the model the run placed with was fitted, as the Library's status
	// gave it to the run.
	Model time.Time `json:"model,omitzero"`
	// The readings the run found in the catalog, and when the newest landed.
	Readings      int       `json:"readings"`
	NewestReading time.Time `json:"newestReading,omitzero"`
	// The placement rows the catalog held after the run, those with a
	// confidence of 0.6 or more, the readings that hold no end titles, and
	// the readings the catalog names and the volume does not hold.
	Placements int `json:"placements"`
	Sure       int `json:"sure"`
	None       int `json:"none"`
	Missing    int `json:"missing"`
}

const (
	titlesPlacePlaced = "placed"
	titlesPlaceFailed = "failed"
)

// What one placing run left, and the payload of its message on the bus.
type titlesPlacing struct {
	// The titles the run placed.
	Kind string `json:"kind"`
	Job  string `json:"job,omitempty"`
	titlesPlaced
}

// The annotation a placing Job carries with the model and the counts it was
// started on, as a titlesPlaced in JSON. A pass reads which model and which
// readings a Job that is still listed answers, whether or not its result
// reached the broker.
const titlesPlacingAnnotation = "library.liken.sh/titles-placing"

// The placings the trigger compares against: the status's last run, the
// broker's last result, and what each placing Job that the pass still lists
// was started on.
func titlesPlaceBaseline(held *titlesModelStatus, placing *titlesPlacing, jobs []Job) []titlesPlaced {
	var runs []titlesPlaced
	if held != nil && !held.Placed.At.IsZero() {
		runs = append(runs, held.Placed)
	}
	if placing != nil && placing.Kind == titlesKindEnd {
		runs = append(runs, placing.titlesPlaced)
	}
	for _, job := range jobs {
		var started titlesPlaced
		if json.Unmarshal([]byte(job.Metadata.Annotations[titlesPlacingAnnotation]), &started) == nil {
			runs = append(runs, started)
		}
	}
	return runs
}

// Why a run is due, in the words of the log line, or empty where none is.
// model is when the Library's model was fitted, counts what the report
// counts now, last the placings of titlesPlaceBaseline, listed whether a
// placing Job of the Library is still listed, and reading whether a reading
// worker of the Library runs.
//
// A catalog that holds fewer placements than the last run wrote is a catalog
// that was built again, but only once the last run's Job is gone: a report
// that the reporter built before the run's rows reached it also counts
// fewer, and the Job is deleted five minutes after it succeeds.
func titlesPlaceCause(model time.Time, counts titlesReadings, last []titlesPlaced, listed, reading bool) string {
	if model.IsZero() {
		return ""
	}
	placedWith := false
	readings := 0
	var newest time.Time
	var latest titlesPlaced
	for _, run := range last {
		placedWith = placedWith || run.Model.Equal(model)
		readings = max(readings, run.Readings)
		if run.NewestReading.After(newest) {
			newest = run.NewestReading
		}
		if run.At.After(latest.At) {
			latest = run
		}
	}
	landed := counts.Readings - readings
	switch {
	case !placedWith:
		return fmt.Sprintf("no placing has run with the model fitted at %s", model.UTC().Format(time.RFC3339))
	case !listed && counts.Placements < latest.Placements:
		return fmt.Sprintf("the catalog holds %s, and the last placing wrote %d",
			counted(counts.Placements, "placement"), latest.Placements)
	case landed >= titlesPlaceReadings:
		return fmt.Sprintf("%s have landed since the last placing", counted(landed, "reading"))
	case !reading && (landed > 0 || counts.NewestReading.After(newest)):
		return "a reading has landed since the last placing, and no reading worker runs"
	}
	return ""
}
