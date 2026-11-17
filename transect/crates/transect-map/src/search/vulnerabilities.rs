//! What quick search finds of the scanners' reports: the
//! vulnerabilities page, when some knowledge describes a findings
//! report, and each id a current report contains, which opens its page.

use std::sync::Arc;

use super::{Entry, Sort};
use transect_supply::vulnerabilities::{Vulnerabilities, link};

/// The entries of `vulnerabilities`, or none when no knowledge
/// describes a findings report.
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
            .holding(id)
            .next()
            .map(|(_, f)| f.severity.to_lowercase()),
        href: link(id),
        object: None,
        lenses: Vec::new(),
    });
    [page].into_iter().chain(ids).collect()
}
