//! The fixture's alerts and Prometheus series, read from `signals.yaml`
//! through the same parsers and lookups that a live poll uses.

use std::collections::HashMap;
use std::sync::Arc;

use serde::Deserialize;

use crate::web::loaded::Loaded;
use transect_observe::prometheus::Sample;
use transect_observe::{alerts, metrics, usage};

#[derive(Deserialize)]
struct Signals {
    alerts: serde_json::Value,
    prometheus: Vec<QueryResponse>,
    #[cfg(test)]
    logs: Vec<Logged>,
}

/// The rows that the log store returns for one container's tail.
#[cfg(test)]
#[derive(Deserialize)]
struct Logged {
    namespace: String,
    pod: String,
    container: String,
    rows: Vec<transect_observe::logs::Row>,
}

#[derive(Deserialize)]
struct QueryResponse {
    query: String,
    samples: Vec<Series>,
}

#[derive(Deserialize)]
struct Series {
    labels: HashMap<String, String>,
    value: f64,
}

fn signals() -> Signals {
    serde_yaml_ng::from_str(include_str!("signals.yaml")).expect("parsing signals.yaml")
}

/// The series that Prometheus returns for `query`, or none for a query
/// that the fixture has no series for.
fn run_query(query: &str) -> Vec<Sample> {
    let responses = signals().prometheus.into_iter();
    let found = responses
        .into_iter()
        .find(|response| response.query == query);
    let samples = found.map(|response| response.samples).unwrap_or_default();
    let sample = |series: Series| Sample {
        labels: series.labels,
        value: series.value,
    };
    samples.into_iter().map(sample).collect()
}

/// The alerts that Alertmanager reports as firing, parsed as the
/// fixture's `MetricsSource` declares.
pub fn alerts(resources: &Loaded) -> alerts::Reading {
    let source = resources.metrics_source.as_ref();
    let Some(source) = source.and_then(|source| source.alerts.as_ref()) else {
        return alerts::Reading::Undeclared;
    };
    let body = signals().alerts.to_string();
    let firing = alerts::parse_alerts(&body, source).expect("parsing the fixture's alerts");
    alerts::Reading::Read(Arc::new(firing))
}

/// The usage of every machine, pod, and container, from the responses to
/// the usage queries of the fixture's `MetricsSource`.
pub fn usage(resources: &Loaded) -> usage::Reading {
    let source = resources.metrics_source.as_ref();
    let Some(queries) = source.map(|source| &source.usage) else {
        return usage::Reading::Undeclared;
    };
    usage::Reading::Read(Arc::new(usage::Usage::from_query_fn(queries, run_query)))
}

/// The bytes used on each claim's volume, from the response to the
/// `volumeUsed` query of the fixture's `MetricsSource`.
pub fn volumes_used(resources: &Loaded) -> metrics::Reading {
    let source = resources.metrics_source.as_ref();
    let Some(query) = source.and_then(|source| source.volume_used.as_ref()) else {
        return metrics::Reading::Undeclared;
    };
    let used = metrics::poll::by_claim(&run_query(&query.query), &query.labels);
    metrics::Reading::Read(Arc::new(used))
}

/// The tail of the pod `namespace/pod`, as the log store declared in
/// `resources` returns it. Returns none when the fixture has no lines
/// for the pod or the cluster declares no log source.
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
