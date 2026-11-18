//! The traffic of each backend of an `Ingress`: for each `Service` it
//! routes to, the request rate, the 95th-percentile latency, and the
//! rate of 5xx responses. transect reads the figures from the ingress
//! controller's metrics, with the queries that the controller's
//! `IngressController` resource declares.
//!
//! Each query returns one series per backend, identified by the label
//! that the resource names, so three queries cover every `Ingress` the
//! controller serves. The page draws the figures in gray and compares
//! them to no threshold, because alerts on traffic are left to the
//! cluster's Alertmanager. When no `IngressController` declares
//! `entry`, the `Ingress` shows one gray line that says what would
//! supply the figures.

mod line;

use std::collections::HashMap;

use k8s_openapi::api::networking::v1::{Ingress, IngressBackend};

use super::EntryQueries;
use transect_observe::prometheus::Sample;

/// One backend's figures.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Figures {
    /// Requests per second.
    pub requests: f64,
    /// The 95th-percentile latency in seconds, when the window
    /// contained a request to measure.
    pub p95: Option<f64>,
    /// Responses with a 5xx status per second.
    pub errors: f64,
}

/// The results of one controller's queries, keyed by the backend name
/// in each series.
#[derive(Clone, Debug, PartialEq)]
pub struct Measured {
    /// The `IngressController` the queries came from.
    pub controller: String,
    pub queries: EntryQueries,
    pub backends: HashMap<String, Figures>,
}

/// The results of the most recent run of every controller's queries,
/// and whether any `IngressController` declares `entry`. transect runs
/// the queries only when one does.
#[derive(Clone, Copy, Debug)]
pub struct TrafficResults<'a> {
    pub measured: &'a [Measured],
    pub declared: bool,
    /// The name of an `IngressController` that failed to load, set
    /// when no loaded one declares `entry`.
    pub unloaded: Option<&'a str>,
}

/// What an `Ingress`'s card shows for its traffic.
#[derive(Clone, Debug, PartialEq)]
pub enum Traffic {
    /// No `IngressController` declares `entry`, so transect reads no
    /// traffic.
    Undeclared,
    /// No loaded `IngressController` declares `entry`, and the one named
    /// here failed to load.
    Unloaded(String),
    /// An `IngressController` declares `entry`, but no controller's
    /// queries have returned yet.
    Unread,
    /// No controller's results contain any of the `Ingress`'s backends.
    /// Another controller serves it, or no request has reached it since
    /// its controller started.
    Unnamed,
    /// Each of its backends, with the figures from the first controller
    /// whose results contain any of them.
    Read {
        backends: Vec<Backend>,
        window: String,
        controller: String,
    },
}

/// One `Service` an `Ingress` routes to, by name and port as written,
/// with its figures when the controller's results contain it.
#[derive(Clone, Debug, PartialEq)]
pub struct Backend {
    pub service: String,
    pub port: String,
    pub figures: Option<Figures>,
}

/// The figures of each backend from the results of the three queries,
/// keyed by the value of `label`. It skips a series that lacks `label`
/// or whose value is not finite, so a latency quantile over no requests
/// (NaN) leaves `p95` as `None`. When two series have the same backend,
/// it adds the rates and keeps the larger latency, because quantiles
/// cannot be added.
pub fn measure(
    label: &str,
    requests: &[Sample],
    latency: &[Sample],
    errors: &[Sample],
) -> HashMap<String, Figures> {
    let mut backends: HashMap<String, Figures> = HashMap::new();
    let named = |sample: &Sample| sample.labels.get(label).cloned();
    for sample in requests.iter().filter(|s| s.value.is_finite()) {
        let Some(name) = named(sample) else { continue };
        backends.entry(name).or_default().requests += sample.value;
    }
    for sample in errors.iter().filter(|s| s.value.is_finite()) {
        let Some(name) = named(sample) else { continue };
        backends.entry(name).or_default().errors += sample.value;
    }
    for sample in latency.iter().filter(|s| s.value.is_finite()) {
        let Some(name) = named(sample) else { continue };
        let figures = backends.entry(name).or_default();
        figures.p95 = Some(
            figures
                .p95
                .map_or(sample.value, |p95| p95.max(sample.value)),
        );
    }
    backends
}

/// Each `Service` and port an `Ingress` routes to, without duplicates.
/// The port is a number or a name, as the `Ingress` writes it.
pub fn backends(ingress: &Ingress) -> Vec<(String, String)> {
    let Some(spec) = ingress.spec.as_ref() else {
        return Vec::new();
    };
    let rules = spec.rules.iter().flatten();
    let paths = rules.flat_map(|rule| rule.http.iter().flat_map(|http| &http.paths));
    let backends = paths
        .map(|path| &path.backend)
        .chain(spec.default_backend.as_ref());
    let mut found: Vec<(String, String)> = Vec::new();
    for backend in backends.filter_map(service_port) {
        if !found.contains(&backend) {
            found.push(backend);
        }
    }
    found
}

fn service_port(backend: &IngressBackend) -> Option<(String, String)> {
    let service = backend.service.as_ref()?;
    let port = service.port.as_ref()?;
    let port = match (port.number, &port.name) {
        (Some(number), _) => number.to_string(),
        (None, Some(name)) => name.clone(),
        (None, None) => return None,
    };
    Some((service.name.clone(), port))
}

/// The traffic of the `Ingress` in `namespace` with `backends`, from the
/// first controller in `results` whose results contain any of them.
pub fn of(namespace: &str, backends: &[(String, String)], results: TrafficResults) -> Traffic {
    if !results.declared {
        return match results.unloaded {
            Some(name) => Traffic::Unloaded(name.to_string()),
            None => Traffic::Undeclared,
        };
    }
    if results.measured.is_empty() {
        return Traffic::Unread;
    }
    let read = results.measured.iter().find_map(|one| {
        let each: Vec<Backend> = backends
            .iter()
            .map(|(service, port)| Backend {
                figures: one
                    .backends
                    .get(&one.queries.backend(namespace, service, port))
                    .copied(),
                service: service.clone(),
                port: port.clone(),
            })
            .collect();
        each.iter()
            .any(|backend| backend.figures.is_some())
            .then(|| Traffic::Read {
                backends: each,
                window: one.queries.window.clone(),
                controller: one.controller.clone(),
            })
    });
    read.unwrap_or(Traffic::Unnamed)
}

#[cfg(test)]
mod tests;
