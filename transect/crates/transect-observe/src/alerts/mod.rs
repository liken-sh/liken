//! Alerts: the alerts that Alertmanager reports as firing.  transect
//! reads them from Alertmanager's API instead of from Prometheus's
//! `ALERTS` series, so the map leaves out the alerts that a silence or
//! an inhibition rule hides.  During a rollout, or while a machine is
//! lost, `ALERTS` lists exactly the alerts that an inhibition exists to
//! keep quiet.
//!
//! transect attaches each alert, by its labels, to the namespace,
//! object, or machine that it is about.  When its labels name nothing
//! in the cluster, the alert attaches to the whole cluster.  The
//! `alerts` section of the `MetricsSource` sets which labels name which
//! things, and what each severity means.  A namespace's alerts are
//! reasons in its health, so they set the color of its parcel and show
//! on its cards.  A machine's alerts mark its strip.  "Now" lists every
//! alert.

mod client;
pub mod markup;
mod now;
mod parse;
mod poll;
pub mod source;
mod target;

use std::collections::BTreeMap;

use k8s_openapi::jiff::Timestamp;

use crate::health::Level;
pub use now::{Alerted, Firing, firing, on_machines, reasons};
#[cfg(any(test, debug_assertions))]
pub use parse::alerts as parse_answer;
#[cfg(any(test, feature = "fixtures"))]
pub use poll::Failure;
pub use poll::{Reading, Sourcing, poll};
pub use source::Source;
pub use target::{Known, Target, target};

/// One firing alert, with its text cut to the length that a card shows.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Alert {
    /// Its `alertname` label.
    pub name: String,
    /// Its severity label as written, for display.
    pub severity: Option<String>,
    pub level: Level,
    /// Every label.  `target` reads the labels to find what the alert
    /// is about, and the link to Alertmanager uses them as its filter.
    pub labels: BTreeMap<String, String>,
    pub summary: Option<String>,
    pub description: Option<String>,
    pub starts_at: Option<Timestamp>,
    /// Alertmanager's page for the alert's group, when transect has the
    /// address of Alertmanager's web interface.
    pub link: Option<String>,
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::*;

    /// An alert named `name` at `severity`, with `labels`, that started
    /// at noon on the first of January 2026.
    pub fn alert(name: &str, severity: &str, labels: &[(&str, &str)]) -> Alert {
        let mut all: BTreeMap<String, String> = labels
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect();
        all.insert("alertname".into(), name.into());
        all.insert("severity".into(), severity.into());
        Alert {
            name: name.into(),
            severity: Some(severity.into()),
            level: source::fixtures::source()
                .severity
                .level(Some(severity))
                .expect("a fixture alert is not ignored"),
            labels: all,
            summary: Some(format!("{name} summary")),
            description: None,
            starts_at: "2026-01-01T12:00:00Z".parse().ok(),
            link: None,
        }
    }
}
