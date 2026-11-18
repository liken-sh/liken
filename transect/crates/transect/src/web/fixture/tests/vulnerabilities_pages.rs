use rstest::rstest;

use crate::web::fixture;
use transect_supply::vulnerabilities::{IdPage, ListPage, Shown};

fn list(severity: Option<&str>) -> ListPage {
    let model = fixture::model();
    let plurals = &model.snapshot.plurals;
    ListPage::build(&model.vulnerabilities, plurals, &Shown::parse(severity))
}

#[rstest]
#[case::every_severity(None, vec!["CVE-2026-0001", "CVE-2026-0002", "CVE-2026-0003", "GHSA-ex12-ab34-cd56"])]
#[case::unknown_alone(Some("UNKNOWN"), vec!["CVE-2026-0003", "GHSA-ex12-ab34-cd56"])]
fn lists_by_severity_then_a_fix_then_by_score(
    #[case] severity: Option<&str>,
    #[case] expected: Vec<&str>,
) {
    let page = list(severity);
    let ids: Vec<&str> = page.ids.iter().map(|row| row.id.as_str()).collect();
    assert_eq!(ids, expected);
}

#[rstest]
fn names_each_workload_that_runs_an_id() {
    let page = list(None);
    let shared = &page.ids[0];
    let names: Vec<&str> = shared.workloads.iter().map(|p| p.text.as_str()).collect();
    assert_eq!(
        names,
        vec![
            "Deployment acme-shop/listings",
            "Deployment acme-shop/storefront"
        ]
    );
}

#[rstest]
fn counts_the_workloads_that_run_an_id() {
    assert_eq!((list(None).workloads, list(Some("HIGH")).workloads), (2, 1));
}

#[rstest]
fn links_out_only_to_web_addresses() {
    let page = list(None);
    let outs: Vec<(&str, Option<&str>)> = page
        .ids
        .iter()
        .map(|row| (row.id.as_str(), row.out.as_deref()))
        .collect();
    assert_eq!(
        outs,
        vec![
            (
                "CVE-2026-0001",
                Some("https://advisories.example.test/CVE-2026-0001")
            ),
            (
                "CVE-2026-0002",
                Some("https://advisories.example.test/CVE-2026-0002")
            ),
            (
                "CVE-2026-0003",
                Some("https://advisories.example.test/CVE-2026-0003")
            ),
            ("GHSA-ex12-ab34-cd56", None),
        ]
    );
}

#[rstest]
fn titles_each_id_from_its_findings() {
    let page = list(None);
    assert_eq!(
        page.ids[0].title.as_deref(),
        Some("An overflow in libexample lets a caller run code")
    );
}

#[rstest]
#[case::held("CVE-2026-0001", Some(2))]
#[case::only_in_a_stale_report("CVE-2025-0009", None)]
#[case::unknown("CVE-1999-0000", None)]
fn opens_an_id_only_from_a_current_report(#[case] id: &str, #[case] rows: Option<usize>) {
    let model = fixture::model();
    let now = fixture::NOW.parse().unwrap();
    let page = IdPage::build(&model.vulnerabilities, &model.snapshot.plurals, id, now);
    assert_eq!(page.map(|page| page.rows.len()), rows);
}

#[rstest]
fn lists_each_package_once_with_its_fixes() {
    let page = list(None);
    let packages: Vec<Vec<(&str, &str)>> = page
        .ids
        .iter()
        .map(|row| {
            let each = row.packages.iter();
            each.map(|p| (p.name.as_str(), p.fixed.as_str())).collect()
        })
        .collect();
    assert_eq!(
        packages,
        vec![
            vec![("libexample", "1.2.4"), ("libexample-dev", "1.2.4")],
            vec![("libother", "—")],
            vec![("libother", "2.0.1")],
            vec![("indexer-lib", "—")],
        ]
    );
}

#[rstest]
fn shows_the_highest_score_and_most_severe_severity_of_an_id() {
    let page = list(None);
    let row = &page.ids[0];
    assert_eq!(
        (row.severity.as_str(), row.score.as_str()),
        ("CRITICAL", "9.8")
    );
}

#[rstest]
fn id_page_has_one_row_per_workload_and_container() {
    let model = fixture::model();
    let now = fixture::NOW.parse().unwrap();
    let page = IdPage::build(
        &model.vulnerabilities,
        &model.snapshot.plurals,
        "CVE-2026-0001",
        now,
    );
    let rows: Vec<(String, Vec<String>)> = page
        .unwrap()
        .rows
        .into_iter()
        .map(|row| {
            let names = row.packages.into_iter().map(|p| p.name);
            (row.workload.text, names.collect())
        })
        .collect();
    assert_eq!(
        rows,
        vec![
            (
                "Deployment acme-shop/listings".to_string(),
                vec!["libexample".to_string()]
            ),
            (
                "Deployment acme-shop/storefront".to_string(),
                vec!["libexample".to_string(), "libexample-dev".to_string()]
            ),
        ]
    );
}
