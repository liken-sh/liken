//! The findings for log patterns.  A finding has the pattern's meaning
//! and a line for each workload, pod that is gone, or namespace whose
//! log lines matched the pattern.
//!
//! The recent matches of an `Error` or `Warning` pattern are reported
//! as a reason on their namespace instead, so a finding lists them only
//! when the workload is a `CronJob` whose last run succeeded.

use super::Finding;
use std::collections::HashSet;
use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;

use transect_cluster::ownership::Ownership;
use transect_graph::edges::{Object, Plurals};
use transect_observe::health::{
    FROM_NO_POD, GONE_POD, Origin, Running, SETTLED, is_recent, is_settled, matching_anchor,
    minutes_between, tally_by_workload, tally_evidence, times,
};
use transect_observe::logs::evidence::Evidence;
use transect_observe::logs::patterns::Severity;
use transect_observe::logs::search::{SearchTarget, Searched};
use transect_resources::knowledge::age;

/// The matches of a pattern in one workload, in one pod that is gone,
/// or in the log lines of a namespace that the log source does not
/// attribute to a pod.
#[derive(Clone, Debug, PartialEq)]
pub struct Matched {
    pub namespace: String,
    pub origin: Origin,
    /// The page that the line links to.  It is the workload's page, or
    /// the namespace's page for a pod that is gone or a line with no
    /// pod, as long as the namespace exists.
    pub href: Option<String>,
    pub matches: usize,
    pub minutes_ago: i64,
    pub evidence: Evidence,
    /// The workload is a `CronJob` whose last run succeeded.
    pub settled: bool,
}

/// The findings for the patterns in `searched`, in the patterns' order.
/// A match is listed when its pattern has severity `WorthKnowing`, when
/// it is not recent, or when its workload is in `settled`, the `CronJob`s
/// whose last run succeeded.  A pattern with no listed match has no
/// finding.  `namespaces` are the namespaces that the cluster has, and
/// only those have pages to link to.
pub fn worth_knowing(
    searched: &Searched,
    settled: &HashSet<Object>,
    namespaces: &HashSet<&str>,
    pods: &[Arc<Pod>],
    ownership: &Ownership,
    plurals: &Plurals,
    now: Timestamp,
) -> Vec<Finding> {
    let running = Running::of(pods);
    let found = searched.patterns.iter().filter_map(|(pattern, found)| {
        let Ok(counts) = found else {
            return None;
        };
        let listed = |one: &Matched| {
            pattern.severity == Severity::WorthKnowing
                || one.settled
                || !is_recent(one.minutes_ago, &pattern.window)
        };
        let samples = searched.samples(&SearchTarget::Pattern(pattern.name.clone()));
        let anchor = matching_anchor(&pattern.name);
        let matched: Vec<Matched> = tally_by_workload(counts, pods, ownership)
            .into_iter()
            .map(|one| Matched {
                evidence: tally_evidence(&one, samples, &running, Some(&anchor)),
                settled: is_settled(&one.origin, settled),
                href: match &one.origin {
                    Origin::Workload(workload) => transect_graph::links::link(workload, plurals),
                    Origin::GonePod(_) | Origin::NoPod => namespaces
                        .contains(one.namespace.as_str())
                        .then(|| format!("/namespaces/{}", one.namespace)),
                },
                namespace: one.namespace,
                origin: one.origin,
                matches: one.matches,
                minutes_ago: one.last.map_or(0, |last| minutes_between(last, now)),
            })
            .filter(listed)
            .collect();
        (!matched.is_empty()).then(|| Finding::Logged {
            pattern: pattern.name.clone(),
            meaning: pattern.meaning.clone(),
            window: pattern.window.clone(),
            namespaces: matched,
        })
    });
    found.collect()
}

/// "secret-file-probe: 14 matching lines in the last 1h".
pub fn title(pattern: &str, window: &str, namespaces: &[Matched]) -> String {
    let total: usize = namespaces.iter().map(|one| one.matches).sum();
    format!(
        "{pattern}: {} in the last {window}",
        times(total as i64, "matching line")
    )
}

/// The label of a line: "shop/web" for a workload, "shop/web-9" for a
/// pod that is gone, or "shop" for log lines with no pod.
pub fn label(one: &Matched) -> String {
    match &one.origin {
        Origin::NoPod => one.namespace.clone(),
        origin => format!("{}/{}", one.namespace, origin.name()),
    }
}

/// The text after a line's label, such as "12, the latest 3m ago".  It
/// adds when a `CronJob`'s last run succeeded, or that the lines came
/// from a pod that is gone or from no named pod.
pub fn line(one: &Matched) -> String {
    let counted = format!("{}, the latest {} ago", one.matches, age(one.minutes_ago));
    match &one.origin {
        Origin::Workload(_) if one.settled => format!("{counted}, {SETTLED}"),
        Origin::Workload(_) => counted,
        Origin::GonePod(_) => format!("{counted}, from {GONE_POD}"),
        Origin::NoPod => format!("{counted}, {FROM_NO_POD}"),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_cluster::ownership::fixtures::build;
    use transect_observe::logs::patterns::fixtures::pattern;
    use transect_observe::logs::search::Count;

    const NOW: &str = "2026-01-01T12:03:00Z";
    const THREE_MINUTES_AGO: &str = "2026-01-01T12:00:00Z";

    fn count(namespace: &str, pod: Option<&str>, matches: usize, last: &str) -> Count {
        Count {
            namespace: namespace.into(),
            pod: pod.map(Into::into),
            matches,
            last: last.parse().ok(),
            ..Count::default()
        }
    }

    /// A running pod, `web-0` of the `StatefulSet` `shop/web`.
    fn pods() -> Vec<Arc<Pod>> {
        let pod = serde_yaml::from_str("metadata: {namespace: shop, name: web-0}").unwrap();
        vec![Arc::new(pod)]
    }

    fn ownership() -> Ownership {
        build(&[
            (
                "",
                "Pod",
                Some("shop"),
                "web-0",
                vec![("apps/v1", "StatefulSet", "web")],
            ),
            ("apps", "StatefulSet", Some("shop"), "web", vec![]),
        ])
    }

    /// The namespaces that the cluster has.  It has no `gone`.
    fn namespaces() -> HashSet<&'static str> {
        HashSet::from(["shop", "blog"])
    }

    fn searched(severity: Severity, found: Result<Vec<Count>, String>) -> Searched {
        Searched {
            patterns: vec![(pattern("probe", "env", severity), found)],
            ..Searched::default()
        }
    }

    fn found(severity: Severity, counts: Vec<Count>) -> Vec<Finding> {
        let now = NOW.parse().unwrap();
        let plurals = Plurals::default();
        worth_knowing(
            &searched(severity, Ok(counts)),
            &HashSet::new(),
            &namespaces(),
            &pods(),
            &ownership(),
            &plurals,
            now,
        )
    }

    fn lines(found: &[Finding]) -> Vec<(String, Option<String>, String)> {
        let lines = found.iter().flat_map(Finding::lines);
        lines
            .map(|line| (line.label, line.href, line.text))
            .collect()
    }

    fn owned(lines: Vec<(&str, Option<&str>, &str)>) -> Vec<(String, Option<String>, String)> {
        let owned = lines.into_iter();
        owned
            .map(|(label, href, text)| (label.into(), href.map(Into::into), text.into()))
            .collect()
    }

    #[rstest]
    fn a_worth_knowing_pattern_has_a_title_lines_and_its_meaning() {
        let counts = vec![
            count("blog", Some("blog-0"), 2, THREE_MINUTES_AGO),
            count("shop", Some("web-0"), 12, THREE_MINUTES_AGO),
        ];
        let found = found(Severity::WorthKnowing, counts);
        assert_eq!(
            (found[0].title(), found[0].lines().len(), found[0].body()),
            (
                "probe: 14 matching lines in the last 1h".into(),
                2,
                "probe means something.".into()
            )
        );
    }

    /// A line is labeled with the workload that logged it, with the pod
    /// when the pod is gone, or with the namespace when the log source
    /// gives no pod.
    #[rstest]
    #[case::a_running_pods_workload(
        "shop",
        Some("web-0"),
        vec![("shop/web", Some("/namespaces/shop/statefulsets/web"), "2, the latest 3m ago")]
    )]
    #[case::a_pod_that_is_gone(
        "shop",
        Some("web-9"),
        vec![("shop/web-9", Some("/namespaces/shop"), "2, the latest 3m ago, from a pod that no longer exists")]
    )]
    #[case::no_pod_named(
        "shop",
        None,
        vec![("shop", Some("/namespaces/shop"), "2, the latest 3m ago, from a pod the log source does not name")]
    )]
    #[case::a_namespace_that_is_gone(
        "gone",
        Some("web-9"),
        vec![("gone/web-9", None, "2, the latest 3m ago, from a pod that no longer exists")]
    )]
    fn labels_each_line_by_where_its_log_lines_came_from(
        #[case] namespace: &str,
        #[case] pod: Option<&str>,
        #[case] expected: Vec<(&str, Option<&str>, &str)>,
    ) {
        let found = found(
            Severity::WorthKnowing,
            vec![count(namespace, pod, 2, THREE_MINUTES_AGO)],
        );
        assert_eq!(lines(&found), owned(expected));
    }

    /// A match of an error or a warning pattern is listed once it is no
    /// longer recent.  A recent match is not listed here.
    #[rstest]
    #[case::an_error_inside_the_span(Severity::Error, "2026-01-01T11:04:00Z", vec![])]
    #[case::an_error_at_the_spans_edge(
        Severity::Error,
        "2026-01-01T11:03:00Z",
        vec![("shop/web", Some("/namespaces/shop/statefulsets/web"), "2, the latest 1h ago")]
    )]
    #[case::a_warning_hours_ago(
        Severity::Warning,
        "2026-01-01T01:00:00Z",
        vec![("shop/web", Some("/namespaces/shop/statefulsets/web"), "2, the latest 11h ago")]
    )]
    #[case::worth_knowing_inside_the_span(
        Severity::WorthKnowing,
        "2026-01-01T11:04:00Z",
        vec![("shop/web", Some("/namespaces/shop/statefulsets/web"), "2, the latest 59m ago")]
    )]
    fn lists_an_old_match_of_any_pattern(
        #[case] severity: Severity,
        #[case] last: &str,
        #[case] expected: Vec<(&str, Option<&str>, &str)>,
    ) {
        let found = found(severity, vec![count("shop", Some("web-0"), 2, last)]);
        assert_eq!(lines(&found), owned(expected));
    }

    #[rstest]
    #[case::an_error_pattern(Severity::Error, Ok(vec![count("shop", Some("web-0"), 1, THREE_MINUTES_AGO)]))]
    #[case::no_match(Severity::WorthKnowing, Ok(vec![]))]
    #[case::a_failed_search(Severity::WorthKnowing, Err("unreachable".into()))]
    fn makes_no_finding(#[case] severity: Severity, #[case] found: Result<Vec<Count>, String>) {
        let now = NOW.parse().unwrap();
        let plurals = Plurals::default();
        let namespaces = namespaces();
        let searched = searched(severity, found);
        let none = HashSet::new();
        let found = worth_knowing(
            &searched,
            &none,
            &namespaces,
            &pods(),
            &ownership(),
            &plurals,
            now,
        );
        assert_eq!(found, vec![]);
    }

    /// A recent match of an error pattern is listed when its workload's
    /// last run succeeded, and the line says so.
    #[rstest]
    fn lists_a_recent_error_of_a_settled_workload() {
        let now = NOW.parse().unwrap();
        let plurals = Plurals::default();
        let counts = vec![count("shop", Some("web-0"), 2, THREE_MINUTES_AGO)];
        let searched = searched(Severity::Error, Ok(counts));
        let settled = HashSet::from([Object::new("apps", "StatefulSet", Some("shop"), "web")]);
        let found = worth_knowing(
            &searched,
            &settled,
            &namespaces(),
            &pods(),
            &ownership(),
            &plurals,
            now,
        );
        assert_eq!(
            lines(&found),
            owned(vec![(
                "shop/web",
                Some("/namespaces/shop/statefulsets/web"),
                "2, the latest 3m ago, and its last run succeeded"
            )])
        );
    }
}
