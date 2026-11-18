//! The findings that a vulnerability scanner reports about the images
//! that the cluster runs.  transect reads the scanner's report objects
//! through the `findings` field of the scanner's kind knowledge, and
//! shows the severity, the score, and the scan time as the scanner wrote
//! them.  transect decides only which reports are current: a report is
//! current when a pod of the workload runs the report's image digest.

mod grouped;
pub mod page;
mod read;
mod scans;

use std::collections::{BTreeMap, HashMap};
use std::sync::Arc;

use k8s_openapi::jiff::Timestamp;

use transect_graph::edges::Object;

pub use grouped::{Group, PackageRow};
pub use page::{IdPage, ListPage};
pub use read::Stores;
pub use scans::Scan;

/// Every findings report in one reading of the cluster.
#[derive(Debug, Default)]
pub struct Vulnerabilities {
    /// Whether any kind knowledge loaded describes a findings report.
    pub described: bool,
    pub reports: Vec<Report>,
    /// The scanners' severities, most severe first, in the order their
    /// knowledge's `summary` lists them.
    pub severities: Vec<Arc<str>>,
    /// For each finding id in a current report, the index in `reports`
    /// and in that report's `findings` of each finding with the id.
    by_id: BTreeMap<Arc<str>, Vec<(usize, usize)>>,
}

/// One report: one scan of one container's image.
#[derive(Debug)]
pub struct Report {
    pub object: Object,
    /// The scanner's name, from its kind knowledge.
    pub scanner: Arc<str>,
    /// The workload that the report scanned: the report's owner,
    /// followed to its top owner.
    pub workload: Object,
    pub container: Option<String>,
    pub digest: Option<String>,
    pub scanned_at: Option<Timestamp>,
    /// The report's own counts, by severity, in the summary's order.
    pub summary: Vec<(Arc<str>, u64)>,
    /// Whether a pod of the workload runs the digest that the report
    /// names.
    pub current: bool,
    pub findings: Vec<Finding>,
}

/// One finding, as the scanner wrote it.
#[derive(Clone, Debug, PartialEq)]
pub struct Finding {
    pub id: Arc<str>,
    pub severity: Arc<str>,
    pub score: Option<Arc<str>>,
    pub package: Option<Arc<str>>,
    pub installed: Option<Arc<str>>,
    pub fixed: Option<Arc<str>>,
    /// The scanner's link for the finding, when the link is a plain web
    /// address.
    pub link: Option<Arc<str>>,
    /// The scanner's one-line title.
    pub title: Option<Arc<str>>,
}

impl Finding {
    /// The score as a number, for sorting.
    pub fn score(&self) -> Option<f64> {
        self.score.as_deref()?.parse().ok()
    }
}

impl Vulnerabilities {
    pub fn current(&self) -> impl Iterator<Item = &Report> {
        self.reports.iter().filter(|report| report.current)
    }

    /// Every report of `workload`, current or not, sorted by container.
    pub fn of_workload<'a>(&'a self, workload: &'a Object) -> impl Iterator<Item = &'a Report> {
        let reports = self.reports.iter();
        reports.filter(move |report| report.workload == *workload)
    }

    /// Whether any report, current or not, is about `workload`.  A
    /// workload with a report has a vulnerabilities tab.
    pub fn names(&self, workload: &Object) -> bool {
        self.of_workload(workload).next().is_some()
    }

    /// Each finding id in a current report, in sorted order.
    pub fn ids(&self) -> impl Iterator<Item = &Arc<str>> {
        self.by_id.keys()
    }

    /// The current reports that contain `id`, each with its finding of
    /// that id.
    pub fn reports_with<'a>(&'a self, id: &str) -> impl Iterator<Item = (&'a Report, &'a Finding)> {
        let found = self.by_id.get(id).into_iter().flatten();
        found.map(|(report, finding)| {
            let report = &self.reports[*report];
            (report, &report.findings[*finding])
        })
    }

    /// How many finding ids of each severity `findings` contains.  Each
    /// id is counted once, under the most severe of its severities.  The
    /// counts list the scanners' severities in their order, then any
    /// other severity in alphabetical order, and leave out each severity
    /// with no ids.
    pub fn counts<'a>(&self, findings: impl Iterator<Item = &'a Finding>) -> Counts {
        let mut counted: HashMap<Arc<str>, usize> = HashMap::new();
        for group in self.group(findings) {
            *counted.entry(group.severity).or_default() += 1;
        }
        let mut ordered: Vec<(Arc<str>, usize)> = Vec::new();
        for severity in &self.severities {
            if let Some(count) = counted.remove(severity) {
                ordered.push((severity.clone(), count));
            }
        }
        let mut rest: Vec<(Arc<str>, usize)> = counted.into_iter().collect();
        rest.sort();
        ordered.extend(rest);
        Counts(ordered)
    }

    /// The position of `severity` among the scanners' severities.  A
    /// severity that the scanners do not list sorts after every listed
    /// one.
    pub fn rank(&self, severity: &str) -> usize {
        let at = self.severities.iter().position(|s| &**s == severity);
        at.unwrap_or(self.severities.len())
    }

    /// The count line of `workload`'s current reports, such as "3
    /// critical, 12 high", or `None` when it has no current report.
    pub fn count_line(&self, workload: &Object) -> Option<Counts> {
        let mut reports = self.of_workload(workload).filter(|r| r.current).peekable();
        reports.peek()?;
        let findings = reports.flat_map(|report| &report.findings);
        Some(self.counts(findings))
    }
}

/// How many finding ids of each severity there are, most severe first.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Counts(pub Vec<(Arc<str>, usize)>);

/// One severity's part of a count line, such as "3 critical".
pub struct Tally {
    pub text: String,
    pub tone: &'static str,
}

impl Counts {
    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }

    /// Each part of the line with the color class of its severity.
    pub fn tallies(&self) -> Vec<Tally> {
        let tally = |(severity, count): &(Arc<str>, usize)| Tally {
            text: format!("{count} {}", severity.to_lowercase()),
            tone: tone(severity),
        };
        self.0.iter().map(tally).collect()
    }
}

/// "3 critical, 12 high", or "no findings".
impl std::fmt::Display for Counts {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        if self.0.is_empty() {
            return write!(f, "no findings");
        }
        let parts: Vec<String> = self.tallies().into_iter().map(|t| t.text).collect();
        write!(f, "{}", parts.join(", "))
    }
}

/// The CSS class that colors a severity's word: red for critical,
/// orange for high, and gray for any other severity.
pub fn tone(severity: &str) -> &'static str {
    match severity {
        "CRITICAL" => "vn-critical",
        "HIGH" => "vn-high",
        _ => "vn-quiet",
    }
}

/// The severities that a page shows, read from its `severity` query.
/// The page shows every severity when the query names none.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct Shown {
    pub only: Vec<String>,
}

impl Shown {
    pub fn parse(query: Option<&str>) -> Self {
        let names = query.into_iter().flat_map(|query| query.split(','));
        let mut only: Vec<String> = names
            .map(str::trim)
            .filter(|name| !name.is_empty())
            .map(Into::into)
            .collect();
        only.sort();
        only.dedup();
        Self { only }
    }

    pub fn shows(&self, severity: &str) -> bool {
        self.only.is_empty() || self.only.iter().any(|s| s == severity)
    }

    /// The query that selects these severities, or `None` when the page
    /// shows every severity.
    pub fn query(&self) -> Option<String> {
        (!self.only.is_empty()).then(|| format!("severity={}", self.only.join(",")))
    }
}

/// The path of the page for the finding id `id`.
pub fn link(id: &str) -> String {
    format!("/vulnerabilities/{}", transect_graph::links::segment(id))
}

#[cfg(test)]
mod tests;
