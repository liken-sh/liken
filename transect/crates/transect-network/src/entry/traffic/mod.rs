//! An `Ingress`'s traffic for each backend: each `Service` it routes
//! to, with that backend's request rate, 95th-percentile latency, and
//! share of 5xx responses.  transect reads the figures from the
//! controller's metrics with the queries that the controller's
//! `IngressController` declares.
//!
//! Each query returns one series per backend, named in the label the
//! resource gives, so one round of three queries covers every
//! `Ingress` the controller serves.  The page draws the figures in
//! gray: transect compares them to no threshold and raises no alert
//! from them, because Alertmanager already does.  When nothing reads
//! the figures, the `Ingress` shows one gray line that names what
//! would read them.

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

/// The results of one controller's queries, by the backend names in
/// them.
#[derive(Clone, Debug, PartialEq)]
pub struct Measured {
    /// The `IngressController` the queries came from.
    pub controller: String,
    pub queries: EntryQueries,
    pub backends: HashMap<String, Figures>,
}

/// The results of every controller's queries in the last round, and
/// whether any `IngressController` declares `entry`, which is what
/// makes transect read them.
#[derive(Clone, Copy, Debug)]
pub struct Answers<'a> {
    pub measured: &'a [Measured],
    pub declared: bool,
    /// The name of an `IngressController` that exists but did not
    /// load, when none declares `entry`.
    pub unloaded: Option<&'a str>,
}

/// One `Ingress`'s traffic, as its boxes show it.
#[derive(Clone, Debug, PartialEq)]
pub enum Traffic {
    /// No `IngressController` declares `entry`, so nothing reads it.
    Undeclared,
    /// No `IngressController` that loaded declares `entry`, and the one
    /// named here did not load.
    Unloaded(String),
    /// Knowledge declares it, and no controller's queries have answered.
    Unread,
    /// The controllers' answers name none of its backends: another
    /// controller serves it, or no request reached it since its
    /// controller started.
    Unnamed,
    /// Each of its backends, from the first controller whose answers
    /// name any of them.
    Read {
        backends: Vec<Backend>,
        window: String,
        controller: String,
    },
}

/// One `Service` an `Ingress` routes to, by name and port as written,
/// and its figures when its controller's answers name it.
#[derive(Clone, Debug, PartialEq)]
pub struct Backend {
    pub service: String,
    pub port: String,
    pub figures: Option<Figures>,
}

/// The backends' figures from the three answers, by the value of
/// `label`.  The function omits a series with no such label, or with a
/// value that is not a number.  A latency that is not a number, as a
/// quantile of no requests is, gives no latency.  For two series of
/// one backend, the function adds their rates and keeps the slower
/// latency, because quantiles do not add.
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

/// Each backend an `Ingress` routes to, by `Service` and port as
/// written: a port's number, or its name.
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
/// first controller in `answers` whose answers name any of them.
pub fn of(namespace: &str, backends: &[(String, String)], answers: Answers) -> Traffic {
    if !answers.declared {
        return match answers.unloaded {
            Some(name) => Traffic::Unloaded(name.to_string()),
            None => Traffic::Undeclared,
        };
    }
    if answers.measured.is_empty() {
        return Traffic::Unread;
    }
    let read = answers.measured.iter().find_map(|one| {
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
