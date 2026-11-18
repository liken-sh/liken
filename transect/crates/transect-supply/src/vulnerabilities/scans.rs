//! The rows of a findings kind's own page, one for each report: the
//! report, its workload and container, the image digest that it
//! scanned, its counts by severity, and whether a pod still runs that
//! digest.  A person who writes the `findings` knowledge uses the page to
//! check what transect reads with it.

use transect_graph::edges::{Object, Plurals};
use transect_graph::links::Part;

use super::Vulnerabilities;

/// One row of the page, for one report.
#[derive(Debug, PartialEq)]
pub struct Scan {
    pub report: Part,
    pub workload: Part,
    pub container: String,
    pub digest: String,
    /// The counts by severity from the report's own summary, without the
    /// severities whose count is zero.
    pub summary: String,
    /// "current" when a pod of the workload runs the digest, otherwise
    /// "stale".
    pub state: &'static str,
}

impl Vulnerabilities {
    /// One row for each report of `group` and `kind`.  A report that has
    /// no container or digest shows "—" for it.
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
