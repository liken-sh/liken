//! Quick search: an index of every object with a page and the fixed
//! pages, ranked by how a name matches what was typed.  A custom kind
//! is found by any of its names, and an object also by its kind's
//! names.  The index is built once per change of the cluster, and a
//! query is a scored scan of it, which on a cluster of a few hundred
//! pods takes well under a millisecond.  Health never changes the
//! order, so one query gives the same list each time.

mod page;
mod rank;
pub mod vulnerabilities;

pub use page::{DIALOG_LIST, PAGE_LIST, Results, Row, SearchPage};
use std::sync::Arc;

use rank::{Match, rank};

use transect_graph::edges::Object;

/// What kind of thing an entry is, in the order that breaks ties.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Sort {
    Page,
    /// A custom kind, which opens its page of every object of it.
    Kind,
    Namespace,
    Workload,
    Machine,
    /// A ground box: something outside the cluster that it reaches.
    Ground,
    /// A custom resource with a page that is not a workload, or a GitOps
    /// object.
    Object,
    /// A host an Ingress answers for, which opens the workload behind it.
    Host,
    Pod,
}

/// One thing search can find.
#[derive(Clone, Debug, PartialEq)]
pub struct Entry {
    /// What a query is matched against.
    pub name: String,
    /// The other names it goes by, matched as its name is, such as a
    /// kind's singular, plural, and short names.
    pub aliases: Vec<String>,
    /// The names of its kind, which match below every match on a name,
    /// so a kind's objects follow the kind's own row.
    pub kind_names: Arc<[String]>,
    /// The word after the name, such as "Deployment" or "machine".
    pub kind: String,
    pub sort: Sort,
    /// Where it is, such as its namespace, or what a host opens.
    pub context: Option<String>,
    pub href: String,
    /// The object it is, whose health its row's dot shows.
    pub object: Option<Object>,
    /// The lenses its row offers besides the overview, as label and URL.
    pub lenses: Vec<(&'static str, String)>,
}

impl Entry {
    /// How `self` and `other` order by where they are, among entries of
    /// one name.  A kind's context is its API group, so kinds of one name
    /// order by group in DNS order.
    fn context_order(&self, other: &Entry) -> std::cmp::Ordering {
        match (self.sort, &self.context, &other.context) {
            (Sort::Kind, Some(a), Some(b)) => transect_graph::dns::order(a, b),
            _ => self.context.cmp(&other.context),
        }
    }
}

/// Every entry, in the order an empty query lists the fixed pages.
#[derive(Debug, Default)]
pub struct Index {
    entries: Vec<Entry>,
}

/// The most rows one query shows.
pub const MOST: usize = 20;

impl Index {
    pub fn new(entries: Vec<Entry>) -> Self {
        Self { entries }
    }

    /// The index with `more` entries added.
    pub fn with(mut self, more: Vec<Entry>) -> Self {
        self.entries.extend(more);
        self
    }

    /// The entries that match `query`, best first, at most `MOST`: by
    /// how the name matches, then by kind, then by name.  An empty query
    /// lists the fixed pages.  Names that contain the query's letters
    /// only scattered show when nothing matched better, because beside a
    /// real match they are noise.
    pub fn find(&self, query: &str) -> Vec<&Entry> {
        let query = query.trim().to_lowercase();
        if query.is_empty() {
            return self
                .entries
                .iter()
                .filter(|e| e.sort == Sort::Page)
                .collect();
        }
        let mut found: Vec<(Tier, &Entry)> = self
            .entries
            .iter()
            .filter_map(|entry| Some((Tier::of(entry, &query)?, entry)))
            .collect();
        if found.iter().any(|(tier, _)| !tier.scattered()) {
            found.retain(|(tier, _)| !tier.scattered());
        }
        found.sort_by(|(a, a_entry), (b, b_entry)| {
            (a, a_entry.sort, &a_entry.name)
                .cmp(&(b, b_entry.sort, &b_entry.name))
                .then_with(|| a_entry.context_order(b_entry))
                .then_with(|| a_entry.href.cmp(&b_entry.href))
        });
        found
            .into_iter()
            .take(MOST)
            .map(|(_, entry)| entry)
            .collect()
    }
}

/// How an entry matched, best first: by its own name or an alias, then
/// by its kind's name, then by letters scattered through its name.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
enum Tier {
    Named(Match),
    OfKind(Match),
    Scattered(usize),
}

impl Tier {
    fn of(entry: &Entry, query: &str) -> Option<Self> {
        let names = std::iter::once(&entry.name).chain(&entry.aliases);
        let named = names.filter_map(|name| rank(name, query)).min();
        let of_kind = entry.kind_names.iter().filter_map(|name| rank(name, query));
        let of_kind = of_kind.filter(|found| !matches!(found, Match::Subsequence(_)));
        match (named, of_kind.min()) {
            (Some(Match::Subsequence(span)), None) => Some(Self::Scattered(span)),
            (Some(Match::Subsequence(_)) | None, Some(found)) => Some(Self::OfKind(found)),
            (Some(found), _) => Some(Self::Named(found)),
            (None, None) => None,
        }
    }

    fn scattered(&self) -> bool {
        matches!(self, Self::Scattered(_))
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn entry(name: &str, sort: Sort, context: Option<&str>) -> Entry {
        Entry {
            name: name.into(),
            aliases: Vec::new(),
            kind_names: Arc::new([]),
            kind: String::new(),
            sort,
            context: context.map(Into::into),
            href: format!("/{name}"),
            object: None,
            lenses: Vec::new(),
        }
    }

    fn index() -> Index {
        Index::new(vec![
            entry("cluster", Sort::Page, None),
            entry("changed", Sort::Page, None),
            entry("web", Sort::Workload, Some("shop")),
            entry("web", Sort::Workload, Some("blog")),
            entry("web", Sort::Namespace, None),
            entry("web-5d8f-x2x7q", Sort::Pod, Some("shop")),
            entry("webhook", Sort::Workload, Some("shop")),
            entry("shop.example.test", Sort::Host, Some("web in shop")),
            entry("cert-webhook", Sort::Workload, Some("certs")),
            entry("weather", Sort::Workload, Some("home")),
            Entry {
                aliases: vec!["gizmo".into(), "gizmos".into(), "gz".into()],
                ..entry("Gizmo", Sort::Kind, Some("example.com"))
            },
            entry("Gadget", Sort::Kind, Some("shop.example.com")),
            entry("Gadget", Sort::Kind, Some("example.com")),
            entry("Gadget", Sort::Kind, Some("blog.example.org")),
            gizmo("red"),
            gizmo("blue"),
            entry("gizmo-press", Sort::Workload, Some("shop")),
            entry("big-zone-mover", Sort::Pod, Some("shop")),
        ])
    }

    /// A Gizmo named `name`, found also by its kind's names.
    fn gizmo(name: &str) -> Entry {
        let names = ["gizmo", "gizmos", "gz"].map(String::from);
        Entry {
            kind_names: Arc::new(names),
            ..entry(name, Sort::Object, Some("shop"))
        }
    }

    fn found(query: &str) -> Vec<(String, Option<String>)> {
        let index = index();
        let found = index.find(query).into_iter();
        found.map(|e| (e.name.clone(), e.context.clone())).collect()
    }

    fn row(name: &str, context: Option<&str>) -> (String, Option<String>) {
        (name.into(), context.map(Into::into))
    }

    #[rstest]
    #[case::nothing_typed("", vec![row("cluster", None), row("changed", None)])]
    #[case::exact_then_prefix_then_word_then_letters(
        "web",
        vec![
            row("web", None),
            row("web", Some("blog")),
            row("web", Some("shop")),
            row("webhook", Some("shop")),
            row("web-5d8f-x2x7q", Some("shop")),
            row("cert-webhook", Some("certs")),
        ]
    )]
    #[case::any_case("WEBH", vec![row("webhook", Some("shop")), row("cert-webhook", Some("certs"))])]
    #[case::part_of_a_host("example", vec![row("shop.example.test", Some("web in shop"))])]
    #[case::nothing_matches("zzz", vec![])]
    #[case::a_kind_then_its_objects(
        "gizmo",
        vec![
            row("Gizmo", Some("example.com")),
            row("gizmo-press", Some("shop")),
            row("blue", Some("shop")),
            row("red", Some("shop")),
        ]
    )]
    #[case::a_kinds_short_name("gz", vec![row("Gizmo", Some("example.com")), row("blue", Some("shop")), row("red", Some("shop"))])]
    #[case::a_kinds_plural("gizmos", vec![row("Gizmo", Some("example.com")), row("blue", Some("shop")), row("red", Some("shop"))])]
    #[case::kinds_of_one_name_by_group_in_dns_order(
        "gadget",
        vec![
            row("Gadget", Some("example.com")),
            row("Gadget", Some("shop.example.com")),
            row("Gadget", Some("blog.example.org")),
        ]
    )]
    #[case::scattered_letters_when_nothing_else_matches("wbhk", vec![row("cert-webhook", Some("certs")), row("webhook", Some("shop"))])]
    fn ranks_and_breaks_ties(#[case] query: &str, #[case] expected: Vec<(String, Option<String>)>) {
        assert_eq!(found(query), expected);
    }
}
