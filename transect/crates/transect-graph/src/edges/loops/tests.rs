use rstest::rstest;

use super::fixtures::*;
use super::*;
use crate::edges::{Edge, Object};

fn found(edges: Vec<Edge>) -> Vec<Loop> {
    Graph::new([edges]).loops()
}

fn ring(kind: Kind) -> Vec<Edge> {
    vec![
        edge(
            kind,
            object("Deployment", "shop", "web"),
            object("Deployment", "blog", "web"),
        ),
        edge(
            kind,
            object("Deployment", "blog", "web"),
            object("Deployment", "shop", "web"),
        ),
    ]
}

#[rstest]
fn finds_the_forge_that_its_tool_applies_from() {
    assert_eq!(
        found(forge_loop()),
        vec![Loop {
            namespaces: vec!["deploy".into(), "forge".into()],
            links: vec![
                link(
                    "Deployment forge/forge",
                    "Tree forge/forge",
                    &[Kind::ReconcilesFrom]
                ),
                link(
                    "Repo deploy/fleet",
                    "Deployment forge/forge",
                    &[Kind::DependsOn]
                ),
                link(
                    "Tree forge/forge",
                    "Repo deploy/fleet",
                    &[Kind::ReconcilesFrom]
                ),
            ],
        }]
    );
}

#[rstest]
#[case::reconciles_from(Kind::ReconcilesFrom, 1)]
#[case::depends_on(Kind::DependsOn, 1)]
#[case::authenticates_through(Kind::AuthenticatesThrough, 1)]
#[case::controlled_by(Kind::ControlledBy, 1)]
#[case::stores_on(Kind::StoresOn, 1)]
#[case::holds(Kind::Holds, 1)]
#[case::runs_on(Kind::RunsOn, 0)]
#[case::selects(Kind::Selects, 0)]
#[case::routes_to(Kind::RoutesTo, 0)]
#[case::allowed_to_call(Kind::AllowedToCall, 0)]
#[case::part_of(Kind::PartOf, 0)]
fn counts_only_the_kinds_that_need(#[case] kind: Kind, #[case] loops: usize) {
    assert_eq!(found(ring(kind)).len(), loops);
}

/// Namespace `a` holds a helper that depends on `b`'s workload, and the
/// applier and source that `b`'s workload is applied from.  Paths join
/// the namespaces both ways; the objects form a cycle only when
/// something on the path needs the helper back.
fn helper_and_applier(closed: bool) -> Vec<Edge> {
    let helper = || object("Deployment", "a", "helper");
    let workload = || object("Deployment", "b", "web");
    let applier = || object("Tree", "a", "apps");
    let repository = || object("Repo", "a", "fleet");
    let mut edges = vec![
        edge(Kind::DependsOn, helper(), workload()),
        edge(Kind::ReconcilesFrom, workload(), applier()),
        edge(Kind::ReconcilesFrom, applier(), repository()),
    ];
    if closed {
        edges.push(edge(Kind::DependsOn, repository(), helper()));
    }
    edges
}

#[rstest]
#[case::paths_both_ways_between_namespaces(helper_and_applier(false), vec![])]
#[case::a_cycle_among_the_objects(helper_and_applier(true), vec![vec!["a", "b"]])]
fn a_loop_is_a_cycle_among_objects(#[case] edges: Vec<Edge>, #[case] expected: Vec<Vec<&str>>) {
    let namespaces: Vec<Vec<String>> = found(edges).into_iter().map(|l| l.namespaces).collect();
    assert_eq!(namespaces, expected);
}

#[rstest]
fn leaves_out_the_namespaces_a_path_only_passes_through() {
    let mut edges = forge_loop();
    edges.extend([
        edge(
            Kind::DependsOn,
            object("Deployment", "shop", "web"),
            object("Deployment", "forge", "forge"),
        ),
        edge(
            Kind::ReconcilesFrom,
            object("Deployment", "deploy", "deploy"),
            object("Tree", "shop", "shop"),
        ),
    ]);
    let namespaces: Vec<Vec<String>> = found(edges).into_iter().map(|l| l.namespaces).collect();
    assert_eq!(namespaces, vec![vec!["deploy".to_string(), "forge".into()]]);
}

#[rstest]
fn leaves_out_a_cycle_inside_one_namespace() {
    let applier = || object("Tree", "deploy", "deploy");
    let repository = || object("Repo", "deploy", "deploy");
    let edges = vec![
        edge(Kind::ReconcilesFrom, applier(), repository()),
        edge(Kind::ReconcilesFrom, repository(), applier()),
    ];
    assert_eq!(found(edges), vec![]);
}

#[rstest]
fn a_path_one_way_is_no_loop() {
    let mut edges = forge_loop();
    edges.pop();
    assert_eq!(found(edges), vec![]);
}

#[rstest]
fn joins_the_kinds_between_two_members() {
    let mut edges = ring(Kind::DependsOn);
    edges.push(edge(
        Kind::StoresOn,
        object("Deployment", "shop", "web"),
        object("Deployment", "blog", "web"),
    ));
    let links = &found(edges)[0].links;
    assert_eq!(
        links,
        &vec![
            link(
                "Deployment blog/web",
                "Deployment shop/web",
                &[Kind::DependsOn]
            ),
            link(
                "Deployment shop/web",
                "Deployment blog/web",
                &[Kind::DependsOn, Kind::StoresOn]
            ),
        ]
    );
}

#[rstest]
fn an_object_stands_for_its_top_owner() {
    let edges = vec![
        edge(
            Kind::ManagedBy,
            object("Pod", "shop", "web-1"),
            object("Deployment", "shop", "web"),
        ),
        edge(
            Kind::DependsOn,
            object("Deployment", "shop", "web"),
            object("Deployment", "blog", "web"),
        ),
        edge(
            Kind::DependsOn,
            object("Deployment", "blog", "web"),
            object("Pod", "shop", "web-1"),
        ),
    ];
    assert_eq!(
        found(edges)[0].links,
        vec![
            link(
                "Deployment blog/web",
                "Deployment shop/web",
                &[Kind::DependsOn]
            ),
            link(
                "Deployment shop/web",
                "Deployment blog/web",
                &[Kind::DependsOn]
            ),
        ]
    );
}

#[rstest]
fn carries_a_loop_through_a_cluster_scoped_owner() {
    let owner = Endpoint::Object(Object::new("example.io", "Fleet", None, "all"));
    let edges = vec![
        edge(Kind::ManagedBy, object("Pod", "shop", "web"), owner.clone()),
        edge(
            Kind::ControlledBy,
            owner.clone(),
            object("Deployment", "operator", "op"),
        ),
        edge(
            Kind::DependsOn,
            object("Deployment", "operator", "op"),
            owner,
        ),
    ];
    let loops = found(edges);
    assert_eq!(loops[0].namespaces, vec!["operator", "shop"]);
    assert_eq!(
        loops[0].links,
        vec![
            link("Deployment operator/op", "Fleet all", &[Kind::DependsOn]),
            link("Fleet all", "Deployment operator/op", &[Kind::ControlledBy]),
        ]
    );
}

#[rstest]
fn machines_and_outside_boxes_end_a_path() {
    let edges = vec![
        edge(
            Kind::StoresOn,
            object("Deployment", "shop", "web"),
            Endpoint::Outside("nas".into()),
        ),
        edge(
            Kind::StoresOn,
            object("Deployment", "blog", "web"),
            Endpoint::Outside("nas".into()),
        ),
        edge(
            Kind::DependsOn,
            object("Deployment", "shop", "web"),
            Endpoint::Machine("alpha".into()),
        ),
    ];
    assert_eq!(found(edges), vec![]);
}
