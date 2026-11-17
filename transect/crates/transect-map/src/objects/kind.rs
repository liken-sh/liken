//! The custom kinds: `/kinds`, every one the cluster serves in a box
//! per API group, and `/kinds/{plural.group}`, one kind's objects across
//! namespaces with their health and printer columns, beside the kind's
//! card.

use askama::Template;

use super::Item;
use crate::map::KnownEntry as Entry;
use transect_frame::frame::crumbs::Crumb;
use transect_graph::edges::provenance::legend;
use transect_graph::links::Reference;
use transect_observe::health::Level;
use transect_resources::intent::Line;

/// The page that lists every custom kind.
#[derive(Template)]
#[template(path = "objects/kinds.html", blocks = ["main"])]
pub struct KindsPage {
    /// The kinds that have objects, by API group.
    pub groups: Vec<ApiGroup>,
    /// The kinds that have none, by API group, folded into one box.
    pub empty: Vec<ApiGroup>,
}

/// One API group's kinds, and the workloads that control them when
/// every kind in it shares them, which its box names once.
#[derive(Debug, PartialEq)]
pub struct ApiGroup {
    pub name: String,
    pub kinds: Vec<KindRow>,
    pub controllers: Vec<Reference>,
}

/// One kind: its plural, its page, how many objects it has and how
/// many of those report a condition that is not healthy, the workloads
/// that control them, and the first sentence of its CRD's description.
#[derive(Debug, PartialEq)]
pub struct KindRow {
    pub label: String,
    pub href: String,
    pub count: usize,
    pub unhealthy: usize,
    /// The worst level among its objects, which colors the dot beside
    /// the count of unhealthy ones.
    pub worst: Option<Level>,
    pub controllers: Vec<Reference>,
    pub summary: Option<String>,
}

impl KindRow {
    /// Its plural inside its group's box.  The box names the group, so a
    /// label that names the group to tell it from another's drops it.
    pub fn label_in(&self, group: &str) -> &str {
        let suffix = format!(" ({group})");
        self.label
            .strip_suffix(suffix.as_str())
            .unwrap_or(&self.label)
    }

    /// "2 not ready", with the dot's class, when any object is not.
    pub fn unhealthy_text(&self) -> Option<(String, &'static str)> {
        let worst = self.worst.filter(|_| self.unhealthy > 0)?;
        Some((
            format!("{} not ready", self.unhealthy),
            super::dot_class(worst),
        ))
    }
}

impl ApiGroup {
    /// The group with the controllers every kind in it shares lifted to
    /// the group, so its box names them once rather than on each row.
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

/// The domain an API group is registered under, its last two labels:
/// `fluxcd.io` for `source.toolkit.fluxcd.io`.  The groups sort by it
/// first, read right to left.
pub fn domain(group: &str) -> &str {
    let mut dots = group.rmatch_indices('.');
    match (dots.next(), dots.next()) {
        (Some(_), Some((at, _))) => &group[at + 1..],
        _ => group,
    }
}

impl KindsPage {
    /// The small heading before the group at `at`: its domain, when the
    /// group starts the domain's run of groups, so the reversed-DNS order
    /// shows.  A domain whose one group is the domain itself gets none.
    pub fn heading(&self, at: &usize) -> Option<&str> {
        let at = *at;
        let name = &self.groups.get(at)?.name;
        let this = domain(name);
        let starts = at == 0 || domain(&self.groups[at - 1].name) != this;
        let groups = self.groups.iter().filter(|g| domain(&g.name) == this);
        let alone = groups.count() == 1 && name == this;
        (starts && !alone).then_some(this)
    }

    /// How many kinds have no objects.
    pub fn empty_count(&self) -> usize {
        self.empty.iter().map(|group| group.kinds.len()).sum()
    }

    /// How many kinds have objects, and how many objects they have.
    pub fn totals(&self) -> (usize, usize) {
        let kinds = self.groups.iter().flat_map(|group| &group.kinds);
        kinds.fold((0, 0), |(kinds, objects), kind| {
            (kinds + 1, objects + kind.count)
        })
    }

    /// The kinds with an object that is not healthy, for the card.
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

/// One kind's objects in every namespace.
#[derive(Template)]
#[template(path = "objects/kind.html", blocks = ["main"])]
pub struct KindPage {
    pub label: String,
    pub path: String,
    /// The other kinds of its API group, this one among them.
    pub strip: Vec<Entry>,
    /// The printer columns' names, which head the table after the name
    /// and the namespace.
    pub columns: Vec<String>,
    pub rows: Vec<KindObject>,
    /// What each report scanned, for a kind of findings reports.
    pub scans: Vec<transect_supply::vulnerabilities::Scan>,
    pub card: KindCard,
}

/// One object of the kind, with each printer column's value.
#[derive(Debug, PartialEq)]
pub struct KindObject {
    pub item: Item,
    pub namespace: Option<String>,
    /// Its deciding condition in words, "Ready: issuing", or none when
    /// it reports none.
    pub condition: Option<String>,
    pub values: Vec<String>,
}

/// A kind's card: its CRD's description, how many objects it has and
/// how they are, and the workloads that control them.
#[derive(Debug, PartialEq)]
pub struct KindCard {
    pub label: String,
    pub group: String,
    pub count: usize,
    pub unhealthy: usize,
    pub worst: Option<Level>,
    pub why: Option<Line>,
    pub controllers: Vec<Reference>,
    /// How the controllers were found, in words.
    pub rule: Option<String>,
}

impl KindCard {
    /// Beside the dot: "3 objects", and how many are not ready.
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

/// Whether a table of objects with this many columns needs more than
/// the column beside the card under 1400px.  Past four columns, the
/// card drops below the table, so each name keeps more of its line.
pub fn wide_table(columns: usize) -> bool {
    columns > 4
}

impl KindPage {
    /// Its table has the name, the namespace, and each printer column.
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

/// One kind of the custom resources a workload's controller
/// reconciles: the kind's plural and page, its printer columns, and a
/// row for each object.
#[derive(Debug, PartialEq)]
pub struct Manages {
    pub label: String,
    pub href: String,
    /// Which rule of controller attribution matched, in words.
    pub rule: String,
    pub columns: Vec<String>,
    pub rows: Vec<KindObject>,
}

/// The first sentence of `text`, which a kind's title on `/kinds`
/// shows on hover.
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
                "media.liken.sh",
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
