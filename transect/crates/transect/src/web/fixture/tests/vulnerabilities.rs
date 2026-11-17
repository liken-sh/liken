//! What the vulnerabilities reader reads of the fixture cluster's scanner
//! reports.

use rstest::rstest;

use crate::web::fixture;
use std::sync::Arc;
use transect_graph::edges::Object;

fn deployment(name: &str) -> Object {
    Object::new("apps", "Deployment", Some("acme-shop"), name)
}

fn ledger() -> Object {
    Object::new("apps", "StatefulSet", Some("acme-ledger"), "ledger-db")
}

#[rstest]
#[case::the_storefront(deployment("storefront"), vec![(Some("main"), true)])]
#[case::the_listings_two_containers(deployment("listings"), vec![(Some("indexer"), true), (Some("main"), true)])]
#[case::a_digest_no_pod_runs(ledger(), vec![(Some("main"), false)])]
fn reads_each_report_and_whether_it_is_current(
    #[case] workload: Object,
    #[case] expected: Vec<(Option<&str>, bool)>,
) {
    let model = fixture::model();
    let reports = model.vulnerabilities.of_workload(&workload);
    let found: Vec<(Option<&str>, bool)> = reports
        .map(|report| (report.container.as_deref(), report.current))
        .collect();
    assert_eq!(found, expected);
}

#[rstest]
fn keeps_only_the_fields_the_knowledge_names() {
    let snapshot = fixture::snapshot();
    let (_, reports) = snapshot
        .custom
        .iter()
        .find(|(resource, _)| resource.kind == "ImageReport")
        .unwrap();
    let text = serde_json::to_string(&reports[0].data).unwrap();
    assert!(!text.contains("A description the store never keeps"));
    assert!(text.contains("An overflow in libexample lets a caller run code"));
}

#[rstest]
fn holds_one_copy_of_a_title_many_reports_share() {
    let model = fixture::model();
    let titles: Vec<_> = model
        .vulnerabilities
        .holding("CVE-2026-0001")
        .filter_map(|(_, finding)| finding.title.clone())
        .collect();
    assert!(titles.len() == 3 && titles.iter().all(|t| Arc::ptr_eq(t, &titles[0])));
}

#[rstest]
fn indexes_each_current_id_by_the_workloads_that_run_it() {
    let model = fixture::model();
    let vulnerabilities = &model.vulnerabilities;
    let ids: Vec<&str> = vulnerabilities.ids().map(|id| &**id).collect();
    let holding: Vec<String> = vulnerabilities
        .holding("CVE-2026-0001")
        .map(|(report, finding)| format!("{} {:?}", report.workload.name, finding.installed))
        .collect();
    assert_eq!(
        (ids, holding),
        (
            vec![
                "CVE-2026-0001",
                "CVE-2026-0002",
                "CVE-2026-0003",
                "GHSA-ex12-ab34-cd56"
            ],
            vec![
                "listings Some(\"1.2.0\")".to_string(),
                "storefront Some(\"1.2.3\")".to_string(),
                "storefront Some(\"1.2.3\")".to_string()
            ]
        )
    );
}

#[rstest]
#[case::the_storefront(deployment("storefront"), Some("1 critical, 1 high, 1 unknown"))]
#[case::the_listings(deployment("listings"), Some("1 critical, 1 unknown"))]
#[case::only_a_stale_report(ledger(), None)]
fn counts_current_findings_in_the_scanners_order(
    #[case] workload: Object,
    #[case] expected: Option<&str>,
) {
    let model = fixture::model();
    assert_eq!(
        model
            .vulnerabilities
            .count_line(&workload)
            .map(|counts| counts.to_string())
            .as_deref(),
        expected
    );
}
