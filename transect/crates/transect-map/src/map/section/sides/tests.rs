use rstest::rstest;

use super::*;
use transect_graph::edges::{Device, Provenance};

fn object(kind: &str, namespace: &str, name: &str) -> Object {
    Object::new("", kind, Some(namespace), name)
}

fn web() -> Object {
    object("Deployment", "shop", "web")
}

fn edge(kind: Kind, from: Endpoint, to: Endpoint, guessed: bool) -> Edge {
    Edge {
        kind,
        from,
        to,
        guessed,
        provenance: Provenance::Kubernetes,
    }
}

fn end(object: Object) -> Endpoint {
    Endpoint::Object(object)
}

fn gpu() -> Endpoint {
    Endpoint::Device(Device {
        driver: "gpu.example.com".into(),
        pool: "alpha".into(),
        name: "card-0".into(),
        machine: Some("alpha".into()),
    })
}

/// The graph around `web`.  A worker in another layer depends on web.
/// Web depends on a database and on machine beta, reconciles from a
/// `Tree`, has a controlled-by edge to an operator that transect
/// guessed, and holds the same GPU on alpha through two edges.
fn graph() -> Graph {
    Graph::new([vec![
        edge(
            Kind::DependsOn,
            end(object("Deployment", "jobs", "worker")),
            end(web()),
            false,
        ),
        edge(
            Kind::DependsOn,
            end(web()),
            end(object("StatefulSet", "shop", "database")),
            false,
        ),
        edge(
            Kind::DependsOn,
            end(web()),
            Endpoint::Machine("beta".into()),
            false,
        ),
        edge(
            Kind::ReconcilesFrom,
            end(web()),
            end(object("Tree", "deploy", "shop")),
            false,
        ),
        edge(
            Kind::ControlledBy,
            end(web()),
            end(object("Deployment", "operators", "shop-operator")),
            true,
        ),
        edge(Kind::Holds, end(web()), gpu(), false),
        edge(Kind::Holds, end(web()), gpu(), false),
    ]])
}

fn layer_of(namespace: &str) -> Option<String> {
    match namespace {
        "jobs" => Some("batch".into()),
        "deploy" | "operators" => Some("platform".into()),
        _ => None,
    }
}

fn layer(name: &str) -> Band {
    Band::Layer(name.into())
}

/// The lookups that `sides` uses, with `has_page` and no ready line for
/// any workload.
fn objects<'a>(has_page: &'a dyn Fn(&Object) -> bool) -> Objects<'a> {
    static PLURALS: std::sync::LazyLock<Plurals> = std::sync::LazyLock::new(Plurals::default);
    static INTENTS: std::sync::LazyLock<Intents> = std::sync::LazyLock::new(Intents::default);
    Objects {
        plurals: &PLURALS,
        intents: &INTENTS,
        has_page,
        ready: &|_| None,
        device_title: &|device| device.name.clone(),
    }
}

fn found() -> Vec<SideBox> {
    sides(&graph(), &web(), &objects(&|_| true), "apps", layer_of)
}

#[rstest]
#[case::every_object_has_a_page(true, true)]
#[case::no_object_has_a_page(false, false)]
fn links_an_objects_box_only_when_it_has_a_page(#[case] pages: bool, #[case] linked: bool) {
    let feed = Object::new("apps", "Deployment", Some("shop"), "feed");
    let graph = Graph::new([vec![edge(Kind::DependsOn, end(feed), end(web()), false)]]);
    let found = sides(&graph, &web(), &objects(&|_| pages), "apps", layer_of);
    let hrefs: Vec<bool> = found.iter().map(|s| s.boxed.href.is_some()).collect();
    assert_eq!(hrefs, vec![linked]);
}

#[rstest]
fn places_each_side_box_in_its_bands_row() {
    let found = found();
    let placed: Vec<(Band, Side, &str, &str)> = found
        .iter()
        .map(|s| {
            (
                s.band.clone(),
                s.side,
                s.boxed.title.as_str(),
                s.boxed.lines[0].as_str(),
            )
        })
        .collect();
    assert_eq!(
        placed,
        vec![
            (layer("batch"), Side::Left, "jobs/worker", "Deployment"),
            (layer("apps"), Side::Right, "database", "StatefulSet"),
            (Band::Machines, Side::Right, "beta", "machine"),
            (layer("platform"), Side::Right, "deploy/shop", "Tree"),
            (
                layer("platform"),
                Side::Right,
                "operators/shop-operator",
                "Deployment"
            ),
            (Band::Machines, Side::Right, "card-0", "gpu.example.com"),
        ]
    );
}

#[rstest]
fn joins_each_side_box_by_its_edge() {
    let found = found();
    let joined: Vec<(&str, Style, bool, Option<&str>)> = found
        .iter()
        .map(|s| (s.label.as_str(), s.style, s.inward, s.machine.as_deref()))
        .collect();
    assert_eq!(
        joined,
        vec![
            ("depends on", Style::Dashed, true, None),
            ("depends on", Style::Dashed, false, None),
            ("depends on", Style::Dashed, false, None),
            ("reconciles from", Style::Solid, false, None),
            ("controlled by", Style::Dotted, false, None),
            ("holds", Style::Solid, false, Some("alpha")),
        ]
    );
}

#[rstest]
fn a_machine_box_names_its_machine() {
    let found = found();
    let names: Vec<Option<&str>> = found.iter().map(|s| s.names.as_deref()).collect();
    assert_eq!(names, vec![None, None, Some("beta"), None, None, None]);
}

/// `count` workloads in another layer that call web.
fn called_by(count: usize) -> Graph {
    let callers = (0..count).map(|i| {
        let caller = object("Deployment", "jobs", &format!("caller-{i:02}"));
        edge(Kind::DependsOn, end(caller), end(web()), false)
    });
    Graph::new([callers.collect()])
}

fn names(count: usize) -> Vec<String> {
    (0..count).map(|i| format!("caller-{i:02}")).collect()
}

/// Each side box by its band, title, first line, and hint.
fn summary(found: &[SideBox]) -> Vec<(Band, String, String, Option<String>)> {
    found
        .iter()
        .map(|s| {
            let line = s.boxed.lines[0].clone();
            (
                s.band.clone(),
                s.boxed.title.clone(),
                line,
                s.boxed.hint.clone(),
            )
        })
        .collect()
}

fn each(count: usize) -> Vec<(Band, String, String, Option<String>)> {
    let line = "Deployment".to_string();
    let each = names(count).into_iter();
    each.map(|name| (layer("batch"), format!("jobs/{name}"), line.clone(), None))
        .collect()
}

fn folded(count: usize) -> Vec<(Band, String, String, Option<String>)> {
    let hint: Vec<String> = names(count).iter().map(|n| format!("jobs/{n}")).collect();
    let title = format!("{count} callers");
    vec![(
        layer("apps"),
        title,
        "in jobs".into(),
        Some(hint.join("\n")),
    )]
}

#[rstest]
#[case::two_drawn_one_by_one(2, each(2))]
#[case::six_drawn_one_by_one(6, each(6))]
#[case::seven_fold_into_one(7, folded(7))]
#[case::twelve_fold_into_one(12, folded(12))]
fn folds_callers_past_six_into_one_box(
    #[case] count: usize,
    #[case] expected: Vec<(Band, String, String, Option<String>)>,
) {
    let found = sides(
        &called_by(count),
        &web(),
        &objects(&|_| true),
        "apps",
        layer_of,
    );
    assert_eq!(summary(&found), expected);
}

#[rstest]
fn folded_callers_join_the_workload_with_one_dashed_line() {
    let found = sides(&called_by(9), &web(), &objects(&|_| true), "apps", layer_of);
    let joined: Vec<(Side, &str, Style, bool)> = found
        .iter()
        .map(|s| (s.side, s.label.as_str(), s.style, s.inward))
        .collect();
    assert_eq!(
        joined,
        vec![(Side::Left, "depends on", Style::Dashed, true)]
    );
}

fn held(name: &str) -> Graph {
    let device = Endpoint::Device(Device {
        driver: "usb.example.com".into(),
        pool: "alpha".into(),
        name: name.into(),
        machine: Some("alpha".into()),
    });
    Graph::new([vec![edge(Kind::Holds, end(web()), device, false)]])
}

#[rstest]
#[case::a_short_name("radio-0", 1)]
#[case::one_that_fits("pci-0000-00-02-0-draw", 1)]
#[case::one_too_long_for_a_line("pci-0000-00-02-0-display", 2)]
fn a_device_box_wraps_a_long_name(#[case] name: &str, #[case] title_lines: usize) {
    let found = sides(&held(name), &web(), &objects(&|_| true), "apps", layer_of);
    let boxed = &found[0].boxed;
    let full = format!("usb.example.com/alpha/{name}");
    assert_eq!(
        (boxed.title.as_str(), boxed.title_lines(), boxed.tooltip()),
        (name, title_lines, full.as_str())
    );
}

/// The intents that the annotations `database` and `worker` declare on
/// the database and on the worker that depends on web.
fn said(database: &str, worker: &str) -> Intents {
    let meta = |name: &str,
                annotations: &str|
     -> k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta {
        serde_yaml::from_str(&format!("{{name: {name}, annotations: {{{annotations}}}}}")).unwrap()
    };
    let (database_meta, worker_meta) = (meta("database", database), meta("worker", worker));
    let objects = [
        (object("StatefulSet", "shop", "database"), &database_meta),
        (object("Deployment", "jobs", "worker"), &worker_meta),
    ];
    let overlaid = transect_resources::resources::Overlaid::default();
    Intents::read(&objects, &[], &overlaid, &Plurals::default())
}

#[rstest]
#[case::provides_over_why(
    "transect.liken.sh/provides: order storage, transect.liken.sh/why: Keeps orders.",
    "transect.liken.sh/why: Sends the receipts.",
    vec![("jobs/worker", "Sends the receipts.", true), ("database", "order storage", true)]
)]
#[case::nothing_said("", "", vec![("jobs/worker", "Deployment", false), ("database", "StatefulSet", false)])]
fn a_side_box_says_what_intent_says_it_provides_or_is(
    #[case] database: &str,
    #[case] worker: &str,
    #[case] expected: Vec<(&str, &str, bool)>,
) {
    let intents = said(database, worker);
    let objects = Objects {
        intents: &intents,
        ..objects(&|_| true)
    };
    let found = sides(&graph(), &web(), &objects, "apps", layer_of);
    let lines: Vec<(&str, &str, bool)> = found
        .iter()
        .take(2)
        .map(|s| {
            (
                s.boxed.title.as_str(),
                s.boxed.lines[0].as_str(),
                s.boxed.mark.is_some(),
            )
        })
        .collect();
    assert_eq!(lines, expected);
}

#[rstest]
fn an_address_outside_the_cluster_goes_in_the_ground_row_marked_as_intent() {
    let provenance = Provenance::Intent(transect_graph::edges::Stated::Annotation {
        field: "depends-on".into(),
        on: "Deployment web".into(),
    });
    let graph = Graph::new([vec![Edge {
        provenance: provenance.clone(),
        ..edge(
            Kind::DependsOn,
            end(web()),
            Endpoint::Outside("api.example.com".into()),
            false,
        )
    }]]);
    let found = sides(&graph, &web(), &objects(&|_| true), "apps", layer_of);
    let placed: Vec<(Band, &str, Look, Option<&Provenance>)> = found
        .iter()
        .map(|s| {
            (
                s.band.clone(),
                s.boxed.title.as_str(),
                s.boxed.look,
                s.boxed.mark.as_ref(),
            )
        })
        .collect();
    assert_eq!(
        placed,
        vec![(
            Band::Ground,
            "api.example.com",
            Look::Ground,
            Some(&provenance)
        )]
    );
}

#[rstest]
#[case::ready(Some(("1 replica · ready".to_string(), false)), vec!["Deployment", "1 replica · ready"], Look::Plain)]
#[case::short(Some(("2 replicas · 1 ready".to_string(), true)), vec!["Deployment", "2 replicas · 1 ready"], Look::Short)]
#[case::runs_no_pods(None, vec!["Deployment"], Look::Plain)]
fn a_workloads_box_says_how_many_of_its_pods_are_ready(
    #[case] ready: Option<(String, bool)>,
    #[case] lines: Vec<&str>,
    #[case] look: Look,
) {
    let feed = Object::new("apps", "Deployment", Some("shop"), "feed");
    let graph = Graph::new([vec![edge(Kind::DependsOn, end(web()), end(feed), false)]]);
    let ready = move |_: &Object| ready.clone();
    let objects = Objects {
        ready: &ready,
        ..objects(&|_| true)
    };
    let found = sides(&graph, &web(), &objects, "apps", layer_of);
    let boxed = &found[0].boxed;
    assert_eq!(
        (boxed.lines.clone(), boxed.look),
        (lines.iter().map(|l| l.to_string()).collect(), look)
    );
}

/// Web holds a radio handset whose `station` attribute names a base
/// station, which gives the handset a depends-on edge to the station.
/// With `station_in_view`, web also depends on the station, so the view
/// draws the station's box.
fn paired(station_in_view: bool) -> Graph {
    let radio = Endpoint::Device(Device {
        driver: "radio.example.com".into(),
        pool: "alpha".into(),
        name: "handset-1".into(),
        machine: Some("alpha".into()),
    });
    let station = end(Object::new("radio.example.com", "Station", None, "desk"));
    let mut edges = vec![
        edge(Kind::Holds, end(web()), radio.clone(), false),
        Edge {
            provenance: Provenance::Devices {
                driver: "radio.example.com".into(),
                attribute: "station".into(),
                label: "pairs through".into(),
            },
            ..edge(Kind::DependsOn, radio, station.clone(), false)
        },
    ];
    if station_in_view {
        edges.push(edge(Kind::DependsOn, end(web()), station, false));
    }
    Graph::new([edges])
}

#[rstest]
#[case::to_a_box_in_the_view(true, vec![("handset-1".to_string(), "desk".to_string(), "pairs through".to_string())])]
#[case::to_nothing_the_view_draws(false, vec![])]
fn ties_a_device_to_the_box_its_attributes_name(
    #[case] station_in_view: bool,
    #[case] expected: Vec<(String, String, String)>,
) {
    let graph = paired(station_in_view);
    let found = sides(&graph, &web(), &objects(&|_| true), "apps", layer_of);
    let name = |end: &Endpoint| match end {
        Endpoint::Device(device) => device.name.clone(),
        Endpoint::Object(object) => object.name.clone(),
        _ => String::new(),
    };
    let tied: Vec<(String, String, String)> = ties(&graph, &found)
        .iter()
        .map(|tie| (name(&tie.from), name(&tie.to), tie.label.clone()))
        .collect();
    assert_eq!(tied, expected);
}
