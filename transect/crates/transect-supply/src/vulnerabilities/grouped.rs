//! Findings grouped by id.  A scanner writes one finding for each
//! package that an id affects, so one id can appear in several
//! findings.  A table of findings has one row for each id, with the
//! affected packages listed in the row.  A row shows the most severe
//! severity and the highest score among its findings, and the table
//! sorts by those.

use std::cmp::Ordering;
use std::collections::BTreeMap;
use std::sync::Arc;

use super::{Finding, Vulnerabilities};

/// One package an id affects, as one finding names it.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct Package {
    pub name: Option<Arc<str>>,
    pub installed: Option<Arc<str>>,
    pub fixed: Option<Arc<str>>,
}

/// The findings of one id.
#[derive(Clone, Debug, PartialEq)]
pub struct Group {
    pub id: Arc<str>,
    /// The most severe of its findings' severities.
    pub severity: Arc<str>,
    /// The highest score of its findings.
    pub score: Option<Arc<str>>,
    pub link: Option<Arc<str>>,
    pub title: Option<Arc<str>>,
    /// Its distinct packages, sorted.
    pub packages: Vec<Package>,
}

impl Group {
    fn number(&self) -> Option<f64> {
        self.score.as_deref()?.parse().ok()
    }

    fn fixed(&self) -> bool {
        self.packages.iter().any(|package| package.fixed.is_some())
    }

    /// Each package name once, with the distinct versions that fix it
    /// joined, or a dash when no version fixes it.
    pub fn fixes(&self) -> Vec<(Option<Arc<str>>, String)> {
        let mut fixes: BTreeMap<Option<Arc<str>>, Vec<&str>> = BTreeMap::new();
        for package in &self.packages {
            let versions = fixes.entry(package.name.clone()).or_default();
            if let Some(fixed) = package.fixed.as_deref().filter(|f| !versions.contains(f)) {
                versions.push(fixed);
            }
        }
        let joined = |versions: Vec<&str>| match versions.is_empty() {
            true => "—".to_string(),
            false => versions.join(", "),
        };
        fixes.into_iter().map(|(n, v)| (n, joined(v))).collect()
    }
}

impl Vulnerabilities {
    /// The sort key of a severity: first the severities that the kind
    /// knowledge's findings `summary` lists, in that order, then any
    /// other severity, then `UNKNOWN`.
    fn severity_key(&self, severity: &str) -> (u8, usize) {
        match severity {
            "UNKNOWN" => (2, 0),
            other => match self.severities.iter().position(|s| &**s == other) {
                Some(at) => (0, at),
                None => (1, 0),
            },
        }
    }

    /// The order of the rows in a table.  Rows sort by severity as in
    /// `severity_key`.  Within a severity, rows with a fixed version
    /// come first. Then they sort by score, highest first and rows with
    /// no score last, and finally by id.
    pub fn order(&self, a: &Group, b: &Group) -> Ordering {
        let score = match (a.number(), b.number()) {
            (Some(a), Some(b)) => b.total_cmp(&a),
            (Some(_), None) => Ordering::Less,
            (None, Some(_)) => Ordering::Greater,
            (None, None) => Ordering::Equal,
        };
        let severity = |g: &Group| (self.severity_key(&g.severity), g.severity.clone());
        severity(a)
            .cmp(&severity(b))
            .then_with(|| b.fixed().cmp(&a.fixed()))
            .then(score)
            .then_with(|| a.id.cmp(&b.id))
    }

    /// `findings` grouped by id, in the order of [`Self::order`].
    pub fn group<'a>(&self, findings: impl Iterator<Item = &'a Finding>) -> Vec<Group> {
        let mut groups: BTreeMap<Arc<str>, Group> = BTreeMap::new();
        for finding in findings {
            let group = groups.entry(finding.id.clone()).or_insert_with(|| Group {
                id: finding.id.clone(),
                severity: finding.severity.clone(),
                score: None,
                link: None,
                title: None,
                packages: Vec::new(),
            });
            if self.severity_key(&finding.severity) < self.severity_key(&group.severity) {
                group.severity = finding.severity.clone();
            }
            let higher = match (finding.score(), group.number()) {
                (Some(new), Some(held)) => new > held,
                (Some(_), None) => true,
                _ => false,
            };
            if higher {
                group.score = finding.score.clone();
            }
            group.link = group.link.take().or_else(|| finding.link.clone());
            group.title = group.title.take().or_else(|| finding.title.clone());
            group.packages.push(Package {
                name: finding.package.clone(),
                installed: finding.installed.clone(),
                fixed: finding.fixed.clone(),
            });
        }
        let mut groups: Vec<Group> = groups.into_values().collect();
        for group in &mut groups {
            group.packages.sort();
            group.packages.dedup();
        }
        groups.sort_by(|a, b| self.order(a, b));
        groups
    }
}

/// One package's line in a row, with a dash for each missing field.
#[derive(Clone, Debug, PartialEq)]
pub struct PackageRow {
    pub name: String,
    pub installed: String,
    pub fixed: String,
}

fn dash(value: &Option<Arc<str>>) -> String {
    value.as_deref().unwrap_or("—").to_string()
}

impl From<&Package> for PackageRow {
    fn from(package: &Package) -> Self {
        Self {
            name: dash(&package.name),
            installed: dash(&package.installed),
            fixed: dash(&package.fixed),
        }
    }
}

impl Group {
    pub fn package_rows(&self) -> Vec<PackageRow> {
        self.packages.iter().map(PackageRow::from).collect()
    }
}
