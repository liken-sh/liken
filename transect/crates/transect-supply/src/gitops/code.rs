//! Where an object is declared in git, for the link to code on its
//! card.  transect finds it by following the object's reconciles-from
//! edges to an applier, and reports the directory the applier applies
//! and whether the applier is ready.

use super::forge::{self, Place};
use super::{Found, GitOps, Ready};
use crate::forge::Forges;
use transect_graph::edges::{Endpoint, Graph, Kind, Object};

/// Where one object is declared.
#[derive(Clone, Debug, PartialEq)]
pub struct Code {
    /// The applier that installed the object from a chart, if a chart
    /// installed it.
    pub release: Option<Release>,
    /// The applier that applied the object from a directory, or applied
    /// the release that installed it.
    pub declared: Option<Declared>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Release {
    pub object: Object,
    /// The address of the release's page, when the card links to it.
    pub href: Option<String>,
    pub chart: Option<String>,
    /// The URL of the repository the chart comes from.
    pub repository: Option<String>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Declared {
    pub applier: Object,
    /// The address of the applier's page, when the card links to it.
    pub href: Option<String>,
    pub ready: Ready,
    /// The directory that the applier applies in its repository, or
    /// `None` when transect cannot read where the applier fetches from.
    pub place: Option<Place>,
    /// The repository's URL, which the card compares with the
    /// repository that a running image was built from.
    pub repository: Option<String>,
    /// Whether the source label of a running image names that
    /// repository.
    pub built_here: bool,
}

impl Code {
    /// Where `end` is declared, found by following its reconciles-from
    /// edges in `graph` to the applier of a directory, or to a release
    /// and the applier that applied the release.  It is `None` when no
    /// edge from `end` leads to an applier.
    pub fn of(graph: &Graph, gitops: &GitOps, forges: &Forges, end: &Endpoint) -> Option<Self> {
        let by = applier(graph, gitops, end, |_| true)?;
        let (release, declaring) = match gitops.installs_a_chart(by) {
            true => {
                let found = gitops.found(by)?;
                let release = Release {
                    object: by.clone(),
                    href: None,
                    chart: found.chart.clone(),
                    repository: gitops.url_of(found),
                };
                let end = Endpoint::Object(by.clone());
                let declaring = applier(graph, gitops, &end, |found| {
                    !gitops.installs_a_chart(&found.object)
                });
                (Some(release), declaring)
            }
            false => (None, Some(by)),
        };
        let declared = declaring.and_then(|object| gitops.found(object));
        let declared = declared.map(|found| {
            let fetched = gitops.fetches_from(found);
            let directory = found.directory.as_deref().unwrap_or_default();
            Declared {
                applier: found.object.clone(),
                href: None,
                ready: found.ready.clone(),
                place: fetched
                    .map(|(url, reference)| forge::place(url, reference, directory, forges)),
                repository: fetched.map(|(url, _)| url.to_string()),
                built_here: false,
            }
        });
        Some(Self { release, declared })
    }
}

impl GitOps {
    /// Whether `object` installs a chart instead of applying a
    /// directory: its kind's knowledge reads a chart, and either reads
    /// no directory or finds none on the object.
    pub fn installs_a_chart(&self, object: &Object) -> bool {
        let Some(fields) = self.fields_of(object) else {
            return false;
        };
        let directory = self
            .found(object)
            .and_then(|found| found.directory.as_ref());
        fields.chart.is_some() && (fields.directory.is_none() || directory.is_none())
    }

    /// The repository that `found` fetches from, and the ref it follows.
    /// They are its own, or its source's when it has no URL of its own.
    pub fn fetches_from<'a>(&'a self, found: &'a Found) -> Option<(&'a str, &'a super::GitRef)> {
        if let Some(url) = &found.url {
            return Some((url, &found.reference));
        }
        let source = self.found(&self.source_of(found)?)?;
        Some((source.url.as_deref()?, &source.reference))
    }
}

/// The first object that `end` reconciles from that is an applier and
/// that `wanted` accepts.  An applier is an object whose kind's
/// knowledge has `applies`.
fn applier<'a>(
    graph: &'a Graph,
    gitops: &GitOps,
    end: &Endpoint,
    wanted: impl Fn(&Found) -> bool,
) -> Option<&'a Object> {
    let applies = |object: &Object| {
        let fields = gitops.fields_of(object);
        let found = gitops.found(object);
        fields.is_some_and(|fields| fields.applies.is_some()) && found.is_some_and(&wanted)
    };
    let mut ends = graph.leaving(Kind::ReconcilesFrom, end).into_iter();
    ends.find_map(|end| match end {
        Endpoint::Object(object) if applies(object) => Some(object),
        _ => None,
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::Applied;
    use super::super::fixtures::{gitops, object};
    use super::*;
    use transect_graph::edges::Edge;

    /// A repo on a known forge in `ops`, a tree `shop` that applies its
    /// `./shop` directory and is ready, a chart repo in `shop`, and a
    /// release `cache` that `shop` applies.
    fn tool() -> GitOps {
        gitops(&[
            (
                "Repo",
                "ops",
                "fleet",
                "",
                "spec: {url: 'https://github.com/acme/fleet', ref: {branch: main}}",
            ),
            (
                "Repo",
                "shop",
                "charts",
                "",
                "spec: {url: 'https://charts.example.com'}",
            ),
            (
                "Tree",
                "ops",
                "shop",
                "",
                "spec: {path: ./shop, sourceRef: {kind: Repo, name: fleet}}\nstatus: {conditions: [{type: Ready, status: 'True'}]}",
            ),
            (
                "Release",
                "shop",
                "cache",
                "tree/name: shop, tree/namespace: ops",
                "spec: {chart: cache, sourceRef: {kind: Repo, name: charts}}",
            ),
        ])
    }

    fn reconciles(from: &Object, to: &Object) -> Edge {
        Edge {
            kind: Kind::ReconcilesFrom,
            from: Endpoint::Object(from.clone()),
            to: Endpoint::Object(to.clone()),
            guessed: false,
            provenance: transect_graph::edges::Provenance::Kubernetes,
        }
    }

    fn deployment(name: &str) -> Object {
        Object::new("apps", "Deployment", Some("shop"), name)
    }

    /// The tool's graph, with `web` applied by `shop` and the
    /// `Deployment` `cache` installed by the release.
    fn graph(gitops: &GitOps) -> Graph {
        let mut edges = gitops.edges(&Applied::default());
        edges.extend([
            reconciles(&deployment("web"), &object("Tree", "ops", "shop")),
            reconciles(&deployment("cache"), &object("Release", "shop", "cache")),
        ]);
        Graph::new([edges])
    }

    fn declared() -> Option<Declared> {
        Some(Declared {
            applier: object("Tree", "ops", "shop"),
            href: None,
            ready: Ready::True,
            place: Some(Place::Link {
                href: "https://github.com/acme/fleet/tree/main/shop".into(),
                text: "fleet/shop".into(),
            }),
            repository: Some("https://github.com/acme/fleet".into()),
            built_here: false,
        })
    }

    #[rstest]
    #[case::applied_by_a_tree("web", Some(Code { release: None, declared: declared() }))]
    #[case::installed_by_a_chart(
        "cache",
        Some(Code {
            release: Some(Release {
                object: object("Release", "shop", "cache"),
                href: None,
                chart: Some("cache".into()),
                repository: Some("https://charts.example.com".into()),
            }),
            declared: declared(),
        })
    )]
    #[case::applied_by_hand("debug", None)]
    fn finds_where_an_object_is_declared(#[case] name: &str, #[case] expected: Option<Code>) {
        let gitops = tool();
        let end = Endpoint::Object(deployment(name));
        let found = Code::of(
            &graph(&gitops),
            &gitops,
            &Forges::build(&crate::forge::fixtures::layouts(), []),
            &end,
        );
        assert_eq!(found, expected);
    }
}
