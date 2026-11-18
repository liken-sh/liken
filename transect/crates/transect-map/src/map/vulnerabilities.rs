//! The vulnerabilities tab of a workload page. It has one box for each
//! container's current scanner report, listing the findings, and one box
//! for the reports of image digests that no pod of the workload runs
//! now.

use askama::Template;
use k8s_openapi::jiff::Timestamp;

use crate::cards::WorkloadCard;
use transect_frame::frame::crumbs::Crumb;
use transect_frame::frame::tabs::{Lens, Tabs};
use transect_graph::edges::Object;
use transect_supply::vulnerabilities::page::{self, Choice, choices, text};
use transect_supply::vulnerabilities::{
    Counts, Group, PackageRow, Shown, Vulnerabilities, link, tone,
};

/// One finding id with the packages that it affects.
#[derive(Debug, PartialEq)]
pub struct FindingRow {
    pub id: String,
    pub href: String,
    pub severity: String,
    pub tone: &'static str,
    pub score: String,
    pub title: Option<String>,
    pub packages: Vec<PackageRow>,
    pub out: Option<String>,
}

/// One container's current report.
#[derive(Debug, PartialEq)]
pub struct Scanned {
    pub container: String,
    pub lead: String,
    pub counts: Counts,
    pub rows: Vec<FindingRow>,
}

/// A report of an image digest that no pod of the workload runs now.
#[derive(Debug, PartialEq)]
pub struct Stale {
    pub container: String,
    pub digest: String,
    pub scanned: String,
    pub findings: usize,
}

#[derive(Template)]
#[template(path = "map/vulnerabilities.html", blocks = ["main"])]
pub struct WorkloadVulnerabilitiesPage {
    pub namespace: String,
    pub layer: String,
    pub name: String,
    pub workload_path: String,
    pub access: bool,
    pub choices: Vec<Choice>,
    pub scanned: Vec<Scanned>,
    pub stale: Vec<Stale>,
    pub query: Option<String>,
    pub card: WorkloadCard,
}

/// The tab's content, without the breadcrumbs and the card that
/// `WorkloadVulnerabilitiesPage` adds.
pub struct Drawn {
    pub choices: Vec<Choice>,
    pub scanned: Vec<Scanned>,
    pub stale: Vec<Stale>,
}

/// Returns `None` when no report, current or stale, is about `workload`,
/// because then the workload has no vulnerabilities tab.
pub fn drawn(
    vulnerabilities: &Vulnerabilities,
    workload: &Object,
    path: &str,
    shown: &Shown,
    now: Timestamp,
) -> Option<Drawn> {
    if !vulnerabilities.names(workload) {
        return None;
    }
    let reports: Vec<_> = vulnerabilities.of_workload(workload).collect();
    let severities: Vec<_> = reports
        .iter()
        .filter(|r| r.current)
        .flat_map(|r| r.findings.iter().map(|f| f.severity.clone()))
        .collect();
    let current = reports.iter().filter(|r| r.current).map(|report| {
        let groups = vulnerabilities.group(report.findings.iter());
        let count = groups.len();
        let container = report
            .container
            .clone()
            .unwrap_or_else(|| "its image".into());
        let lead = format!(
            "{} reports {count} finding {} in the image of {container}, scanned {}.",
            report.scanner,
            if count == 1 { "id" } else { "ids" },
            page::scanned(report, now),
        );
        Scanned {
            counts: vulnerabilities.counts(report.findings.iter()),
            rows: groups
                .iter()
                .filter(|g| shown.shows(&g.severity))
                .map(row)
                .collect(),
            container,
            lead,
        }
    });
    let stale = reports.iter().filter(|r| !r.current).map(|report| Stale {
        container: report.container.clone().unwrap_or_else(|| "—".into()),
        digest: report.digest.clone().unwrap_or_else(|| "—".into()),
        scanned: page::scanned(report, now),
        findings: report.findings.len(),
    });
    Some(Drawn {
        choices: choices(
            vulnerabilities,
            &format!("{path}/vulnerabilities"),
            shown,
            &severities,
        ),
        scanned: current.collect(),
        stale: stale.collect(),
    })
}

fn row(group: &Group) -> FindingRow {
    FindingRow {
        id: group.id.to_string(),
        href: link(&group.id),
        severity: group.severity.to_string(),
        tone: tone(&group.severity),
        score: text(&group.score),
        title: group.title.as_deref().map(String::from),
        packages: group.package_rows(),
        out: group.link.as_deref().map(String::from),
    }
}

impl WorkloadVulnerabilitiesPage {
    pub fn path(&self) -> String {
        format!("{}/vulnerabilities", self.workload_path)
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        let select = format!("/?select={}", self.namespace);
        vec![
            Crumb::home(),
            Crumb::link(&self.layer, format!("{select}#{}", self.layer)),
            Crumb::link_name(&self.namespace, format!("/namespaces/{}", self.namespace)),
            Crumb::link_name(&self.name, self.workload_path.clone()),
            Crumb::current("vulnerabilities"),
        ]
    }

    pub fn tabs(&self) -> Tabs {
        Tabs::workload(&self.workload_path, Lens::Vulnerabilities, self.access)
            .with_vulnerabilities(&self.workload_path, true)
    }

    pub fn events_url(&self) -> String {
        match &self.query {
            Some(query) => format!("/events{}?{query}", self.path()),
            None => format!("/events{}", self.path()),
        }
    }
}
