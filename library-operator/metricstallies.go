package main

// metricstallies.go holds the counters a Job's own counts feed. A Job
// container exits when its work is done, so nothing there can be scraped. It
// writes its counts into the tallies table instead, the reporter publishes
// them, and this file turns each row into a counter on the operator's own
// registry.

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// tallyMetric is one tally metric as Prometheus holds it: the counter's name,
// the labels the tally's own labels fill, and the counter's help text.
type tallyMetric struct {
	name   string
	labels []string
	help   string
}

// tallyMetrics is the whole table. A Job writes a tally metric with no row
// here, and the operator ignores it, so a new count reaches the catalog before
// this operator has a counter for it.
var tallyMetrics = map[string]tallyMetric{
	tallyAttempts: {
		name:   "library_attempts_total",
		labels: []string{"fact", "result"},
		help:   "Attempts one fact made, by the result each attempt left behind.",
	},
	tallyProviderRequests: {
		name:   "library_provider_requests_total",
		labels: []string{"provider", "status"},
		help:   "Requests the enricher made to a provider, by the status class of the answer.",
	},
	tallyProviderRequestSeconds: {
		name:   "library_provider_request_seconds_total",
		labels: []string{"provider"},
		help:   "Seconds the enricher spent waiting on a provider.",
	},
	tallyTrailerFetches: {
		name:   "library_trailer_fetches_total",
		labels: []string{"site", "result"},
		help:   "Trailer downloads a Job made, by the site it read and the result of each one.",
	},
	tallyTrailerFetchBytes: {
		name:   "library_trailer_fetch_bytes_total",
		labels: []string{"site"},
		help:   "Bytes a Job wrote from a site's trailers.",
	},
}

// newTallyCounters builds one CounterVec per row of the table, each labeled
// by the library and then by the metric's own labels, in the table's order.
func newTallyCounters(factory promauto.Factory) map[string]*prometheus.CounterVec {
	counters := map[string]*prometheus.CounterVec{}
	for metric, one := range tallyMetrics {
		counters[metric] = factory.NewCounterVec(prometheus.CounterOpts{
			Name: one.name,
			Help: one.help,
		}, append([]string{"library"}, one.labels...))
	}
	return counters
}

// tallyStateKey names one tally row this operator has already read. It is
// what tells a grown count from a count read twice.
type tallyStateKey struct {
	library string
	worker  string
	job     string
	// The Unix second the run began. It is part of the run's identity because
	// a retried pod and a Job created again have the same name.
	started   int64
	container string
	metric    string
	labels    string
}

// observeTallies folds one library's tallies into the counters. A row the
// operator has not seen adds its whole value, because a Job that has exited
// counted from zero. A row that has grown adds the difference. A row that
// reads the same, or lower, adds nothing: a lower value is a Job that started
// over, and its next report holds the growth from there.
//
// The rows of a run the report no longer holds are forgotten, so a swept run
// leaves no state behind and a later run of the same job name starts from
// zero.
func (m *metrics) observeTallies(library string, held []libraryTally) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.forgetVanishedRuns(library, held)
	for _, tally := range held {
		one, known := tallyMetrics[tally.Metric]
		if !known {
			continue
		}
		values := make([]string, 0, len(one.labels)+1)
		values = append(values, library)
		for _, name := range one.labels {
			values = append(values, tally.Labels[name])
		}
		// The container is part of the key and never a label, because two
		// containers of one Job each count from zero and both belong to the
		// same series.
		key := tallyStateKey{
			library:   library,
			worker:    tally.Worker,
			job:       tally.Job,
			started:   tally.Started.Unix(),
			container: tally.Container,
			metric:    tally.Metric,
			labels:    strings.Join(values[1:], "\x1f"),
		}
		last, seen := m.lastTally[key]
		m.lastTally[key] = tally.Value
		if !seen {
			m.tallyCounters[tally.Metric].WithLabelValues(values...).Add(tally.Value)
			continue
		}
		if tally.Value > last {
			m.tallyCounters[tally.Metric].WithLabelValues(values...).Add(tally.Value - last)
		}
	}
}

// forgetVanishedRuns drops the state of every run of this library the report
// no longer holds, so the map holds what the catalog holds and no more.
func (m *metrics) forgetVanishedRuns(library string, held []libraryTally) {
	live := map[tallyStateKey]bool{}
	for _, tally := range held {
		live[tallyRunKey(library, tally.Worker, tally.Job, tally.Started.Unix())] = true
	}
	for key := range m.lastTally {
		if key.library != library {
			continue
		}
		if !live[tallyRunKey(key.library, key.worker, key.job, key.started)] {
			delete(m.lastTally, key)
		}
	}
}

// tallyRunKey names the run a row belongs to: one library, one worker, one
// job name, and the second that run began.
func tallyRunKey(library, worker, job string, started int64) tallyStateKey {
	return tallyStateKey{library: library, worker: worker, job: job, started: started}
}
