use rstest::rstest;

use super::*;
use crate::logs::patterns::LogPattern;
use crate::logs::patterns::fixtures::pattern;
use transect_cluster::ownership::fixtures::build;

const NOW: &str = "2026-01-01T12:10:00Z";

fn pods() -> Vec<Arc<Pod>> {
    ["web-0", "web-1"]
        .map(|name| {
            let text = format!("metadata: {{namespace: shop, name: {name}}}");
            Arc::new(serde_yaml::from_str(&text).unwrap())
        })
        .to_vec()
}

fn ownership() -> Ownership {
    let web = || vec![("apps/v1", "StatefulSet", "web")];
    build(&[
        ("", "Pod", Some("shop"), "web-0", web()),
        ("", "Pod", Some("shop"), "web-1", web()),
        ("apps", "StatefulSet", Some("shop"), "web", vec![]),
    ])
}

fn count(namespace: &str, pod: Option<&str>, matches: usize, last: &str) -> Count {
    Count {
        namespace: namespace.into(),
        pod: pod.map(Into::into),
        matches,
        last: last.parse().ok(),
        ..Count::default()
    }
}

fn searched(patterns: [(LogPattern, Result<Vec<Count>, String>); 1]) -> Searched {
    Searched {
        patterns: patterns.to_vec(),
        ..Searched::default()
    }
}

fn seen() -> Seen {
    Seen::Searched {
        source: "logs".into(),
        window: "1h".into(),
    }
}

#[rstest]
#[case::nothing(vec![], vec![])]
#[case::two_pods_of_one_workload(
    vec![count("shop", Some("web-0"), 3, "2026-01-01T12:00:00Z"), count("shop", Some("web-1"), 2, "2026-01-01T12:05:00Z")],
    vec![("shop", 5, 5)]
)]
#[case::a_pod_gone(vec![count("shop", Some("web-9"), 4, "2026-01-01T12:05:00Z")], vec![])]
#[case::no_pod(vec![count("shop", None, 4, "2026-01-01T12:05:00Z")], vec![])]
fn counts_error_lines_by_workload(
    #[case] counts: Vec<Count>,
    #[case] expected: Vec<(&str, usize, i64)>,
) {
    let now = NOW.parse().unwrap();
    let reasons = errors(&counts, &seen(), &[], &pods(), &ownership(), now);
    let reasons: Vec<(String, Reason)> = reasons
        .into_iter()
        .map(|(namespace, mut reason)| {
            if let Reason::LoggedErrors { evidence, .. } = &mut reason {
                *evidence = Evidence::default();
            }
            (namespace, reason)
        })
        .collect();
    let expected: Vec<(String, Reason)> = expected
        .into_iter()
        .map(|(namespace, errors, minutes_ago)| {
            let reason = Reason::LoggedErrors {
                workload: Object::new("apps", "StatefulSet", Some("shop"), "web"),
                errors,
                minutes_ago,
                seen: seen(),
                evidence: Evidence::default(),
            };
            (namespace.into(), reason)
        })
        .collect();
    assert_eq!(reasons, expected);
}

#[rstest]
#[case::an_error(Severity::Error, vec![
    "shop: web: refused: 5 matching lines in the last 1h, the latest 5m ago",
    "shop: refused: 4 matching lines in the last 1h, the latest 10m ago, from web-9, a pod that no longer exists",
    "shop: refused: 1 matching line in the last 1h, the latest 10m ago, from a pod the log source does not name",
])]
#[case::a_warning(Severity::Warning, vec![
    "shop: web: refused: 5 matching lines in the last 1h, the latest 5m ago",
    "shop: refused: 4 matching lines in the last 1h, the latest 10m ago, from web-9, a pod that no longer exists",
    "shop: refused: 1 matching line in the last 1h, the latest 10m ago, from a pod the log source does not name",
])]
#[case::only_worth_knowing(Severity::WorthKnowing, vec![])]
fn an_error_or_warning_pattern_is_a_reason_per_workload(
    #[case] severity: Severity,
    #[case] expected: Vec<&str>,
) {
    let counts = vec![
        count("shop", Some("web-0"), 3, "2026-01-01T12:00:00Z"),
        count("shop", Some("web-1"), 2, "2026-01-01T12:05:00Z"),
        count("shop", Some("web-9"), 4, "2026-01-01T12:00:00Z"),
        count("shop", None, 1, "2026-01-01T12:00:00Z"),
    ];
    let patterns = [(pattern("refused", "refused", severity), Ok(counts))];
    let reasons: Vec<String> = matched(
        &searched(patterns),
        &HashSet::new(),
        &pods(),
        &ownership(),
        NOW.parse().unwrap(),
    )
    .into_iter()
    .map(|(namespace, reason)| format!("{namespace}: {reason}"))
    .collect();
    assert_eq!(reasons, expected);
}

#[rstest]
#[case::inside_the_span("2026-01-01T11:11:00Z", vec!["shop: web: refused: 3 matching lines in the last 1h, the latest 59m ago"])]
#[case::at_the_spans_edge("2026-01-01T11:10:00Z", vec![])]
#[case::hours_ago("2026-01-01T01:00:00Z", vec![])]
fn only_a_recent_match_is_a_reason(#[case] last: &str, #[case] expected: Vec<&str>) {
    let counts = vec![count("shop", Some("web-0"), 3, last)];
    let patterns = [(pattern("refused", "refused", Severity::Error), Ok(counts))];
    let reasons: Vec<String> = matched(
        &searched(patterns),
        &HashSet::new(),
        &pods(),
        &ownership(),
        NOW.parse().unwrap(),
    )
    .into_iter()
    .map(|(namespace, reason)| format!("{namespace}: {reason}"))
    .collect();
    assert_eq!(reasons, expected);
}

/// The `CronJob` `report`, with its running pod `report-29000020-x7k2q`.
fn report() -> (Vec<Arc<Pod>>, Ownership) {
    let text = "metadata: {namespace: shop, name: report-29000020-x7k2q}";
    let pods = vec![Arc::new(serde_yaml::from_str(text).unwrap())];
    let ownership = build(&[
        (
            "",
            "Pod",
            Some("shop"),
            "report-29000020-x7k2q",
            vec![("batch/v1", "Job", "report-29000020")],
        ),
        (
            "batch",
            "Job",
            Some("shop"),
            "report-29000020",
            vec![("batch/v1", "CronJob", "report")],
        ),
        ("batch", "CronJob", Some("shop"), "report", vec![]),
    ]);
    (pods, ownership)
}

#[rstest]
fn a_cron_jobs_gone_pods_fold_into_its_line() {
    let counts = vec![
        count(
            "shop",
            Some("report-29000020-x7k2q"),
            2,
            "2026-01-01T12:05:00Z",
        ),
        count(
            "shop",
            Some("report-29000010-b4n8z"),
            2,
            "2026-01-01T11:55:00Z",
        ),
        count(
            "shop",
            Some("report-29000000-q9z2m"),
            2,
            "2026-01-01T11:45:00Z",
        ),
        count("shop", Some("migrate-q9z2m"), 1, "2026-01-01T12:00:00Z"),
    ];
    let patterns = [(pattern("refused", "refused", Severity::Error), Ok(counts))];
    let (pods, ownership) = report();
    let reasons: Vec<String> = matched(
        &searched(patterns),
        &HashSet::new(),
        &pods,
        &ownership,
        NOW.parse().unwrap(),
    )
    .into_iter()
    .map(|(namespace, reason)| format!("{namespace}: {reason}"))
    .collect();
    assert_eq!(
        reasons,
        vec![
            "shop: report: refused: 6 matching lines in the last 1h, the latest 5m ago",
            "shop: refused: 1 matching line in the last 1h, the latest 10m ago, from migrate-q9z2m, a pod that no longer exists",
        ]
    );
}

#[rstest]
fn a_failed_search_gives_no_reason() {
    let patterns = [(
        pattern("refused", "refused", Severity::Error),
        Err("unreachable".into()),
    )];
    let found = matched(
        &searched(patterns),
        &HashSet::new(),
        &pods(),
        &ownership(),
        NOW.parse().unwrap(),
    );
    assert_eq!(found, vec![]);
}

/// An error that an earlier run logged does not matter once a later run
/// of the `CronJob` succeeded.
#[rstest]
#[case::its_last_run_succeeded(true, vec![])]
#[case::its_last_run_did_not(false, vec!["shop: report: refused: 2 matching lines in the last 1h, the latest 5m ago"])]
fn a_settled_cron_job_gives_no_reason(#[case] settled: bool, #[case] expected: Vec<&str>) {
    let counts = vec![count(
        "shop",
        Some("report-29000020-x7k2q"),
        2,
        "2026-01-01T12:05:00Z",
    )];
    let patterns = [(pattern("refused", "refused", Severity::Error), Ok(counts))];
    let (pods, ownership) = report();
    let report = Object::new("batch", "CronJob", Some("shop"), "report");
    let settled: HashSet<Object> = settled.then_some(report).into_iter().collect();
    let reasons: Vec<String> = matched(
        &searched(patterns),
        &settled,
        &pods,
        &ownership,
        NOW.parse().unwrap(),
    )
    .into_iter()
    .map(|(namespace, reason)| format!("{namespace}: {reason}"))
    .collect();
    assert_eq!(reasons, expected);
}

#[rstest]
fn a_reason_includes_its_pods_counts_and_lines() {
    let counts = vec![
        count("shop", Some("web-0"), 3, "2026-01-01T12:00:00Z"),
        count("shop", Some("web-1"), 2, "2026-01-01T12:05:00Z"),
    ];
    let line = |pod: &str, message: &str| Sample {
        namespace: Some("shop".into()),
        pod: Some(pod.into()),
        message: message.into(),
        ..Sample::default()
    };
    let mut searched = searched([(pattern("refused", "refused", Severity::Error), Ok(counts))]);
    let samples = vec![line("web-1", "dial: refused"), line("db-0", "not ours")];
    searched.samples = vec![(SearchTarget::Pattern("refused".into()), samples)];
    let found = matched(
        &searched,
        &HashSet::new(),
        &pods(),
        &ownership(),
        NOW.parse().unwrap(),
    );
    let evidence = found[0].1.evidence().unwrap();
    let parts: Vec<(&str, Option<&str>)> = evidence
        .parts
        .iter()
        .map(|part| (part.label(), part.href.as_deref()))
        .collect();
    let lines: Vec<&str> = evidence
        .samples
        .iter()
        .map(|s| s.message.as_str())
        .collect();
    assert_eq!(
        (parts, lines),
        (
            vec![
                (
                    "web-0",
                    Some("/namespaces/shop/pods/web-0/logs#matching-refused")
                ),
                (
                    "web-1",
                    Some("/namespaces/shop/pods/web-1/logs#matching-refused")
                ),
            ],
            vec!["dial: refused"]
        )
    );
}
