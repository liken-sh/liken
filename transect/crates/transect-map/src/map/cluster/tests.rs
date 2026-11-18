use rstest::{fixture, rstest};

use super::*;
use crate::map::relation::Way;
use transect_graph::edges::Kind;
use transect_resources::layers::fixtures::namespace;

#[fixture]
fn layers() -> Layers {
    Layers::from_yaml(
        r#"
apiVersion: transect.liken.sh/v1alpha1
kind: Layers
spec:
  layers:
    - name: apps
      namespaceSelectors: [{matchLabels: {tier: apps}}]
    - name: platform
      namespaceSelectors: [{matchLabels: {tier: platform}}]
"#,
    )
    .unwrap()
}

#[rstest]
#[case::unselected(false, "/?select=shop")]
#[case::selected(true, "/?lose=alpha")]
fn a_second_click_on_a_tile_deselects_it(#[case] selected: bool, #[case] href: &str) {
    let tile = NamespaceTile {
        selected,
        ..tile("shop", 1)
    };
    assert_eq!(tile.href("/?lose=alpha"), href);
}

fn band_names(map: &ClusterMap) -> Vec<&str> {
    map.bands.iter().map(|band| band.name.as_str()).collect()
}

fn tile(namespace: &str, workloads: usize) -> NamespaceTile {
    NamespaceTile {
        namespace: namespace.into(),
        workloads,
        health: Health::default(),
        selected: false,
        relations: Vec::new(),
        looped: false,
        loss: None,
    }
}

#[rstest]
fn tiles_sort_by_name_whatever_their_weight(layers: Layers) {
    let namespaces = [
        namespace("blog", &[("tier", "apps")], None),
        namespace("Shop", &[("tier", "apps")], None),
        namespace("wiki", &[("tier", "apps")], None),
        namespace("chat", &[("tier", "apps")], None),
    ];
    let workloads = HashMap::from([("Shop".into(), 3), ("wiki".into(), 1), ("blog".into(), 1)]);
    let map = ClusterMap::build(
        &layers,
        &namespaces,
        &workloads,
        HashMap::new(),
        &HashMap::new(),
    );
    assert_eq!(
        map.bands[0].tiles,
        vec![
            tile("blog", 1),
            tile("chat", 0),
            tile("Shop", 3),
            tile("wiki", 1)
        ]
    );
}

#[rstest]
fn every_layer_draws_and_unsorted_is_left_out_when_empty(layers: Layers) {
    let namespaces = [namespace("blog", &[("tier", "apps")], None)];
    let map = ClusterMap::build(
        &layers,
        &namespaces,
        &HashMap::new(),
        HashMap::new(),
        &HashMap::new(),
    );
    assert_eq!(band_names(&map), vec!["apps", "platform"]);
}

#[rstest]
fn unmatched_namespaces_go_to_unsorted_at_the_bottom(layers: Layers) {
    let namespaces = [
        namespace("scratch", &[], None),
        namespace("dns", &[("tier", "platform")], None),
    ];
    let map = ClusterMap::build(
        &layers,
        &namespaces,
        &HashMap::new(),
        HashMap::new(),
        &HashMap::new(),
    );
    assert_eq!(band_names(&map), vec!["apps", "platform", UNSORTED]);
    assert_eq!(map.bands[2].tiles, vec![tile("scratch", 0)]);
}

#[rstest]
fn a_tile_takes_its_namespaces_health(layers: Layers) {
    let namespaces = [
        namespace("blog", &[("tier", "apps")], None),
        namespace("shop", &[("tier", "apps")], None),
    ];
    let failing = Health {
        level: Level::Failing,
        reasons: Vec::new(),
    };
    let health = HashMap::from([("shop".into(), failing.clone())]);
    let map = ClusterMap::build(
        &layers,
        &namespaces,
        &HashMap::new(),
        health,
        &HashMap::new(),
    );
    let levels: Vec<(&str, &str)> = map.bands[0]
        .tiles
        .iter()
        .map(|tile| (tile.namespace.as_str(), tile.health_class()))
        .collect();
    assert_eq!(levels, vec![("blog", ""), ("shop", " ns-tile-failing")]);
}

#[rstest]
#[case::a_namespace_on_the_map("dns", Some(("dns", "platform")), vec!["dns"])]
#[case::a_namespace_not_on_the_map("gone", None, vec![])]
fn selecting_marks_one_tile_and_names_its_layer(
    layers: Layers,
    #[case] select: &str,
    #[case] expected: Option<(&str, &str)>,
    #[case] marked: Vec<&str>,
) {
    let namespaces = [
        namespace("blog", &[("tier", "apps")], None),
        namespace("dns", &[("tier", "platform")], None),
    ];
    let mut map = ClusterMap::build(
        &layers,
        &namespaces,
        &HashMap::new(),
        HashMap::new(),
        &HashMap::new(),
    );
    let selected = map.select(select);
    assert_eq!(
        selected,
        expected.map(|(namespace, layer)| Selected {
            namespace: namespace.into(),
            layer: layer.into(),
        })
    );
    let selected_tiles: Vec<&str> = map
        .bands
        .iter()
        .flat_map(|band| &band.tiles)
        .filter(|tile| tile.selected)
        .map(|tile| tile.namespace.as_str())
        .collect();
    assert_eq!(selected_tiles, marked);
}

fn related(kind: Kind, way: Way, guessed: bool) -> Relation {
    Relation { kind, way, guessed }
}

#[rstest]
fn relates_a_tile_once_per_kind_and_way(layers: Layers) {
    let namespaces = [
        namespace("blog", &[("tier", "apps")], None),
        namespace("dns", &[("tier", "platform")], None),
    ];
    let mut map = ClusterMap::build(
        &layers,
        &namespaces,
        &HashMap::new(),
        HashMap::new(),
        &HashMap::new(),
    );
    map.relate("dns", related(Kind::ControlledBy, Way::From, false));
    map.relate("dns", related(Kind::DependsOn, Way::From, false));
    map.relate("dns", related(Kind::DependsOn, Way::From, false));
    let relations: Vec<(&str, &[Relation])> = map
        .bands
        .iter()
        .flat_map(|band| &band.tiles)
        .map(|tile| (tile.namespace.as_str(), tile.relations.as_slice()))
        .collect();
    assert_eq!(
        relations,
        vec![
            ("blog", &[][..]),
            (
                "dns",
                &[
                    related(Kind::DependsOn, Way::From, false),
                    related(Kind::ControlledBy, Way::From, false)
                ][..]
            )
        ]
    );
}

#[rstest]
fn marks_each_namespace_in_a_loop(layers: Layers) {
    let namespaces = [
        namespace("blog", &[("tier", "apps")], None),
        namespace("forge", &[("tier", "apps")], None),
        namespace("deploy", &[("tier", "platform")], None),
    ];
    let mut map = ClusterMap::build(
        &layers,
        &namespaces,
        &HashMap::new(),
        HashMap::new(),
        &HashMap::new(),
    );
    map.mark_loops(&[Loop {
        namespaces: vec!["deploy".into(), "forge".into()],
        links: Vec::new(),
    }]);
    let looped: Vec<(&str, bool)> = map
        .bands
        .iter()
        .flat_map(|band| &band.tiles)
        .map(|tile| (tile.namespace.as_str(), tile.looped))
        .collect();
    assert_eq!(
        looped,
        vec![("blog", false), ("forge", true), ("deploy", true)]
    );
}

#[rstest]
#[case::none(vec![], "", "")]
#[case::one(vec![related(Kind::DependsOn, Way::To, false)], "needs it", "needs it")]
#[case::a_guess(
    vec![related(Kind::ControlledBy, Way::From, true)],
    "controls it?",
    "controls it? (a guess from labels)"
)]
#[case::several(
    vec![
        related(Kind::DependsOn, Way::From, false),
        related(Kind::AllowedToCall, Way::To, false),
    ],
    "it needs +1",
    "it needs · may call it"
)]
fn a_tile_says_its_first_relation_and_counts_the_rest(
    #[case] relations: Vec<Relation>,
    #[case] line: &str,
    #[case] title: &str,
) {
    let tile = NamespaceTile {
        relations,
        ..tile("blog", 3)
    };
    assert_eq!(
        (tile.relation().as_str(), tile.relation_title().as_str()),
        (line, title)
    );
}

#[rstest]
#[case::none(0, 1)]
#[case::one(1, 1)]
#[case::several(9, 9)]
fn tile_weight(#[case] workloads: usize, #[case] weight: usize) {
    assert_eq!(tile("blog", workloads).weight(), weight);
}
