//! The pages for custom kinds.  `/kinds` lists every custom kind the
//! cluster serves, in one box per API group.  `/kinds/{plural.group}`
//! lists one kind's objects across all namespaces, with their health and
//! printer columns, beside a card for the kind.

use askama::Template;

use super::Item;
use crate::map::KnownEntry as Entry;
use transect_frame::frame::crumbs::Crumb;
use transect_graph::edges::provenance::legend;
use transect_graph::links::Reference;
use transect_observe::health::Level;
use transect_resources::intent::Line;

#[derive(Template)]
#[template(path = "objects/kinds.html", blocks = ["main"])]
pub struct KindsPage {
    /// The kinds that have objects, by API group.
    pub groups: Vec<ApiGroup>,
    /// The kinds that have no objects, by API group, shown together in
    /// one box.
    pub empty: Vec<ApiGroup>,
}

/// One API group's kinds.  When every kind in the group has the same
/// controllers, `controllers` lists them once for the group and each
/// kind's own list is empty.
#[derive(Debug, PartialEq)]
pub struct ApiGroup {
    pub name: String,
    pub kinds: Vec<KindRow>,
    pub controllers: Vec<Reference>,
}

/// One kind in the list.  `summary` is the first sentence of its CRD's
/// description.
#[derive(Debug, PartialEq)]
pub struct KindRow {
    pub label: String,
    pub href: String,
    pub count: usize,
    pub unhealthy: usize,
    /// The worst health level among its unhealthy objects, which colors
    /// the dot beside their count.
    pub worst: Option<Level>,
    pub controllers: Vec<Reference>,
    pub summary: Option<String>,
}

impl KindRow {
    /// The label inside its group's box.  A label ends with " (group)"
    /// when another group has a kind of the same plural.  The box
    /// already shows the group, so the suffix is dropped.
    pub fn label_in(&self, group: &str) -> &str {
        let suffix = format!(" ({group})");
        self.label
            .strip_suffix(suffix.as_str())
            .unwrap_or(&self.label)
    }

    /// The text "2 not ready" and the CSS class of its dot, or `None`
    /// when no object is unhealthy.
    pub fn unhealthy_text(&self) -> Option<(String, &'static str)> {
        let worst = self.worst.filter(|_| self.unhealthy > 0)?;
        Some((
            format!("{} not ready", self.unhealthy),
            super::dot_class(worst),
        ))
    }
}

impl ApiGroup {
    /// When every kind has the same non-empty controllers, they move to
    /// the group, so the box shows them once instead of on each row.
    pub fn new(name: String, mut kinds: Vec<KindRow>) -> Self {
        let first = kinds.first().map(|kind| kind.controllers.clone());
        let shared = first.filter(|first| {
            !first.is_empty() && kinds.iter().all(|kind| &kind.controllers == first)
        });
        if shared.is_some() {
            for kind in &mut kinds {
                kind.controllers.clear();
            }
        }
        Self {
            name,
            kinds,
            controllers: shared.unwrap_or_default(),
        }
    }
}

/// Returns the last two labels of an API group, such as `fluxcd.io` for
/// `source.toolkit.fluxcd.io`, or the whole group when it has fewer.
pub fn domain(group: &str) -> &str {
    let mut dots = group.rmatch_indices('.');
    match (dots.next(), dots.next()) {
        (Some(_), Some((at, _))) => &group[at + 1..],
        _ => group,
    }
}

impl KindsPage {
    /// Returns the heading before the group at index `at`: the group's
    /// domain, when it is the first group of that domain.  The page
    /// sorts the groups by their labels from the right, so the groups
    /// of a domain are next to each other.  A domain whose only group is
    /// the domain itself gets no heading.
    pub fn heading(&self, at: &usize) -> Option<&str> {
        let at = *at;
        let name = &self.groups.get(at)?.name;
        let this = domain(name);
        let starts = at == 0 || domain(&self.groups[at - 1].name) != this;
        let groups = self.groups.iter().filter(|g| domain(&g.name) == this);
        let alone = groups.count() == 1 && name == this;
        (starts && !alone).then_some(this)
    }

    pub fn empty_count(&self) -> usize {
        self.empty.iter().map(|group| group.kinds.len()).sum()
    }

    /// The number of kinds that have objects, and the total number of
    /// those objects.
    pub fn totals(&self) -> (usize, usize) {
        let kinds = self.groups.iter().flat_map(|group| &group.kinds);
        kinds.fold((0, 0), |(kinds, objects), kind| {
            (kinds + 1, objects + kind.count)
        })
    }

    /// The kinds that have an unhealthy object, for the card.
    pub fn unhealthy(&self) -> Vec<&KindRow> {
        let kinds = self.groups.iter().flat_map(|group| &group.kinds);
        kinds
            .filter(|kind| kind.unhealthy_text().is_some())
            .collect()
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        vec![Crumb::home(), Crumb::current("kinds")]
    }

    pub fn events_url(&self) -> String {
        "/events/kinds".into()
    }
}

#[derive(Template)]
#[template(path = "objects/kind.html", blocks = ["main"])]
pub struct KindPage {
    pub label: String,
    pub path: String,
    /// The kinds of this kind's API group, including this one.
    pub kind_links: Vec<Entry>,
    /// The names of the CRD's printer columns, which follow the name and
    /// namespace columns.
    pub columns: Vec<String>,
    pub rows: Vec<KindObject>,
    /// What each report scanned, for a kind whose objects are
    /// vulnerability reports.
    pub scans: Vec<transect_supply::vulnerabilities::Scan>,
    pub card: KindCard,
}

/// One object of the kind, with the value of each printer column.
#[derive(Debug, PartialEq)]
pub struct KindObject {
    pub item: Item,
    pub namespace: Option<String>,
    /// The condition that decides its health, as text such as
    /// "Ready: issuing", or `None` when it reports no condition.
    pub condition: Option<String>,
    pub values: Vec<String>,
}

#[derive(Debug, PartialEq)]
pub struct KindCard {
    pub label: String,
    pub group: String,
    pub count: usize,
    pub unhealthy: usize,
    pub worst: Option<Level>,
    pub why: Option<Line>,
    pub controllers: Vec<Reference>,
    /// Which rule found the controllers, as text.
    pub rule: Option<String>,
}

impl KindCard {
    /// The text beside the dot, such as "3 objects" or "5 objects, 2 not
    /// ready".
    pub fn status_text(&self) -> String {
        let objects = match self.count {
            1 => "1 object".to_string(),
            count => format!("{count} objects"),
        };
        match self.unhealthy {
            0 => objects,
            unhealthy => format!("{objects}, {unhealthy} not ready"),
        }
    }

    pub fn dot_class(&self) -> &'static str {
        match self.worst.filter(|_| self.unhealthy > 0) {
            Some(level) => super::dot_class(level),
            None => "dot dot-gray",
        }
    }

    pub fn legend(&self) -> Option<String> {
        legend(self.why.iter().map(|line| &line.provenance))
    }
}

/// Whether a table with this many columns takes the full page width.
/// Below 1400px, a table of more than four columns does not fit beside
/// the card, so the card moves below it.
pub fn wide_table(columns: usize) -> bool {
    columns > 4
}

impl KindPage {
    /// The table has a name column, a namespace column, and one column
    /// for each printer column.
    pub fn wide(&self) -> bool {
        wide_table(self.columns.len() + 2)
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        vec![
            Crumb::home(),
            Crumb::link("kinds", "/kinds".into()),
            Crumb::current(&self.label),
        ]
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }

    pub fn none(&self) -> String {
        format!("The cluster has no {} now.", self.label)
    }
}

/// A custom kind whose objects a workload controls, with a row for each
/// object.
#[derive(Debug, PartialEq)]
pub struct Manages {
    pub label: String,
    pub href: String,
    /// Which rule matched the workload as this kind's controller, as
    /// text.
    pub rule: String,
    pub columns: Vec<String>,
    pub rows: Vec<KindObject>,
}

/// Returns the first sentence of `text`, for the kind's tooltip on
/// `/kinds`.
pub fn first_sentence(text: &str) -> String {
    match text.find(". ") {
        Some(at) => text[..=at].to_string(),
        None => text.to_string(),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::two("A Camera images. It belongs to a train.", "A Camera images.")]
    #[case::one("A Camera images.", "A Camera images.")]
    #[case::a_host_name("Serves web.example.test now", "Serves web.example.test now")]
    fn takes_the_first_sentence(#[case] text: &str, #[case] expected: &str) {
        assert_eq!(first_sentence(text), expected);
    }

    #[rstest]
    #[case::a_subdomain("source.toolkit.fluxcd.io", "fluxcd.io")]
    #[case::the_domain_itself("liken.sh", "liken.sh")]
    #[case::one_label("example", "example")]
    fn reads_the_domain_a_group_is_registered_under(#[case] group: &str, #[case] expected: &str) {
        assert_eq!(domain(group), expected);
    }

    #[rstest]
    #[case::in_its_own_group("liken.sh", "Clusters")]
    #[case::elsewhere("postgresql.cnpg.io", "Clusters (liken.sh)")]
    fn drops_the_group_from_a_label_inside_its_box(#[case] group: &str, #[case] shown: &str) {
        let kind = KindRow {
            label: "Clusters (liken.sh)".into(),
            href: String::new(),
            count: 1,
            unhealthy: 0,
            worst: None,
            controllers: Vec::new(),
            summary: None,
        };
        assert_eq!(kind.label_in(group), shown);
    }

    #[test]
    fn heads_each_domain_once_where_its_groups_start() {
        let group = |name: &str| ApiGroup::new(name.into(), Vec::new());
        let page = KindsPage {
            groups: [
                "example.io",
                "liken.sh",
                "shop.liken.sh",
                "monitoring.coreos.com",
            ]
            .map(group)
            .into(),
            empty: Vec::new(),
        };
        let headings: Vec<Option<&str>> = (0..4).map(|at| page.heading(&at)).collect();
        assert_eq!(headings, [None, Some("liken.sh"), None, Some("coreos.com")]);
    }

    #[rstest]
    #[case::four_columns_fit_beside_the_card(4, false)]
    #[case::five_take_the_page(5, true)]
    fn takes_the_page_for_a_table_past_four_columns(#[case] columns: usize, #[case] wide: bool) {
        assert_eq!(wide_table(columns), wide);
    }

    fn row(label: &str, controllers: &[&str]) -> KindRow {
        KindRow {
            label: label.into(),
            href: format!("/kinds/{label}"),
            count: 1,
            unhealthy: 0,
            worst: None,
            controllers: controllers
                .iter()
                .map(|c| super::super::words(*c))
                .collect(),
            summary: None,
        }
    }

    fn named(references: &[Reference]) -> Vec<&str> {
        references.iter().map(|r| r.text.as_str()).collect()
    }

    #[rstest]
    #[case::shared(vec![vec!["operator"], vec!["operator"]], vec!["operator"], vec![vec![], vec![]])]
    #[case::not_shared(
        vec![vec!["operator"], vec!["other"]],
        vec![],
        vec![vec!["operator"], vec!["other"]]
    )]
    #[case::none(vec![vec![], vec![]], vec![], vec![vec![], vec![]])]
    fn names_a_controller_every_kind_shares_once_on_the_group(
        #[case] controllers: Vec<Vec<&str>>,
        #[case] group: Vec<&str>,
        #[case] rows: Vec<Vec<&str>>,
    ) {
        let kinds = controllers.iter().map(|c| row("Gizmos", c)).collect();
        let found = ApiGroup::new("gizmos.example.test".into(), kinds);
        let found_rows: Vec<Vec<&str>> =
            found.kinds.iter().map(|k| named(&k.controllers)).collect();
        assert_eq!((named(&found.controllers), found_rows), (group, rows));
    }

    #[rstest]
    #[case::healthy(3, 0, "3 objects")]
    #[case::one(1, 0, "1 object")]
    #[case::some_not_ready(5, 2, "5 objects, 2 not ready")]
    fn counts_a_kinds_objects_on_its_card(
        #[case] count: usize,
        #[case] unhealthy: usize,
        #[case] text: &str,
    ) {
        let card = KindCard {
            label: "Gizmos".into(),
            group: "gizmos.example.test".into(),
            count,
            unhealthy,
            worst: Some(Level::Warning),
            why: None,
            controllers: Vec::new(),
            rule: None,
        };
        assert_eq!(card.status_text(), text);
    }
}
