//! A card's "depends on" and "depended on by" sections, read from the
//! graph's depends-on edges: what the object's configured addresses and
//! intent reach, and what reaches the object. The card also lists the
//! intent entries that transect cannot resolve to anything. The
//! authenticates-through edges give the identity providers that the
//! object signs its users in through, and the applications that sign
//! in through it.

use std::collections::BTreeSet;

use super::Across;
use super::stated::Unread;
use transect_graph::edges::{Edge, Endpoint, Graph, Kind, Object, Plurals, Provenance};
use transect_graph::links::{Named, Reference};

#[derive(Debug, Default, PartialEq)]
pub struct Depends {
    pub on: Vec<End>,
    pub by: Vec<End>,
    /// The identity providers the object signs its users in through.
    pub through: Vec<End>,
    /// The applications that sign their users in through the object.
    pub signs_in: Vec<End>,
    /// The entries of the object's intent that resolve to nothing.
    pub unread: Vec<Unread>,
}

/// One end a card lists, with a mark when only intent or kind knowledge
/// supplies it.
#[derive(Debug, PartialEq)]
pub struct End {
    pub reference: Reference,
    pub mark: Option<Provenance>,
}

fn marks(ends: &[End]) -> impl Iterator<Item = Option<&Provenance>> {
    ends.iter().map(|end| end.mark.as_ref())
}

impl Depends {
    /// The ends that `workload`'s depends-on and authenticates-through
    /// edges reach, and the ends whose edges reach it, each sorted by
    /// displayed text, plus `unread`. An end links to its page only when
    /// `has_page` returns true for it, because a custom resource that
    /// runs nothing and has no kind knowledge has no page.
    pub fn of_workload(
        graph: &Graph,
        workload: &Object,
        plurals: &Plurals,
        unread: &[Unread],
        has_page: impl Fn(&Object) -> bool,
    ) -> Self {
        let end = Endpoint::Object(workload.clone());
        let namespace = workload.namespace.as_deref();
        let refer = |pairs: Vec<(&Endpoint, Vec<&Edge>)>| -> Vec<End> {
            let mut ends: Vec<End> = pairs
                .into_iter()
                .map(|(end, edges)| {
                    let mut reference = Reference::to_end(end, namespace, plurals);
                    if let Endpoint::Object(object) = end
                        && !has_page(object)
                    {
                        reference.href = None;
                    }
                    End {
                        reference,
                        mark: stated(&edges),
                    }
                })
                .collect();
            ends.sort_by(|a, b| a.reference.text.cmp(&b.reference.text));
            ends
        };
        let leaving = |kind: Kind| {
            let ends = graph.leaving(kind, &end).into_iter().map(|to| {
                let edges = graph.edges_leaving(kind, &end);
                (to, edges.filter(|edge| &edge.to == to).collect())
            });
            refer(ends.collect())
        };
        let reaching = |kind: Kind| {
            let ends = graph.reaching(kind, &end).into_iter().map(|from| {
                let edges = graph.edges_reaching(kind, &end);
                (from, edges.filter(|edge| &edge.from == from).collect())
            });
            refer(ends.collect())
        };
        Self {
            on: leaving(Kind::DependsOn),
            by: reaching(Kind::DependsOn),
            through: leaving(Kind::AuthenticatesThrough),
            signs_in: reaching(Kind::AuthenticatesThrough),
            unread: unread.to_vec(),
        }
    }

    /// The unread entries of the `kind` list.
    fn unread_of(&self, kind: Kind) -> Vec<&Unread> {
        self.unread
            .iter()
            .filter(|unread| unread.kind == kind)
            .collect()
    }

    pub fn unread_on(&self) -> Vec<&Unread> {
        self.unread_of(Kind::DependsOn)
    }

    pub fn unread_through(&self) -> Vec<&Unread> {
        self.unread_of(Kind::AuthenticatesThrough)
    }

    /// No entries, for a section that lists what reaches the object.
    /// The object's own intent never names those.
    pub fn unread_none(&self) -> Vec<&Unread> {
        Vec::new()
    }

    /// The mark that a section's heading shows in place of each line's
    /// mark, when two or more lines share it. The sections are "Depends
    /// on", "Depended on by", "Authenticates through", and "Signs in
    /// users of".
    pub fn shared_on(&self) -> Option<Provenance> {
        let unread = self.unread_on();
        let unread = unread.iter().map(|entry| Some(&entry.provenance));
        transect_graph::edges::provenance::shared(marks(&self.on).chain(unread))
    }

    pub fn shared_by(&self) -> Option<Provenance> {
        transect_graph::edges::provenance::shared(marks(&self.by))
    }

    pub fn shared_through(&self) -> Option<Provenance> {
        let unread = self.unread_through();
        let unread = unread.iter().map(|entry| Some(&entry.provenance));
        transect_graph::edges::provenance::shared(marks(&self.through).chain(unread))
    }

    pub fn shared_signs_in(&self) -> Option<Provenance> {
        transect_graph::edges::provenance::shared(marks(&self.signs_in))
    }

    /// The mark that a section's marked lines share when the section
    /// also has unmarked lines, so the mark shows once over the group.
    pub fn grouped_on(&self) -> Option<Provenance> {
        transect_graph::edges::provenance::grouped(marks(&self.on))
    }

    pub fn grouped_by(&self) -> Option<Provenance> {
        transect_graph::edges::provenance::grouped(marks(&self.by))
    }

    pub fn grouped_through(&self) -> Option<Provenance> {
        transect_graph::edges::provenance::grouped(marks(&self.through))
    }

    pub fn grouped_signs_in(&self) -> Option<Provenance> {
        transect_graph::edges::provenance::grouped(marks(&self.signs_in))
    }

    /// The provenance of every mark in the card's lists.
    pub fn provenances(&self) -> impl Iterator<Item = &Provenance> {
        let ends = self.on.iter().chain(&self.by);
        let ends = ends.chain(&self.through).chain(&self.signs_in);
        let marks = ends.filter_map(|end| end.mark.as_ref());
        marks.chain(self.unread.iter().map(|unread| &unread.provenance))
    }

    /// The namespaces and machines that a namespace's workloads depend
    /// on from outside it, and the namespaces that depend on it.
    pub fn of_namespace(across: &Across) -> Self {
        let namespaces = |names: &BTreeSet<String>| {
            names
                .iter()
                .map(|name| Reference::to_namespace(name))
                .collect()
        };
        let mut on: Vec<Reference> = namespaces(&across.on);
        on.extend(across.machines.iter().map(|machine| Reference {
            text: Named::Machine(machine).to_string(),
            href: None,
        }));
        let unmarked = |references: Vec<Reference>| {
            let ends = references.into_iter();
            ends.map(|reference| End {
                reference,
                mark: None,
            })
            .collect()
        };
        Self {
            on: unmarked(on),
            by: unmarked(namespaces(&across.by)),
            ..Self::default()
        }
    }

    pub fn is_empty(&self) -> bool {
        let ends = [&self.on, &self.by, &self.through, &self.signs_in];
        ends.iter().all(|ends| ends.is_empty()) && self.unread.is_empty()
    }
}

/// The mark of an end that only intent or kind knowledge supplies. The
/// mark comes from the first of its edges. An end that the Kubernetes
/// API also supplies is a fact of the cluster, so it has no mark.
fn stated(edges: &[&Edge]) -> Option<Provenance> {
    let marked = |edge: &&&Edge| edge.provenance.mark().is_some();
    match edges.iter().all(|edge| marked(&edge)) {
        true => edges.first().map(|edge| edge.provenance.clone()),
        false => None,
    }
}

impl Unread {
    /// "names the billing API, which transect cannot read".
    pub fn text(&self) -> String {
        format!("names {}, which transect cannot read", self.entry)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_graph::edges::Edge;

    fn deployment(namespace: &str, name: &str) -> Endpoint {
        Endpoint::Object(Object::new("apps", "Deployment", Some(namespace), name))
    }

    fn depends(from: Endpoint, to: Endpoint) -> Edge {
        Edge {
            kind: Kind::DependsOn,
            from,
            to,
            guessed: false,
            provenance: transect_graph::edges::Provenance::Kubernetes,
        }
    }

    fn reference(text: &str, href: Option<&str>) -> End {
        End {
            reference: Reference {
                text: text.into(),
                href: href.map(Into::into),
            },
            mark: None,
        }
    }

    #[rstest]
    fn a_workload_names_both_directions() {
        let graph = Graph::new([vec![
            depends(deployment("shop", "web"), deployment("shop", "db")),
            depends(deployment("shop", "web"), deployment("auth", "login")),
            depends(deployment("shop", "web"), Endpoint::Machine("alpha".into())),
            depends(deployment("blog", "feed"), deployment("shop", "web")),
        ]]);
        let web = Object::new("apps", "Deployment", Some("shop"), "web");
        let found = Depends::of_workload(&graph, &web, &Plurals::default(), &[], |_| true);
        assert_eq!(
            found,
            Depends {
                on: vec![
                    reference(
                        "Deployment auth/login",
                        Some("/namespaces/auth/deployments/login")
                    ),
                    reference("Deployment db", Some("/namespaces/shop/deployments/db")),
                    reference("machine alpha", Some("/machines/alpha")),
                ],
                by: vec![reference(
                    "Deployment blog/feed",
                    Some("/namespaces/blog/deployments/feed")
                )],
                ..Depends::default()
            }
        );
    }

    #[rstest]
    fn names_an_end_with_no_page_without_a_link() {
        let plurals = Plurals::from([(
            ("widgets.example.com".to_string(), "Widget".to_string()),
            "widgets".to_string(),
        )]);
        let widget = Object::new("widgets.example.com", "Widget", Some("shop"), "idle");
        let graph = Graph::new([vec![
            depends(Endpoint::Object(widget.clone()), deployment("shop", "web")),
            depends(deployment("blog", "feed"), deployment("shop", "web")),
        ]]);
        let web = Object::new("apps", "Deployment", Some("shop"), "web");
        let found = Depends::of_workload(&graph, &web, &plurals, &[], |o| o != &widget);
        assert_eq!(
            found.by,
            vec![
                reference(
                    "Deployment blog/feed",
                    Some("/namespaces/blog/deployments/feed")
                ),
                reference("Widget idle", None),
            ]
        );
    }

    fn intent() -> Provenance {
        Provenance::Intent(transect_graph::edges::Stated::Annotation {
            field: "depends-on".into(),
            on: "Deployment web".into(),
        })
    }

    #[rstest]
    #[case::only_intent(vec![intent()], Some(intent()))]
    #[case::the_api_too(vec![intent(), Provenance::Kubernetes], None)]
    #[case::only_the_api(vec![Provenance::Kubernetes], None)]
    fn marks_an_end_only_intent_gives(
        #[case] provenances: Vec<Provenance>,
        #[case] mark: Option<Provenance>,
    ) {
        let edges = provenances.into_iter().map(|provenance| Edge {
            provenance,
            ..depends(deployment("shop", "web"), deployment("shop", "db"))
        });
        let graph = Graph::new([edges.collect()]);
        let web = Object::new("apps", "Deployment", Some("shop"), "web");
        let found = Depends::of_workload(&graph, &web, &Plurals::default(), &[], |_| true);
        assert_eq!(found.on[0].mark, mark);
    }

    #[rstest]
    fn a_namespace_names_namespaces_then_machines() {
        let set = |names: &[&str]| names.iter().map(|n| n.to_string()).collect();
        let across = Across {
            on: set(&["auth"]),
            machines: set(&["alpha"]),
            by: set(&["blog"]),
        };
        assert_eq!(
            Depends::of_namespace(&across),
            Depends {
                on: vec![
                    reference("auth", Some("/namespaces/auth")),
                    reference("machine alpha", None),
                ],
                by: vec![reference("blog", Some("/namespaces/blog"))],
                ..Depends::default()
            }
        );
    }

    #[rstest]
    fn a_workload_names_its_identity_provider_and_who_signs_in_through_it() {
        let through = |from, to| Edge {
            kind: Kind::AuthenticatesThrough,
            provenance: intent(),
            ..depends(from, to)
        };
        let graph = Graph::new([vec![
            through(deployment("shop", "web"), deployment("auth", "login")),
            through(deployment("blog", "feed"), deployment("shop", "web")),
        ]]);
        let web = Object::new("apps", "Deployment", Some("shop"), "web");
        let found = Depends::of_workload(&graph, &web, &Plurals::default(), &[], |_| true);
        let marked = |text: &str, href: &str| End {
            mark: Some(intent()),
            ..reference(text, Some(href))
        };
        assert_eq!(
            (found.through, found.signs_in),
            (
                vec![marked(
                    "Deployment auth/login",
                    "/namespaces/auth/deployments/login"
                )],
                vec![marked(
                    "Deployment blog/feed",
                    "/namespaces/blog/deployments/feed"
                )],
            )
        );
    }
}
