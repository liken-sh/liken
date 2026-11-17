//! `/vulnerabilities` lists every finding id in a current report, with
//! the workloads that run it, and `/vulnerabilities/{id}` is one id's
//! page.

use std::collections::BTreeSet;
use std::sync::Arc;

use askama::Template;
use k8s_openapi::jiff::Timestamp;

use super::{Finding, Group, PackageRow, Report, Shown, Vulnerabilities, link, tone};
use transect_frame::frame::crumbs::Crumb;
use transect_graph::edges::{Object, Plurals};
use transect_graph::links::Part;

/// How many workloads a row names before it counts the rest.
const NAMED: usize = 6;

/// One link of the severity row: every severity, or one alone.
#[derive(Debug, PartialEq)]
pub struct Choice {
    pub label: String,
    pub href: String,
    pub current: bool,
}

/// The severities that a page can show, each at its own URL under
/// `path`.
pub fn choices(
    vulnerabilities: &Vulnerabilities,
    path: &str,
    shown: &Shown,
    held: &[Arc<str>],
) -> Vec<Choice> {
    let mut choices = vec![Choice {
        label: "every severity".into(),
        href: path.into(),
        current: shown.only.is_empty(),
    }];
    let mut severities: Vec<&Arc<str>> = held.iter().collect();
    severities.sort_by_key(|s| (vulnerabilities.rank(s), (*s).clone()));
    severities.dedup();
    choices.extend(severities.into_iter().map(|severity| Choice {
        label: severity.to_lowercase(),
        href: format!("{path}?severity={severity}"),
        current: shown.only.len() == 1 && shown.only[0] == **severity,
    }));
    choices
}

/// One id, with what the scanner reports about it and the workloads
/// that run it.
#[derive(Debug, PartialEq)]
pub struct IdRow {
    pub id: String,
    pub href: String,
    /// The scanner's own link for the id, when the link is a web
    /// address.
    pub out: Option<String>,
    pub severity: String,
    pub tone: &'static str,
    pub score: String,
    pub title: Option<String>,
    /// Each package once, with the versions that fix it.
    pub packages: Vec<PackageRow>,
    pub workloads: Vec<Part>,
    /// The count and the names of the workloads after the first
    /// `NAMED`.
    pub more: Option<(usize, String)>,
}

#[derive(Template)]
#[template(path = "vulnerabilities/list.html", blocks = ["main"])]
pub struct ListPage {
    pub scanners: String,
    pub choices: Vec<Choice>,
    /// How many workloads run an image with a finding shown here.
    pub workloads: usize,
    pub ids: Vec<IdRow>,
    /// How many reports name an image no pod runs now.
    pub stale: usize,
    pub described: bool,
    pub query: Option<String>,
}

impl ListPage {
    pub fn build(vulnerabilities: &Vulnerabilities, plurals: &Plurals, shown: &Shown) -> Self {
        let current: Vec<&Report> = vulnerabilities.current().collect();
        let held: Vec<Arc<str>> = current
            .iter()
            .flat_map(|report| report.findings.iter().map(|f| f.severity.clone()))
            .collect();
        let all = current.iter().flat_map(|report| &report.findings);
        let groups = vulnerabilities.group(all);
        let mut running: BTreeSet<&Object> = BTreeSet::new();
        let ids: Vec<IdRow> = groups
            .into_iter()
            .filter(|group| shown.shows(&group.severity))
            .map(|group| {
                let mut runs: Vec<&Object> = vulnerabilities
                    .holding(&group.id)
                    .map(|(report, _)| &report.workload)
                    .collect();
                runs.sort();
                runs.dedup();
                running.extend(runs.iter().copied());
                let names: Vec<Part> = runs.iter().map(|w| named(w, plurals)).collect();
                let more = (names.len() > NAMED).then(|| {
                    let rest: Vec<&str> = names[NAMED..].iter().map(|p| p.text.as_str()).collect();
                    (rest.len(), rest.join(", "))
                });
                let packages = group.fixes().into_iter().map(|(name, fixed)| PackageRow {
                    name: text(&name),
                    installed: String::new(),
                    fixed,
                });
                IdRow {
                    href: link(&group.id),
                    out: group.link.as_deref().map(String::from),
                    tone: tone(&group.severity),
                    severity: group.severity.to_string(),
                    score: text(&group.score),
                    title: group.title.as_deref().map(String::from),
                    packages: packages.collect(),
                    id: group.id.to_string(),
                    workloads: names.into_iter().take(NAMED).collect(),
                    more,
                }
            })
            .collect();
        let workloads = running.len();
        Self {
            scanners: scanners(vulnerabilities),
            choices: choices(vulnerabilities, "/vulnerabilities", shown, &held),
            workloads,
            ids,
            stale: vulnerabilities
                .reports
                .iter()
                .filter(|r| !r.current)
                .count(),
            described: vulnerabilities.described,
            query: shown.query(),
        }
    }

    /// The sentence at the top of the page: which scanners report how
    /// many finding ids, in the images of how many workloads.
    pub fn lead(&self) -> String {
        let ids = self.ids.len();
        let workloads = self.workloads;
        format!(
            "{} {} {} finding {} in the images {} {} {} now.",
            self.scanners,
            reports(&self.scanners),
            ids,
            plural(ids, "id", "ids"),
            workloads,
            plural(workloads, "workload", "workloads"),
            plural(workloads, "runs", "run"),
        )
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        vec![Crumb::home(), Crumb::current("vulnerabilities")]
    }

    pub fn events_url(&self) -> String {
        match &self.query {
            Some(query) => format!("/events/vulnerabilities?{query}"),
            None => "/events/vulnerabilities".into(),
        }
    }
}

/// One workload's findings of the id in one container.
#[derive(Debug, PartialEq)]
pub struct HoldRow {
    pub workload: Part,
    pub tab: Option<String>,
    pub container: String,
    pub packages: Vec<PackageRow>,
    pub scanned: String,
}

#[derive(Template)]
#[template(path = "vulnerabilities/id.html", blocks = ["main"])]
pub struct IdPage {
    pub id: String,
    pub path: String,
    pub link: Option<String>,
    pub severity: String,
    pub tone: &'static str,
    pub title: Option<String>,
    pub score: String,
    pub scanners: String,
    pub rows: Vec<HoldRow>,
}

impl IdPage {
    /// The page of `id`, or `None` when no current report contains it.
    pub fn build(
        vulnerabilities: &Vulnerabilities,
        plurals: &Plurals,
        id: &str,
        now: Timestamp,
    ) -> Option<Self> {
        let holding: Vec<(&Report, &Finding)> = vulnerabilities.holding(id).collect();
        let group = vulnerabilities
            .group(holding.iter().map(|(_, finding)| *finding))
            .into_iter()
            .next()?;
        let mut held: Vec<(&Report, Vec<&Finding>)> = Vec::new();
        for (report, finding) in &holding {
            let same = |(one, _): &&mut (&Report, Vec<&Finding>)| {
                (&one.workload, &one.container) == (&report.workload, &report.container)
            };
            match held.iter_mut().find(same) {
                Some((_, findings)) => findings.push(finding),
                None => held.push((report, vec![finding])),
            }
        }
        let rows = held.into_iter().map(|(report, findings)| HoldRow {
            workload: named(&report.workload, plurals),
            tab: tab(&report.workload, plurals),
            container: report.container.clone().unwrap_or_else(|| "—".into()),
            packages: vulnerabilities
                .group(findings.into_iter())
                .iter()
                .flat_map(Group::package_rows)
                .collect(),
            scanned: scanned(report, now),
        });
        Some(Self {
            id: group.id.to_string(),
            path: super::link(&group.id),
            link: group.link.as_deref().map(String::from),
            severity: group.severity.to_string(),
            tone: tone(&group.severity),
            title: group.title.as_deref().map(String::from),
            score: text(&group.score),
            scanners: scanners(vulnerabilities),
            rows: rows.collect(),
        })
    }

    pub fn lead(&self) -> String {
        let mut workloads: Vec<&str> = self.rows.iter().map(|r| r.workload.text.as_str()).collect();
        workloads.dedup();
        let count = workloads.len();
        format!(
            "{} {} it in the images {count} {} {} now.",
            self.scanners,
            reports(&self.scanners),
            plural(count, "workload", "workloads"),
            plural(count, "runs", "run"),
        )
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        vec![
            Crumb::home(),
            Crumb::link("vulnerabilities", "/vulnerabilities".into()),
            Crumb::current_name(&self.id),
        ]
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }
}

/// When `report`'s scan ran, as an age such as "3h ago".
pub fn scanned(report: &Report, now: Timestamp) -> String {
    match report.scanned_at {
        Some(at) => {
            let minutes = now.duration_since(at).as_secs() / 60;
            format!("{} ago", transect_resources::knowledge::age(minutes))
        }
        None => "—".into(),
    }
}

/// The scanners that wrote the reports, by name, joined.
pub fn scanners(vulnerabilities: &Vulnerabilities) -> String {
    let names: BTreeSet<&str> = vulnerabilities
        .reports
        .iter()
        .map(|r| &*r.scanner)
        .collect();
    match names.is_empty() {
        true => "The scanner".into(),
        false => names.into_iter().collect::<Vec<_>>().join(" and "),
    }
}

/// "reports" after one scanner's name, "report" after two.
fn reports(scanners: &str) -> &'static str {
    match scanners.contains(" and ") {
        true => "report",
        false => "reports",
    }
}

fn named(object: &Object, plurals: &Plurals) -> Part {
    Part {
        text: transect_graph::links::kind_and_name_in(object, None),
        href: transect_graph::links::link(object, plurals),
    }
}

/// `workload`'s vulnerabilities tab, when it has a page.
pub fn tab(workload: &Object, plurals: &Plurals) -> Option<String> {
    let page = transect_graph::links::page(workload, plurals)?;
    Some(format!("{page}/vulnerabilities"))
}

/// A field's text, or a dash when it is not set.
pub fn text(value: &Option<Arc<str>>) -> String {
    value.as_deref().unwrap_or("—").to_string()
}

fn plural(count: usize, one: &'static str, many: &'static str) -> &'static str {
    match count {
        1 => one,
        _ => many,
    }
}
