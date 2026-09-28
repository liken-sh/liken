package main

// tally.go is how a Job's counts reach Prometheus. A Job container exits when
// its work is done, so Prometheus cannot scrape it. The container writes its
// counts into the tallies table of the catalog instead, the reporter publishes
// them on the bus in its report, and the operator turns them into counters.

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tallyRetention is how long a worker's rows stay in the table before that
// worker's next run sweeps them. The sweep is what bounds the table.
const tallyRetention = 48 * time.Hour

// tallyFlushInterval is the time between two flushes of a running recorder.
// A container that runs for an hour is visible while it runs, and not only at
// its end.
const tallyFlushInterval = 30 * time.Second

// The metric names the Jobs record. A Job writes a name the operator's
// tallyMetrics table does not hold, and the operator ignores that row.
const (
	tallyAttempts               = "attempts"
	tallyProviderRequests       = "provider_requests"
	tallyProviderRequestSeconds = "provider_request_seconds"
)

// tallyCell is one metric and one label set. A value accumulates under a
// cell, and the row's last two key columns hold the cell.
type tallyCell struct {
	metric string
	labels string
}

// tallies is one Job container's counts: the row key every flush writes
// under, and the totals raised since the container started.
type tallies struct {
	catalog   *Catalog
	library   string
	worker    string
	job       string
	container string
	started   time.Time

	// The trailer fact and the art fact raise counts from several goroutines,
	// so one mutex covers the totals.
	mutex sync.Mutex
	held  map[tallyCell]float64
}

// newTallies builds a recorder for one run of one worker. Every method
// returns at once on a nil recorder, so a caller with no catalog needs no
// branch.
func newTallies(catalog *Catalog, library, worker, job, container string, started time.Time) *tallies {
	return &tallies{
		catalog:   catalog,
		library:   library,
		worker:    worker,
		job:       job,
		container: container,
		started:   started,
		held:      map[tallyCell]float64{},
	}
}

// add raises one metric's total under its labels. The labels arrive as name
// and value pairs, and tallyLabels serializes them in one canonical order.
func (t *tallies) add(metric string, value float64, labels ...string) {
	if t == nil {
		return
	}
	cell := tallyCell{metric: metric, labels: tallyLabels(labels)}
	t.mutex.Lock()
	t.held[cell] += value
	t.mutex.Unlock()
}

// flush writes the current total of every cell in one transaction. A flush
// that fails leaves the totals in memory, so the next flush writes them.
func (t *tallies) flush(ctx context.Context) error {
	if t == nil {
		return nil
	}
	t.mutex.Lock()
	cells := make([]tallyCell, 0, len(t.held))
	for cell := range t.held {
		cells = append(cells, cell)
	}
	// One order per flush, so a batch reads the same way every time.
	slices.SortFunc(cells, func(a, b tallyCell) int {
		if order := strings.Compare(a.metric, b.metric); order != 0 {
			return order
		}
		return strings.Compare(a.labels, b.labels)
	})
	statements := make([]statement, len(cells))
	for at, cell := range cells {
		statements[at] = statement{
			sql: `INSERT INTO tallies (library, worker, job, container, started, metric, labels, value) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, worker, job, started, container, metric, labels) DO UPDATE SET ` +
				`value = excluded.value`,
			params: []any{t.library, t.worker, t.job, t.container, t.started.Unix(),
				cell.metric, cell.labels, t.held[cell]},
		}
	}
	t.mutex.Unlock()
	if len(statements) == 0 {
		return nil
	}
	_, err := t.catalog.apply(ctx, statements)
	return err
}

// run flushes on the interval until ctx ends, then once more on a context of
// its own, because the container's own context is already done by then.
func (t *tallies) run(ctx context.Context) {
	if t == nil {
		return
	}
	ticker := time.NewTicker(tallyFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			ending, done := context.WithTimeout(context.Background(), catalogWriteTimeout)
			defer done()
			_ = t.flush(ending)
			return
		case <-ticker.C:
			_ = t.flush(ctx)
		}
	}
}

// recording runs the recorder beside the work and returns the call that ends
// it, so a container flushes once before it exits.
func (t *tallies) recording(ctx context.Context) func() {
	if t == nil {
		return func() {}
	}
	running, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t.run(running)
	}()
	return func() {
		stop()
		<-done
	}
}

// tallyLabels writes the labels as one string: the pairs sorted by name, as
// name=value joined by commas, and empty for a metric with none. A name with
// no value gets an empty value.
func tallyLabels(pairs []string) string {
	names := make([]string, 0, (len(pairs)+1)/2)
	values := map[string]string{}
	for at := 0; at < len(pairs); at += 2 {
		name := pairs[at]
		value := ""
		if at+1 < len(pairs) {
			value = pairs[at+1]
		}
		if _, held := values[name]; !held {
			names = append(names, name)
		}
		values[name] = value
	}
	slices.Sort(names)
	written := make([]string, len(names))
	for at, name := range names {
		written[at] = name + "=" + values[name]
	}
	return strings.Join(written, ",")
}

// sweepTallies deletes one worker's rows from before a cutoff. Every Job runs
// it where it writes its run's started mark, so a row outlives its run by the
// retention and no longer.
func (c *Catalog) sweepTallies(ctx context.Context, library, worker string, before time.Time) error {
	_, err := c.apply(ctx, []statement{{
		sql:    `DELETE FROM tallies WHERE library = ? AND worker = ? AND started < ?`,
		params: []any{library, worker, before.Unix()},
	}})
	return err
}

// tallyKey is the six key columns beside the library. The whole-library sweep
// reads them as one joined string and deletes by them.
type tallyKey struct {
	Worker    string
	Job       string
	Started   int64
	Container string
	Metric    string
	Labels    string
}

// librarySweepTallySQL selects one bounded batch of one library's rows, with
// the six key columns joined by the separator every composite-key sweep uses.
func librarySweepTallySQL() string {
	return `SELECT worker || char(31) || job || char(31) || started || char(31) || ` +
		`container || char(31) || metric || char(31) || labels FROM tallies ` +
		`WHERE library = ? LIMIT ?`
}

// tallyKeys reads each joined key back into the columns the delete names.
func tallyKeys(keys []string) []tallyKey {
	out := make([]tallyKey, len(keys))
	for i, key := range keys {
		parts := strings.SplitN(key, linkKeySeparator, 6)
		for len(parts) < 6 {
			parts = append(parts, "")
		}
		started, _ := strconv.ParseInt(parts[2], 10, 64)
		out[i] = tallyKey{Worker: parts[0], Job: parts[1], Started: started,
			Container: parts[3], Metric: parts[4], Labels: parts[5]}
	}
	return out
}

// DeleteTallies deletes the named rows. This is how a departed library's
// tallies leave with the rest of its catalog.
func (c *Catalog) DeleteTallies(ctx context.Context, library string, keys []tallyKey) (int, error) {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql: `DELETE FROM tallies WHERE library = ? AND worker = ? AND job = ? ` +
				`AND started = ? AND container = ? AND metric = ? AND labels = ?`,
			params: []any{library, key.Worker, key.Job, key.Started, key.Container,
				key.Metric, key.Labels},
		}
	}
	return c.apply(ctx, statements)
}
