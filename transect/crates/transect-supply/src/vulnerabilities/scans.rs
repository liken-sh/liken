//! What each report of a findings kind scanned, as the kind's own page
//! lists it: the report, its workload, its container, the digest that
//! it names, and whether a pod runs that digest.  A person who writes
//! the `findings` knowledge can check its effect on this page.

use transect_graph::edges::{Object, Plurals};
use transect_graph::links::Part;

use super::Vulnerabilities;

/// One report's row.
#[derive(Debug, PartialEq)]
pub struct Scan {
    pub report: Part,
    pub workload: Part,
    pub container: String,
    pub digest: String,
    /// The report's own counts by severity, as its summary gives them.
    pub summary: String,
    /// "current" or "stale".
    pub state: &'static str,
}

impl Vulnerabilities {
    /// The rows of the reports of `group` and `kind`, by workload.
    pub fn scans(&self, group: &str, kind: &str, plurals: &Plurals) -> Vec<Scan> {
        let named = |object: &Object| Part {
            text: transect_graph::links::kind_and_name_in(object, None),
            href: transect_graph::links::link(object, plurals),
        };
        let reports = self.reports.iter();
        let reports = reports.filter(|r| r.object.group == group && r.object.kind == kind);
        reports
            .map(|report| Scan {
                report: Part {
                    text: report.object.name.clone(),
                    href: transect_graph::links::link(&report.object, plurals),
                },
                workload: named(&report.workload),
                container: report.container.clone().unwrap_or_else(|| "—".into()),
                digest: report.digest.clone().unwrap_or_else(|| "—".into()),
                summary: {
                    let counts = report.summary.iter().filter(|(_, count)| *count > 0);
                    let counts =
                        counts.map(|(severity, count)| (severity.clone(), *count as usize));
                    super::Counts(counts.collect()).to_string()
                },
                state: match report.current {
                    true => "current",
                    false => "stale",
                },
            })
            .collect()
    }
}
