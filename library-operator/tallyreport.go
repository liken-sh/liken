package main

// tallyreport.go is the read side of the tallies table. The reporter puts a
// library's rows in its report, because the containers that wrote them have
// exited and the operator is the process Prometheus scrapes.

import (
	"context"
	"strings"
	"time"
)

// talliesPerWorker is how many runs of one worker the report holds, newest
// first. The table keeps the whole retention for a person reading it, and the
// report keeps the last two, so one retained message stays small and a run the
// operator has not read yet is still in it.
const talliesPerWorker = 2

// libraryTally is one tally row as the report holds it: the run and the
// container that wrote it, the metric, its labels, and the total that
// container has reached.
type libraryTally struct {
	Worker    string            `json:"worker"`
	Job       string            `json:"job"`
	Container string            `json:"container"`
	Started   time.Time         `json:"started"`
	Metric    string            `json:"metric"`
	Labels    map[string]string `json:"labels,omitempty"`
	Value     float64           `json:"value"`
}

// talliesQuery reads one library's newest runs. The window function ranks
// each worker's runs newest first, and the join selects the rows of those runs
// alone, so the read is bounded by the runs it returns and never by the
// retention the table holds.
//
// A run is one worker, one job, and one start, because a retried pod and a
// Job created again have the same name. Two runs that started in the same
// second rank by job, so every read returns one order.
const talliesQuery = `WITH runs AS (SELECT worker, job, started, ` +
	`row_number() OVER (PARTITION BY worker ORDER BY started DESC, job DESC) AS place ` +
	`FROM (SELECT DISTINCT worker, job, started FROM tallies WHERE library = ?)) ` +
	`SELECT t.worker, t.job, t.container, t.started, t.metric, t.labels, t.value ` +
	`FROM tallies t JOIN runs r ON t.worker = r.worker AND t.job = r.job ` +
	`AND t.started = r.started ` +
	`WHERE t.library = ? AND r.place <= ? ` +
	`ORDER BY t.worker, t.job, t.started, t.container, t.metric, t.labels`

// Tallies returns the tallies of one library's newest runs, in one order, so
// a report that counts the same rows holds the same bytes.
func (c *Catalog) Tallies(ctx context.Context, library string) ([]libraryTally, error) {
	var held []libraryTally
	err := c.stream(ctx, talliesQuery, []any{library, library, talliesPerWorker},
		func(cells []any) error {
			tally, ok := decodeTally(cells)
			if !ok {
				return nil
			}
			held = append(held, tally)
			return nil
		})
	return held, err
}

// decodeTally reads one row out of the cells the query streams, in the column
// order talliesQuery names.
func decodeTally(cells []any) (libraryTally, bool) {
	if len(cells) < 7 {
		return libraryTally{}, false
	}
	worker, ok := cells[0].(string)
	if !ok {
		return libraryTally{}, false
	}
	job, _ := cells[1].(string)
	container, _ := cells[2].(string)
	metric, _ := cells[4].(string)
	labels, _ := cells[5].(string)
	value, _ := cells[6].(float64)
	return libraryTally{
		Worker:    worker,
		Job:       job,
		Container: container,
		Started:   runTime(cellNumber(cells[3])),
		Metric:    metric,
		Labels:    tallyLabelMap(labels),
		Value:     value,
	}, true
}

// tallyLabelMap reads the labels column back into the map the report holds.
// A metric with no labels gets a nil map.
func tallyLabelMap(labels string) map[string]string {
	if labels == "" {
		return nil
	}
	held := map[string]string{}
	for _, pair := range strings.Split(labels, ",") {
		name, value, found := strings.Cut(pair, "=")
		if !found {
			continue
		}
		held[name] = value
	}
	return held
}
