//! Parses Alertmanager's response to `GET /api/v2/alerts`.  Anyone who
//! can write an alerting rule sets an alert's labels and annotations, so
//! this module removes the control characters from each value and cuts
//! it to the length that a card shows, before any other code reads it.

use std::collections::BTreeMap;

use anyhow::Context;
use serde::Deserialize;

use super::Alert;
use super::source::Source;
use transect_graph::untrusted::capped;

/// The longest an alert's name may be, in characters.
const NAME_CHARS: usize = 120;
/// The longest a label value may be, in characters.  It is the longest
/// name a Kubernetes object may have, because an object's name is the
/// longest value that `target` matches against.
const LABEL_CHARS: usize = 253;
const SEVERITY_CHARS: usize = 20;
const SUMMARY_CHARS: usize = 200;
const DESCRIPTION_CHARS: usize = 400;

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Gettable {
    #[serde(default)]
    labels: BTreeMap<String, String>,
    #[serde(default)]
    annotations: BTreeMap<String, String>,
    starts_at: Option<String>,
    status: Option<Status>,
}

#[derive(Deserialize)]
struct Status {
    state: String,
}

/// The alerts in `body` that need attention, parsed with the
/// conventions of `source`.  It leaves out an alert whose severity
/// `source` ignores.  The query in `client.rs` already leaves out
/// silenced and inhibited alerts, and this function also leaves out an
/// alert in the state `suppressed`, so a change to the query cannot
/// bring one back onto the map.
pub fn alerts(body: &str, source: &Source) -> anyhow::Result<Vec<Alert>> {
    let gettable: Vec<Gettable> = serde_json::from_str(body).context("reading the alerts")?;
    let read = gettable.into_iter().filter_map(|one| alert(one, source));
    Ok(read.collect())
}

fn alert(gettable: Gettable, source: &Source) -> Option<Alert> {
    let suppressed = gettable.status.is_some_and(|s| s.state == "suppressed");
    if suppressed {
        return None;
    }
    let labels: BTreeMap<String, String> = gettable
        .labels
        .into_iter()
        .filter_map(|(key, value)| Some((capped(&key, LABEL_CHARS)?, capped(&value, LABEL_CHARS)?)))
        .collect();
    let severity = labels.get(&source.severity.label).map(String::as_str);
    let level = source.severity.level(severity)?;
    let annotation = |key: Option<&String>, chars| capped(gettable.annotations.get(key?)?, chars);
    let names = &source.annotations;
    Some(Alert {
        // Prometheus sets `alertname` to the rule's name, whatever
        // conventions the rules follow for other labels.
        name: labels
            .get("alertname")
            .and_then(|name| capped(name, NAME_CHARS))
            .unwrap_or_else(|| "unnamed".into()),
        severity: severity.and_then(|severity| capped(severity, SEVERITY_CHARS)),
        level,
        summary: annotation(names.summary.as_ref(), SUMMARY_CHARS),
        description: annotation(names.description.as_ref(), DESCRIPTION_CHARS),
        starts_at: gettable.starts_at.and_then(|at| at.parse().ok()),
        link: None,
        labels,
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::alerts::source::fixtures::source;
    use crate::health::Level;

    fn alerts(body: &str) -> anyhow::Result<Vec<Alert>> {
        super::alerts(body, &source())
    }

    /// One alert in the form that Alertmanager returns, with `labels`
    /// and `state`.
    fn body(labels: &str, state: &str) -> String {
        format!(
            r#"[{{"annotations":{{"summary":"web is down","description":"It has not\nanswered for 5m."}},
                "endsAt":"2026-01-01T12:20:00.000Z","fingerprint":"0123456789abcdef",
                "receivers":[{{"name":"default"}}],"startsAt":"2026-01-01T12:00:00.000Z",
                "status":{{"inhibitedBy":[],"silencedBy":[],"state":"{state}"}},
                "updatedAt":"2026-01-01T12:15:00.000Z","generatorURL":"http://prometheus/graph",
                "labels":{labels}}}]"#
        )
    }

    #[rstest]
    fn reads_an_alerts_name_level_text_and_start() {
        let alerts = alerts(&body(
            r#"{"alertname":"WebDown","severity":"critical","namespace":"shop"}"#,
            "active",
        ))
        .unwrap();
        let alert = &alerts[0];
        assert_eq!(
            (
                alert.name.as_str(),
                alert.severity.as_deref(),
                alert.level,
                alert.summary.as_deref(),
                alert.description.as_deref(),
                alert.starts_at.map(|at| at.to_string()),
                alert.labels.get("namespace").map(String::as_str),
            ),
            (
                "WebDown",
                Some("critical"),
                Level::Failing,
                Some("web is down"),
                Some("It has not answered for 5m."),
                Some("2026-01-01T12:00:00Z".into()),
                Some("shop"),
            )
        );
    }

    #[rstest]
    #[case::a_watchdog(r#"{"alertname":"Watchdog","severity":"none"}"#, "active")]
    #[case::an_inhibitor(r#"{"alertname":"InhibitDuringRollout","severity":"none"}"#, "active")]
    #[case::silenced(r#"{"alertname":"WebDown","severity":"warning"}"#, "suppressed")]
    fn leaves_out_what_never_asks_for_attention(#[case] labels: &str, #[case] state: &str) {
        assert_eq!(alerts(&body(labels, state)).unwrap(), vec![]);
    }

    #[rstest]
    fn cuts_long_text() {
        let long = "x".repeat(1000);
        let alerts = alerts(&body(
            &format!(r#"{{"alertname":"{long}","namespace":"{long}"}}"#),
            "active",
        ))
        .unwrap();
        let alert = &alerts[0];
        assert_eq!(
            (
                alert.name.chars().count(),
                alert.labels["namespace"].chars().count()
            ),
            (NAME_CHARS, LABEL_CHARS)
        );
    }

    #[rstest]
    fn an_alert_without_a_name_or_severity_is_an_unnamed_warning() {
        let alerts = alerts(&body("{}", "active")).unwrap();
        assert_eq!(
            (alerts[0].name.as_str(), alerts[0].level),
            ("unnamed", Level::Warning)
        );
    }

    #[rstest]
    #[case::not_json("<html>502 Bad Gateway</html>")]
    #[case::not_a_list(r#"{"error":"bad"}"#)]
    fn reports_an_answer_that_is_not_a_list_of_alerts(#[case] body: &str) {
        assert!(alerts(body).is_err());
    }
}
