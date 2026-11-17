//! The fixture's signals, from `signals.yaml`: the alerts Alertmanager
//! answers and the series Prometheus answers each query with, read
//! through the same parsers and lookups a live poll uses.

use std::collections::HashMap;
use std::sync::Arc;

use serde::Deserialize;

use crate::web::loaded::Loaded;
use transect_observe::prometheus::Sample;
use transect_observe::{alerts, metrics, usage};

#[derive(Deserialize)]
struct Signals {
    alerts: serde_json::Value,
    prometheus: Vec<Answer>,
    #[cfg(test)]
    logs: Vec<Logged>,
}

/// The rows the log store answers one container's tail with.
#[cfg(test)]
#[derive(Deserialize)]
struct Logged {
    namespace: String,
    pod: String,
    container: String,
    rows: Vec<transect_observe::logs::Row>,
}

#[derive(Deserialize)]
struct Answer {
    query: String,
    samples: Vec<Series>,
}

#[derive(Deserialize)]
struct Series {
    labels: HashMap<String, String>,
    value: f64,
}

fn signals() -> Signals {
    serde_yaml::from_str(include_str!("signals.yaml")).expect("signals.yaml reads")
}

/// The series Prometheus answers `query` with, or none for a query the
/// fixture does not answer.
fn answer(query: &str) -> Vec<Sample> {
    let answers = signals().prometheus.into_iter();
    let found = answers.into_iter().find(|answer| answer.query == query);
    let samples = found.map(|answer| answer.samples).unwrap_or_default();
    let sample = |series: Series| Sample {
        labels: series.labels,
        value: series.value,
    };
    samples.into_iter().map(sample).collect()
}

/// The alerts Alertmanager reports as firing, read by the conventions of
/// the fixture's `MetricsSource`.
pub fn alerts(resources: &Loaded) -> alerts::Reading {
    let source = resources.metrics_source.as_ref();
    let Some(source) = source.and_then(|source| source.alerts.as_ref()) else {
        return alerts::Reading::Undeclared;
    };
    let body = signals().alerts.to_string();
    let firing = alerts::parse_answer(&body, source).expect("the fixture's alerts read");
    alerts::Reading::Read(Arc::new(firing))
}

/// What every machine, pod, and container uses, as the usage queries
/// of the fixture's `MetricsSource` are answered.
pub fn usage(resources: &Loaded) -> usage::Reading {
    let source = resources.metrics_source.as_ref();
    let Some(queries) = source.map(|source| &source.usage) else {
        return usage::Reading::Undeclared;
    };
    usage::Reading::Read(Arc::new(usage::Usage::from_answers(queries, answer)))
}

/// The bytes on each claim's volume, as the `volumeUsed` query of the
/// fixture's `MetricsSource` is answered.
pub fn volumes_used(resources: &Loaded) -> metrics::Reading {
    let source = resources.metrics_source.as_ref();
    let Some(query) = source.and_then(|source| source.volume_used.as_ref()) else {
        return metrics::Reading::Undeclared;
    };
    let used = metrics::poll::by_claim(&answer(&query.query), &query.labels);
    metrics::Reading::Read(Arc::new(used))
}

/// The tail of the pod `namespace/pod`, as the log store `resources`
/// declare answers it, or none for a pod the fixture holds no lines of
/// or a cluster that declares no log source.
#[cfg(test)]
pub fn tails(
    resources: &Loaded,
    namespace: &str,
    pod: &str,
) -> Option<Arc<transect_observe::logs::Tails>> {
    use transect_observe::logs::{Origin, Tail, Tails, from_rows};
    let source = resources.log_source.as_ref()?;
    let logged = signals().logs.into_iter();
    let containers: Vec<Tail> = logged
        .filter(|logged| logged.namespace == namespace && logged.pod == pod)
        .map(|logged| Tail {
            read: from_rows(&logged.rows, source),
            container: logged.container,
        })
        .collect();
    if containers.is_empty() {
        return None;
    }
    Some(Arc::new(Tails {
        read_at: super::NOW.parse().ok()?,
        from: Origin::Store(source.name.clone()),
        forbidden: false,
        containers,
        matching: Vec::new(),
    }))
}
