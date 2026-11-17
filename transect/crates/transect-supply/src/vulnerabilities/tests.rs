use rstest::rstest;

use super::*;
use std::sync::Arc;

fn finding(id: &str, score: Option<&str>, fixed: Option<&str>) -> Finding {
    Finding {
        id: id.into(),
        severity: "HIGH".into(),
        score: score.map(Into::into),
        package: None,
        installed: None,
        fixed: fixed.map(Into::into),
        link: None,
        title: None,
    }
}

fn leveled(id: &str, severity: &str) -> Finding {
    Finding {
        severity: severity.into(),
        ..finding(id, None, None)
    }
}

fn scanner_order() -> Vulnerabilities {
    let severities = ["CRITICAL", "HIGH", "MEDIUM", "UNKNOWN"];
    Vulnerabilities {
        severities: severities.iter().map(|s| Arc::from(*s)).collect(),
        ..Default::default()
    }
}

fn sorted(findings: &[Finding]) -> Vec<(String, String)> {
    let groups = scanner_order().group(findings.iter());
    groups
        .into_iter()
        .map(|g| (g.id.to_string(), g.severity.to_string()))
        .collect()
}

#[rstest]
fn sorts_a_fix_first_then_by_score_then_by_id() {
    let findings = [
        finding("d", None, Some("1")),
        finding("c", Some("9.8"), None),
        finding("b", Some("5.0"), Some("1")),
        finding("a", Some("7.5"), Some("1")),
        finding("e", Some("7.5"), Some("1")),
    ];
    let ids: Vec<String> = sorted(&findings).into_iter().map(|(id, _)| id).collect();
    assert_eq!(ids, vec!["a", "e", "b", "d", "c"]);
}

#[rstest]
fn sorts_severity_first_in_the_scanners_order_with_unknown_last() {
    let findings = [
        leveled("a", "UNKNOWN"),
        leveled("b", "LOW"),
        leveled("c", "HIGH"),
        leveled("d", "CRITICAL"),
        leveled("e", "MEDIUM"),
        leveled("f", "ODD"),
    ];
    let severities: Vec<String> = sorted(&findings).into_iter().map(|(_, s)| s).collect();
    assert_eq!(
        severities,
        vec!["CRITICAL", "HIGH", "MEDIUM", "LOW", "ODD", "UNKNOWN"]
    );
}

fn package(id: &str, severity: &str, score: &str, name: &str) -> Finding {
    Finding {
        package: Some(name.into()),
        installed: Some("1".into()),
        ..finding(id, Some(score), None).with_severity(severity)
    }
}

impl Finding {
    fn with_severity(self, severity: &str) -> Self {
        Self {
            severity: severity.into(),
            ..self
        }
    }
}

#[rstest]
fn groups_one_id_at_its_highest_score_and_most_severe_severity() {
    let findings = [
        package("x", "MEDIUM", "5.8", "b"),
        package("x", "CRITICAL", "9.8", "a"),
        package("x", "HIGH", "9.8", "a"),
        package("x", "CRITICAL", "9.8", "a"),
    ];
    let groups = scanner_order().group(findings.iter());
    let shown: Vec<(&str, Option<&str>, Vec<&str>)> = groups
        .iter()
        .map(|g| {
            let names = g.packages.iter().filter_map(|p| p.name.as_deref());
            (&*g.severity, g.score.as_deref(), names.collect())
        })
        .collect();
    assert_eq!(shown, vec![("CRITICAL", Some("9.8"), vec!["a", "b"])]);
}

#[rstest]
#[case::critical("CRITICAL", "vn-critical")]
#[case::high("HIGH", "vn-high")]
#[case::medium("MEDIUM", "vn-quiet")]
#[case::unknown("UNKNOWN", "vn-quiet")]
fn colors_only_critical_and_high(#[case] severity: &str, #[case] class: &str) {
    assert_eq!(tone(severity), class);
}

#[rstest]
#[case::every_one(None, vec![], true)]
#[case::some(Some("HIGH,CRITICAL"), vec!["CRITICAL", "HIGH"], false)]
#[case::blank(Some(" , "), vec![], true)]
fn reads_the_severities_a_page_asks_for(
    #[case] query: Option<&str>,
    #[case] only: Vec<&str>,
    #[case] shows_unknown: bool,
) {
    let shown = Shown::parse(query);
    assert_eq!(
        (shown.only.clone(), shown.shows("UNKNOWN")),
        (
            only.into_iter().map(String::from).collect::<Vec<_>>(),
            shows_unknown
        )
    );
}
