//! The outside-systems band of a page: one box for each backend outside the
//! cluster, with its volumes counted by type, and one box for each
//! address outside the cluster that a depends-on edge reaches, with the
//! number of objects that depend on it.  An address that is both gets
//! one box with both counts.  When a namespace is selected, each box
//! also shows that namespace's own counts.

use std::collections::{BTreeMap, BTreeSet};

use super::{Storage, Type};
use transect_graph::edges::{Endpoint, Graph, Kind};
use transect_resources::intent::Line;

/// Volumes counted by type, the most numerous type first.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Counts(pub Vec<(Type, usize)>);

impl Counts {
    fn of(kinds: &BTreeMap<&Type, usize>) -> Self {
        let mut counts: Vec<(Type, usize)> = kinds
            .iter()
            .map(|(&kind, &count)| (kind.clone(), count))
            .collect();
        counts.sort_by(|(a_kind, a), (b_kind, b)| b.cmp(a).then_with(|| a_kind.cmp(b_kind)));
        Self(counts)
    }

    pub fn total(&self) -> usize {
        self.0.iter().map(|(_, count)| count).sum()
    }

    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }
}

/// What a box counts, for the whole cluster or for one namespace.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Totals {
    pub counts: Counts,
    /// How many objects depend on the box, by their depends-on edges.
    pub dependents: usize,
}

impl Totals {
    pub fn is_empty(&self) -> bool {
        self.counts.is_empty() && self.dependents == 0
    }
}

#[derive(Debug, PartialEq)]
pub struct GroundBox {
    /// The address or driver that names the backend.
    pub name: String,
    pub all: Totals,
    /// The selected namespace's counts, or `None` when no namespace is
    /// selected.
    pub here: Option<Totals>,
    /// The intent lines about the backend or address, such as an
    /// `Outside` resource's `why` and `gotcha`.
    pub about: Vec<Line>,
    /// The path of the box's own page, under `/ground/`.
    pub page: String,
    /// The URL of the backend's console: its `Outside`'s `url`, or else
    /// its drivers' `console` URL when they have exactly one.
    pub link: Option<String>,
}

#[derive(Debug, PartialEq)]
pub struct Ground {
    pub boxes: Vec<GroundBox>,
    /// The selected namespace. The page writes its name before its
    /// counts on each box.
    pub selected: Option<String>,
}

impl Ground {
    /// A box for each backend outside the cluster and each address
    /// outside it that `graph`'s depends-on edges reach, with the most
    /// volumes first, then the most dependents, then by name.  A
    /// selected namespace adds its own counts to each box but does not
    /// change the order.  `stores_on` is whether the page shows
    /// stores-on edges.  When it is false, the selected namespace's
    /// volume counts are zero, because those edges draw its volumes.
    pub fn build(
        storage: &Storage,
        graph: &Graph,
        selected: Option<&str>,
        stores_on: bool,
    ) -> Self {
        let tally = storage.tally(None);
        let here_tally = selected
            .filter(|_| stores_on)
            .map(|namespace| storage.tally(Some(namespace)))
            .unwrap_or_default();
        let dependents = dependents(graph);
        let names: BTreeSet<&str> = tally
            .keys()
            .copied()
            .chain(dependents.keys().map(String::as_str))
            .collect();
        let counts = |tally: &BTreeMap<&str, BTreeMap<&Type, usize>>, name: &str| {
            tally
                .get(name)
                .map_or_else(Counts::default, |kinds| Counts::of(kinds))
        };
        let mut boxes: Vec<GroundBox> = names
            .into_iter()
            .map(|name| {
                let on = dependents.get(name);
                let all = Totals {
                    counts: counts(&tally, name),
                    dependents: on.map_or(0, BTreeSet::len),
                };
                let here = selected.map(|namespace| Totals {
                    counts: counts(&here_tally, name),
                    dependents: on.map_or(0, |on| {
                        on.iter().filter(|end| end.in_namespace(namespace)).count()
                    }),
                });
                GroundBox {
                    name: name.into(),
                    all,
                    here,
                    about: Vec::new(),
                    page: format!("/ground/{name}"),
                    link: None,
                }
            })
            .collect();
        boxes.sort_by(|a, b| {
            let (a_all, b_all) = (&a.all, &b.all);
            b_all
                .counts
                .total()
                .cmp(&a_all.counts.total())
                .then_with(|| b_all.dependents.cmp(&a_all.dependents))
                .then_with(|| a.name.cmp(&b.name))
        });
        Self {
            boxes,
            selected: selected.map(Into::into),
        }
    }

    pub fn with_about(mut self, about: impl Fn(&str) -> Vec<Line>) -> Self {
        for one in &mut self.boxes {
            one.about = about(&one.name);
        }
        self
    }

    pub fn with_pages(mut self, pages: impl Fn(&str) -> (String, Option<String>)) -> Self {
        for one in &mut self.boxes {
            (one.page, one.link) = pages(&one.name);
        }
        self
    }

    /// The number of volumes outside the cluster.
    pub fn total(&self) -> usize {
        self.boxes.iter().map(|one| one.all.counts.total()).sum()
    }

    /// The number of boxes that at least one object depends on.
    pub fn depended_on(&self) -> usize {
        self.boxes
            .iter()
            .filter(|one| one.all.dependents > 0)
            .count()
    }
}

/// The objects whose depends-on edges reach each address outside the
/// cluster, by the address.
fn dependents(graph: &Graph) -> BTreeMap<String, BTreeSet<&Endpoint>> {
    let mut dependents: BTreeMap<String, BTreeSet<&Endpoint>> = BTreeMap::new();
    for edge in graph.of_kind(Kind::DependsOn) {
        if let Endpoint::Outside(name) = &edge.to {
            dependents
                .entry(name.clone())
                .or_default()
                .insert(&edge.from);
        }
    }
    dependents
}

impl GroundBox {
    /// The box's CSS classes.  When a namespace is selected, a box that
    /// has counts for it gets `ground-box-here`, and a box with none
    /// gets `ground-box-faded`.
    pub fn class(&self) -> &'static str {
        match &self.here {
            None => "ground-box",
            Some(here) if here.is_empty() => "ground-box ground-box-faded",
            Some(_) => "ground-box ground-box-here",
        }
    }
}

/// Each backend that stores `namespace`'s volumes, with the namespace's
/// counts there, the backend with the most volumes first.  The
/// namespace's page shows them as one line.
pub fn under(storage: &Storage, namespace: &str) -> Vec<(String, Counts)> {
    let mut under: Vec<(String, Counts)> = storage
        .tally(Some(namespace))
        .iter()
        .map(|(&name, kinds)| (name.to_string(), Counts::of(kinds)))
        .collect();
    under.sort_by(|(a_name, a), (b_name, b)| {
        b.total().cmp(&a.total()).then_with(|| a_name.cmp(b_name))
    });
    under
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::storage::fixtures::storage;
    use transect_graph::edges::{Edge, Object};

    fn fast(count: usize) -> Counts {
        Counts(vec![(Type::Class("fast".into()), count)])
    }

    fn nfs(count: usize) -> Counts {
        Counts(vec![(Type::Nfs, count)])
    }

    fn totals(counts: Counts, dependents: usize) -> Totals {
        Totals { counts, dependents }
    }

    fn ground_box(name: &str, all: Totals, here: Option<Totals>) -> GroundBox {
        GroundBox {
            name: name.into(),
            all,
            here,
            about: Vec::new(),
            page: format!("/ground/{name}"),
            link: None,
        }
    }

    fn depends_on(namespace: &str, name: &str, outside: &str) -> Edge {
        let from = Object::new("apps", "Deployment", Some(namespace), name);
        Edge {
            kind: Kind::DependsOn,
            from: Endpoint::Object(from),
            to: Endpoint::Outside(outside.into()),
            guessed: false,
            provenance: Default::default(),
        }
    }

    /// Two workloads in shop and one in blog that depend on an API
    /// outside the cluster, and one workload in shop that depends on the
    /// NFS server that it also stores volumes on.
    fn graph() -> Graph {
        Graph::new([vec![
            depends_on("shop", "web", "api.example.com"),
            depends_on("shop", "worker", "api.example.com"),
            depends_on("blog", "posts", "api.example.com"),
            depends_on("shop", "web", "nas.example.com"),
        ]])
    }

    #[rstest]
    #[case::nothing_selected(None, true, vec![
        ground_box("nas.example.com", totals(nfs(3), 1), None),
        ground_box("csi.example.com", totals(fast(2), 0), None),
        ground_box("api.example.com", totals(Counts::default(), 3), None),
    ])]
    #[case::shop_has_counts_on_every_box(Some("shop"), true, vec![
        ground_box("nas.example.com", totals(nfs(3), 1), Some(totals(nfs(2), 1))),
        ground_box("csi.example.com", totals(fast(2), 0), Some(totals(fast(1), 0))),
        ground_box("api.example.com", totals(Counts::default(), 3), Some(totals(Counts::default(), 2))),
    ])]
    #[case::blog_has_counts_on_two_boxes(Some("blog"), true, vec![
        ground_box("nas.example.com", totals(nfs(3), 1), Some(totals(Counts::default(), 0))),
        ground_box("csi.example.com", totals(fast(2), 0), Some(totals(fast(1), 0))),
        ground_box("api.example.com", totals(Counts::default(), 3), Some(totals(Counts::default(), 1))),
    ])]
    #[case::stores_on_hidden(Some("blog"), false, vec![
        ground_box("nas.example.com", totals(nfs(3), 1), Some(totals(Counts::default(), 0))),
        ground_box("csi.example.com", totals(fast(2), 0), Some(totals(Counts::default(), 0))),
        ground_box("api.example.com", totals(Counts::default(), 3), Some(totals(Counts::default(), 1))),
    ])]
    fn draws_a_box_per_backend_and_dependency_with_the_selected_namespaces_counts(
        #[case] selected: Option<&str>,
        #[case] stores_on: bool,
        #[case] boxes: Vec<GroundBox>,
    ) {
        let ground = Ground::build(&storage(), &graph(), selected, stores_on);
        assert_eq!(ground.boxes, boxes);
        assert_eq!((ground.total(), ground.depended_on()), (5, 2));
    }

    #[rstest]
    fn counts_the_most_numerous_type_first() {
        let kinds = BTreeMap::from([(&Type::Nfs, 2), (&Type::Iscsi, 6), (&Type::Local, 2)]);
        assert_eq!(
            Counts::of(&kinds),
            Counts(vec![(Type::Iscsi, 6), (Type::Nfs, 2), (Type::Local, 2)])
        );
    }

    #[rstest]
    #[case::nothing_selected(None, "ground-box")]
    #[case::volumes_here(Some(totals(nfs(1), 0)), "ground-box ground-box-here")]
    #[case::dependents_here(Some(totals(Counts::default(), 1)), "ground-box ground-box-here")]
    #[case::not_here(Some(Totals::default()), "ground-box ground-box-faded")]
    fn a_box_marks_the_selection(#[case] here: Option<Totals>, #[case] class: &str) {
        let one = ground_box("nas.example.com", totals(nfs(1), 1), here);
        assert_eq!(one.class(), class);
    }

    #[rstest]
    #[case::two_backends("shop", vec![("nas.example.com".into(), nfs(2)), ("csi.example.com".into(), fast(1))])]
    #[case::none("wiki", vec![])]
    fn lists_the_backends_that_store_a_namespace(
        #[case] namespace: &str,
        #[case] expected: Vec<(String, Counts)>,
    ) {
        assert_eq!(under(&storage(), namespace), expected);
    }
}
