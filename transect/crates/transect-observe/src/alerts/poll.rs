//! Reads the firing alerts from Alertmanager on an interval and stores
//! the latest reading in a watch channel that each render reads, in the
//! same way as the usage poller.  A reading that matches the one before
//! wakes no receiver, and a reading that differs marks the cluster as
//! changed.  The `alerts` section of the `MetricsSource` names the
//! Alertmanager and the conventions for reading its alerts.  The poller
//! reads that section again on each round and whenever the resources
//! change.

use std::sync::Arc;
use std::time::Duration;

use tokio::sync::watch;
use tokio::time::MissedTickBehavior;

use super::Alert;
use super::client::{Alertmanager, FetchError};
use crate::metrics::MetricsSource;
use transect_graph::upstream;
use transect_resources::resources::Field;

#[derive(Clone, Debug, Default, PartialEq)]
pub enum Reading {
    /// The `alerts` section names no Alertmanager, and neither does
    /// `--alertmanager`.
    #[default]
    Off,
    /// No `MetricsSource` declares an `alerts` section.
    Undeclared,
    /// The first request has not returned yet.
    Waiting,
    Failed(Failure),
    Read(Arc<Vec<Alert>>),
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Failure {
    Unreachable,
    Unreadable,
}

impl Reading {
    /// The alerts the latest request returned, or none when the latest
    /// request failed or no request has returned yet.
    pub fn alerts(&self) -> &[Alert] {
        match self {
            Self::Read(alerts) => alerts,
            _ => &[],
        }
    }

    /// The line that "Now" shows in place of alerts, with the text of
    /// `note`.  When no Alertmanager is declared because the
    /// `MetricsSource` did not load, the line reports that it did not
    /// load.
    pub fn off_line(
        &self,
        loaded: &transect_resources::resources::Directory,
    ) -> Option<transect_resources::resources::OffLine> {
        let note = self.note()?;
        Some(match self {
            Self::Off | Self::Undeclared => loaded.off("MetricsSource", "", note),
            _ => transect_resources::resources::OffLine::said(note),
        })
    }

    /// One short line under "Now" when it lists no alerts.  While the
    /// first request is in progress, this function returns `None`, so
    /// transect does not show a warning at start that clears a moment
    /// later.
    pub fn note(&self) -> Option<&'static str> {
        match self {
            Self::Off => Some(
                "No Alertmanager to read: set --alertmanager, or a url in the MetricsSource's alerts section",
            ),
            Self::Undeclared => {
                Some("A MetricsSource with an alerts section would list the firing alerts")
            }
            Self::Waiting => None,
            Self::Failed(Failure::Unreachable) => Some("Alertmanager unreachable"),
            Self::Failed(Failure::Unreadable) => Some("Alertmanager's answer was unreadable"),
            Self::Read(alerts) if alerts.is_empty() => Some("No alerts firing"),
            Self::Read(_) => None,
        }
    }
}

/// Where transect reads the alerts from: the resources, and the flags
/// that take precedence over the address in the resources.
pub struct Sourcing {
    /// The `MetricsSource`, which names the Alertmanager.
    pub source: Field<Option<MetricsSource>>,
    /// `--alertmanager`.
    pub url: Option<String>,
    /// `--alertmanager-url-external`: the address where a browser opens
    /// the web interface, when it differs from the address where
    /// transect reads the API.
    pub ui: Option<String>,
    /// The cluster's DNS domain, at the end of a `Service`'s DNS name.
    pub domain: String,
}

impl Sourcing {
    /// The client for the Alertmanager that `source` names, or that the
    /// flag names in its place.  The function keeps the client in
    /// `built` and builds a new one only when the address changes.  It
    /// returns `None` when neither `source` nor the flag names one.
    fn client(
        &self,
        source: &super::Source,
        built: &mut Option<Alertmanager>,
    ) -> Option<Alertmanager> {
        let (url, service) = (source.url.as_deref(), source.service.as_ref());
        let url = upstream::base(self.url.as_deref(), url, service, &self.domain)?;
        let ui = self.ui.clone().unwrap_or_else(|| url.clone());
        if built
            .as_ref()
            .is_none_or(|client| !client.serves(&url, &ui))
        {
            *built = Alertmanager::new(&url, &ui)
                .inspect_err(
                    |error| tracing::warn!(%url, %error, "cannot build a client for Alertmanager"),
                )
                .ok();
        }
        built.clone()
    }
}

/// Reads the Alertmanager that `sourcing` names at once, then every
/// `interval` and whenever the resources change.  It marks `changed`
/// after each reading that differs from the one before.
pub fn poll(
    sourcing: Sourcing,
    interval: Duration,
    changed: watch::Sender<()>,
) -> watch::Receiver<Reading> {
    let (readings, latest) = watch::channel(Reading::Waiting);
    tokio::spawn(async move {
        let mut ticks = tokio::time::interval(interval);
        ticks.set_missed_tick_behavior(MissedTickBehavior::Delay);
        let mut resources = sourcing.source.clone();
        let mut client: Option<Alertmanager> = None;
        loop {
            // When the resources channel's sender is dropped, `changed`
            // returns an error and its arm does not match, so only the
            // tick wakes the loop.
            tokio::select! {
                _ = ticks.tick() => {}
                Ok(()) = resources.changed() => {}
            }
            let source = resources.read_and_update();
            let source = source.and_then(|source| source.alerts);
            let (reading, error) = match source {
                None => (Reading::Undeclared, None),
                Some(source) => match sourcing.client(&source, &mut client) {
                    None => (Reading::Off, None),
                    Some(alertmanager) => {
                        let result = alertmanager.firing(&source).await;
                        let error = result.as_ref().err().map(ToString::to_string);
                        (Reading::from(result), error)
                    }
                },
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

impl From<Result<Vec<Alert>, FetchError>> for Reading {
    fn from(result: Result<Vec<Alert>, FetchError>) -> Self {
        match result {
            Ok(alerts) => Self::Read(Arc::new(alerts)),
            Err(FetchError::Unreachable(_)) => Self::Failed(Failure::Unreachable),
            Err(FetchError::Unreadable(_)) => Self::Failed(Failure::Unreadable),
        }
    }
}

/// Whether `next` starts or ends a failure, or is the first reading.  A
/// failure is logged when it starts and when it ends, not every
/// interval, so an Alertmanager that is down for an hour writes one
/// warning to the log.
fn is_transition(previous: &Reading, next: &Reading) -> bool {
    let failed = |reading: &Reading| match reading {
        Reading::Failed(failure) => Some(*failure),
        _ => None,
    };
    *previous == Reading::Waiting || failed(previous) != failed(next)
}

fn log(reading: &Reading, error: Option<&str>) {
    let note = reading.note().unwrap_or_default();
    match reading {
        Reading::Failed(_) => tracing::warn!(error = error.unwrap_or("none"), "{note}"),
        Reading::Read(_) => tracing::info!("reading alerts from Alertmanager"),
        _ => tracing::info!("{note}"),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::alert;
    use super::*;

    fn read(names: &[&str]) -> Reading {
        let alerts = names
            .iter()
            .map(|name| alert(name, "warning", &[]))
            .collect();
        Reading::Read(Arc::new(alerts))
    }

    const UNREACHABLE: Reading = Reading::Failed(Failure::Unreachable);
    const UNREADABLE: Reading = Reading::Failed(Failure::Unreadable);

    #[rstest]
    #[case::first_success(Reading::Waiting, read(&[]), true)]
    #[case::first_failure(Reading::Waiting, UNREACHABLE, true)]
    #[case::an_alert_starts(read(&[]), read(&["WebDown"]), false)]
    #[case::failure_starts(read(&["WebDown"]), UNREACHABLE, true)]
    #[case::failure_continues(UNREACHABLE, UNREACHABLE, false)]
    #[case::failure_changes(UNREACHABLE, UNREADABLE, true)]
    #[case::failure_ends(UNREACHABLE, read(&[]), true)]
    fn logs_once_per_failure(
        #[case] previous: Reading,
        #[case] next: Reading,
        #[case] expected: bool,
    ) {
        assert_eq!(is_transition(&previous, &next), expected);
    }

    #[rstest]
    #[case::off(
        Reading::Off,
        Some(
            "No Alertmanager to read: set --alertmanager, or a url in the MetricsSource's alerts section"
        )
    )]
    #[case::undeclared(
        Reading::Undeclared,
        Some("A MetricsSource with an alerts section would list the firing alerts")
    )]
    #[case::waiting(Reading::Waiting, None)]
    #[case::unreachable(UNREACHABLE, Some("Alertmanager unreachable"))]
    #[case::unreadable(UNREADABLE, Some("Alertmanager's answer was unreadable"))]
    #[case::quiet(read(&[]), Some("No alerts firing"))]
    #[case::firing(read(&["WebDown"]), None)]
    fn notes(#[case] reading: Reading, #[case] expected: Option<&str>) {
        assert_eq!(reading.note(), expected);
    }
}
