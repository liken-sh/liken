//! Quick search entries for vulnerability scanners' reports: one for the
//! vulnerabilities page, and one for each finding id in a current report
//! that links to that id's page.

use std::sync::Arc;

use super::{Entry, Sort};
use transect_supply::vulnerabilities::{Vulnerabilities, link};

/// Returns no entries when no loaded `KindKnowledge` has a `findings`
/// section, because then the cluster has no kind of findings report.
pub fn entries(vulnerabilities: &Vulnerabilities) -> Vec<Entry> {
    if !vulnerabilities.described {
        return Vec::new();
    }
    let page = Entry {
        aliases: vec!["cve".into(), "findings".into()],
        kind_names: Arc::new([]),
        name: "vulnerabilities".into(),
        kind: "page".into(),
        sort: Sort::Page,
        context: None,
        href: "/vulnerabilities".into(),
        object: None,
        lenses: Vec::new(),
    };
    let ids = vulnerabilities.ids().map(|id| Entry {
        aliases: Vec::new(),
        kind_names: Arc::new([]),
        name: id.to_string(),
        kind: "finding".into(),
        sort: Sort::Object,
        context: vulnerabilities
            .reports_with(id)
            .next()
            .map(|(_, f)| f.severity.to_lowercase()),
        href: link(id),
        object: None,
        lenses: Vec::new(),
    });
    [page].into_iter().chain(ids).collect()
}
