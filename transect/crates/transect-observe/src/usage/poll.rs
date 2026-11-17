//! Reads usage from Prometheus on an interval and stores the latest
//! reading in a `watch` channel that each render reads.
//!
//! Because the reading is in a `watch` channel, a render borrows the
//! newest one without a lock of its own, and a new reading that matches
//! the old one wakes no receiver.  When a reading differs, the poller
//! marks the cluster as changed, and the event stream renders the page
//! again and pushes it, as it does for any watch event.

use std::sync::Arc;
use std::time::Duration;

use tokio::sync::watch;
use tokio::time::MissedTickBehavior;

use super::{Answers, Queries, Usage};
use crate::metrics::Reaching;
use crate::prometheus::{Prometheus, QueryError, Sample};

#[derive(Clone, Debug, PartialEq)]
pub enum Reading {
    /// Neither `--prometheus` nor a `MetricsSource` gives a
    /// Prometheus.
    Off,
    /// No `MetricsSource` declares a usage query.
    Undeclared,
    /// The first query has not returned yet.
    Waiting,
    Failed(Failure),
    /// Shared through an `Arc`, because a reading of every container is
    /// far larger than the other variants, and each render copies the
    /// reading out of the channel instead of holding the channel's lock
    /// while it draws.
    Read(Arc<Usage>),
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Failure {
    Unreachable,
    QueryFailed,
}

impl Reading {
    /// The usage to draw, if there is any.
    pub fn usage(&self) -> Option<&Usage> {
        match self {
            Self::Read(usage) => Some(usage),
            _ => None,
        }
    }

    /// The line that a page shows in place of used bars, with the text
    /// of `note`.  When no usage is declared because the
    /// `MetricsSource` did not load, the line reports that it did not
    /// load.
    pub fn off_line(
        &self,
        loaded: &transect_resources::resources::Directory,
    ) -> Option<transect_resources::resources::OffLine> {
        let note = self.note()?;
        Some(match self {
            Self::Off | Self::Undeclared => loaded.off("MetricsSource", "no usage: ", note),
            _ => transect_resources::resources::OffLine::said(note),
        })
    }

    /// One short line for the machines band when it draws no used bars.
    /// While the first query is in progress, the band shows no line, so
    /// transect does not show a warning at start that clears a moment
    /// later.
    pub fn note(&self) -> Option<&'static str> {
        match self {
            Self::Off => Some("no usage: no Prometheus configured"),
            Self::Undeclared => Some(
                "no usage: a MetricsSource's machineCpu and machineMemory queries would show it",
            ),
            Self::Waiting => None,
            Self::Failed(Failure::Unreachable) => Some("no usage: Prometheus unreachable"),
            Self::Failed(Failure::QueryFailed) => Some("no usage: Prometheus query failed"),
            Self::Read(usage) if usage.is_empty() => {
                Some("no usage: the MetricsSource's machine queries found no series")
            }
            Self::Read(_) => None,
        }
    }
}

/// Sends the usage queries of the `MetricsSource` to the Prometheus
/// that `reaching` selects, at once, then every `interval` and whenever
/// the resources change.  It marks `changed` after each reading that
/// differs from the one before.
pub fn poll(
    reaching: Reaching,
    interval: Duration,
    changed: watch::Sender<()>,
) -> watch::Receiver<Reading> {
    let (readings, latest) = watch::channel(Reading::Waiting);
    tokio::spawn(async move {
        let mut ticks = tokio::time::interval(interval);
        ticks.set_missed_tick_behavior(MissedTickBehavior::Delay);
        let mut resources = reaching.resources();
        loop {
            // When the resources channel's sender is dropped, `changed`
            // returns an error and its arm does not match, so only the
            // tick wakes the loop.
            tokio::select! {
                _ = ticks.tick() => {}
                Ok(()) = resources.changed() => {}
            }
            resources.mark_unchanged();
            let queries = reaching
                .source()
                .map(|source| source.usage)
                .unwrap_or_default();
            let (reading, error) = match reaching.prometheus() {
                None => (Reading::Off, None),
                Some(_) if queries.is_empty() => (Reading::Undeclared, None),
                Some(prometheus) => {
                    let result = read(&prometheus, &queries).await;
                    let error = result.as_ref().err().map(ToString::to_string);
                    (Reading::from(result), error)
                }
            };
            if is_transition(&readings.borrow(), &reading) {
                log(&reading, error.as_deref());
            }
            if readings.send_if_modified(|current| replace_if_differs(current, reading)) {
                changed.send_replace(());
            }
        }
    });
    latest
}

fn replace_if_differs(current: &mut Reading, next: Reading) -> bool {
    let differs = *current != next;
    if differs {
        *current = next;
    }
    differs
}

/// Sends every query that `queries` declares at the same time, so all
/// the views drawn from one reading show the same figures.
async fn read(prometheus: &Prometheus, queries: &Queries) -> Result<Usage, QueryError> {
    let container_cpu = queries
        .container_cpu
        .as_ref()
        .map(|query| query.query.as_str());
    let container_memory = queries
        .container_memory
        .as_ref()
        .map(|query| query.query.as_str());
    let (machine_cpu, machine_memory, container_cpu, container_memory) = tokio::join!(
        ask(prometheus, queries.machine_cpu.as_deref()),
        ask(prometheus, queries.machine_memory.as_deref()),
        ask(prometheus, container_cpu),
        ask(prometheus, container_memory),
    );
    let answers = Answers {
        machine_cpu: machine_cpu?,
        machine_memory: machine_memory?,
        container_cpu: container_cpu?,
        container_memory: container_memory?,
    };
    Ok(Usage::answered(queries, answers))
}

/// The samples that `query` returns, or no samples for a query that is
/// left out.
async fn ask(prometheus: &Prometheus, query: Option<&str>) -> Result<Vec<Sample>, QueryError> {
    match query {
        Some(query) => prometheus.query(query).await,
        None => Ok(Vec::new()),
    }
}

impl From<Result<Usage, QueryError>> for Reading {
    fn from(result: Result<Usage, QueryError>) -> Self {
        match result {
            Ok(usage) => Self::Read(Arc::new(usage)),
            Err(QueryError::Unreachable(_)) => Self::Failed(Failure::Unreachable),
            Err(QueryError::Failed(_)) => Self::Failed(Failure::QueryFailed),
        }
    }
}

/// Whether `next` is the first reading or changes its `note`, as when
/// a failure starts or ends, or a read finds no series.  A failure is
/// logged when it starts and when it ends, not every
/// interval, because a Prometheus that is down for an hour, polled every
/// 30 s, would otherwise write 120 identical warnings.
fn is_transition(previous: &Reading, next: &Reading) -> bool {
    *previous == Reading::Waiting || previous.note() != next.note()
}

fn log(reading: &Reading, error: Option<&str>) {
    match reading.note() {
        None => tracing::info!("reading usage from Prometheus"),
        Some(note) => tracing::warn!(error = error.unwrap_or("none"), "{note}"),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn read_one() -> Reading {
        let sample = Sample {
            labels: [("node".into(), "alpha".into())].into(),
            value: 1.0,
        };
        let labels = crate::usage::fixtures::node_labels();
        Reading::Read(Arc::new(Usage::from_samples(vec![sample], vec![], &labels)))
    }

    const UNREACHABLE: Reading = Reading::Failed(Failure::Unreachable);
    const QUERY_FAILED: Reading = Reading::Failed(Failure::QueryFailed);

    #[rstest]
    #[case::first_success(Reading::Waiting, read_one(), true)]
    #[case::first_failure(Reading::Waiting, UNREACHABLE, true)]
    #[case::still_reading(read_one(), read_one(), false)]
    #[case::failure_starts(read_one(), UNREACHABLE, true)]
    #[case::failure_continues(UNREACHABLE, UNREACHABLE, false)]
    #[case::failure_changes(UNREACHABLE, QUERY_FAILED, true)]
    #[case::failure_ends(UNREACHABLE, read_one(), true)]
    #[case::metrics_vanish(read_one(), Reading::Read(Arc::default()), true)]
    fn logs_once_per_transition(
        #[case] previous: Reading,
        #[case] next: Reading,
        #[case] expected: bool,
    ) {
        assert_eq!(is_transition(&previous, &next), expected);
    }

    #[rstest]
    #[case::off(Reading::Off, Some("no usage: no Prometheus configured"))]
    #[case::waiting(Reading::Waiting, None)]
    #[case::unreachable(UNREACHABLE, Some("no usage: Prometheus unreachable"))]
    #[case::query_failed(QUERY_FAILED, Some("no usage: Prometheus query failed"))]
    #[case::undeclared(
        Reading::Undeclared,
        Some("no usage: a MetricsSource's machineCpu and machineMemory queries would show it")
    )]
    #[case::no_metrics(
        Reading::Read(Arc::default()),
        Some("no usage: the MetricsSource's machine queries found no series")
    )]
    #[case::read(read_one(), None)]
    fn notes(#[case] reading: Reading, #[case] expected: Option<&str>) {
        assert_eq!(reading.note(), expected);
    }
}
