use rstest::rstest;

use super::*;

fn line(workloads: &[(&str, State)]) -> Line {
    Line {
        namespace: "shop".into(),
        workloads: workloads
            .iter()
            .map(|(name, state)| {
                let object =
                    transect_graph::edges::Object::new("apps", "Deployment", Some("shop"), name);
                let named = Named {
                    shown: (*name).into(),
                    object,
                };
                (named, *state)
            })
            .collect(),
        stranded: Vec::new(),
    }
}

/// The text of the reasons that a line's stranded workloads stay down.
fn stranded_text(line: &Line) -> Option<String> {
    let pieces = line.stranded_pieces();
    (!pieces.is_empty()).then(|| transect_graph::links::text(&pieces))
}

/// A line whose workloads are all down, each stranded for the reason
/// that `stranded` gives.
fn stranded_line(stranded: &[(&str, Stranded)]) -> Line {
    let down: Vec<(&str, State)> = stranded
        .iter()
        .map(|(name, _)| (*name, State::Down { wanted: 1 }))
        .collect();
    Line {
        stranded: stranded
            .iter()
            .map(|(name, reason)| ((*name).into(), reason.clone()))
            .collect(),
        ..line(&down)
    }
}

fn attached(kind: Type) -> Stranded {
    Stranded::Attached {
        kind,
        machine: "alpha".into(),
    }
}

#[rstest]
#[case::none(&[], None)]
#[case::one_iscsi(&[("db", attached(Type::Iscsi))], Some("Its iSCSI volume is attached to alpha."))]
#[case::one_by_class(
    &[("db", attached(Type::Class("fast".into())))],
    Some("Its fast volume is attached to alpha.")
)]
#[case::one_on_disk(
    &[("cache", Stranded::Disk { machine: "alpha".into() })],
    Some("Its volume is on alpha's own disk.")
)]
#[case::several_named(
    &[
        ("db", attached(Type::Unclassed)),
        ("radio", Stranded::Device { device: "usb radio-1".into(), machine: "alpha".into() }),
    ],
    Some("db's volume is attached to alpha. radio holds usb radio-1 on alpha.")
)]
#[case::several_sharing_a_reason(
    &[("db", attached(Type::Iscsi)), ("web", attached(Type::Iscsi))],
    Some("Their iSCSI volumes are attached to alpha.")
)]
#[case::a_group_beside_another(
    &[
        ("db", attached(Type::Iscsi)),
        ("radio", Stranded::Device { device: "usb radio-1".into(), machine: "alpha".into() }),
        ("web", attached(Type::Iscsi)),
    ],
    Some("db and web: their iSCSI volumes are attached to alpha. radio holds usb radio-1 on alpha.")
)]
fn says_why_a_workload_stays_down(
    #[case] stranded: &[(&str, Stranded)],
    #[case] text: Option<&str>,
) {
    assert_eq!(stranded_text(&stranded_line(stranded)).as_deref(), text);
}

#[rstest]
fn reports_only_the_stranded_workload_of_a_line_with_others() {
    let web = line(&[("web", State::Down { wanted: 1 })]).workloads;
    let mut several = stranded_line(&[("db", attached(Type::Iscsi))]);
    several.workloads.extend(web);
    assert_eq!(
        stranded_text(&several).as_deref(),
        Some("db's iSCSI volume is attached to alpha.")
    );
}

const ONE: State = State::Down { wanted: 1 };

#[rstest]
#[case::its_only_instance(&[("db", ONE)], "db, its only instance")]
#[case::one_replica_each(&[("cache", ONE), ("web", ONE)], "cache and web, one replica each")]
#[case::none_left(&[("queue", State::Down { wanted: 3 })], "queue, none of 3 left")]
#[case::mixed(
    &[("db", ONE), ("queue", State::Down { wanted: 3 })],
    "db, its only instance; queue, none of 3 left"
)]
#[case::short(&[("web", State::Short { ready: 2, wanted: 3 })], "web 2 of 3")]
#[case::a_primary(
    &[("db", State::Primary { ready: 1, wanted: 2 })],
    "db's primary; its operator promotes its replica"
)]
#[case::a_primary_of_several(
    &[("db", State::Primary { ready: 2, wanted: 3 })],
    "db's primary; its operator promotes one of its replicas"
)]
#[case::short_alike(
    &[
        ("api", State::Short { ready: 2, wanted: 3 }),
        ("dns", State::Short { ready: 1, wanted: 2 }),
        ("web", State::Short { ready: 2, wanted: 3 }),
    ],
    "api and web 2 of 3; dns 1 of 2"
)]
fn a_namespaces_line(#[case] workloads: &[(&str, State)], #[case] text: &str) {
    assert_eq!(line(workloads).to_string(), text);
}

#[rstest]
#[case::named(
    false,
    &[("db", ONE), ("web", State::Short { ready: 1, wanted: 2 })],
    &[],
    &[],
    "db down · web 1/2"
)]
#[case::alone_down(true, &[("db", ONE)], &[], &[], "down")]
#[case::alone_short(true, &[("web", State::Short { ready: 2, wanted: 3 })], &[], &[], "2/3")]
#[case::alone_primary(true, &[("db", State::Primary { ready: 1, wanted: 2 })], &[], &[], "no primary")]
#[case::loses_elsewhere(false, &[], &[], &["cache", "db"], "can't reach cache and db")]
#[case::down_and_loses(true, &[("web", ONE)], &[], &["db"], "down · can't reach db")]
#[case::lacks(
    false,
    &[("web", State::Short { ready: 1, wanted: 2 })],
    &["office file conversion", "order storage"],
    &["cache"],
    "web 1/2 · no office file conversion and order storage · can't reach cache"
)]
fn tile_text(
    #[case] alone: bool,
    #[case] taken: &[(&str, State)],
    #[case] lacks: &[&str],
    #[case] loses: &[&str],
    #[case] text: &str,
) {
    let strings = |names: &[&str]| names.iter().map(|name| name.to_string()).collect();
    let tile = NamespaceTile {
        level: transect_observe::health::Level::Warning,
        taken: line(taken)
            .workloads
            .into_iter()
            .map(|(named, state)| (named.shown, state))
            .collect(),
        alone,
        lacks: strings(lacks),
        loses: strings(loses),
    };
    assert_eq!(tile.to_string(), text);
}

#[rstest]
#[case::one(&["alpha"], "alpha")]
#[case::two(&["alpha", "beta"], "alpha and beta")]
#[case::three(&["alpha", "beta", "gamma"], "alpha, beta, and gamma")]
fn lists_names(#[case] names: &[&str], #[case] text: &str) {
    assert_eq!(list(names.iter().copied()), text);
}

fn sidebar(machines: &[&str], pods: usize, daemon_sets: usize, folded: usize) -> Sidebar {
    Sidebar {
        machines: Lost::of(machines.iter().copied(), machines.iter().copied()),
        pods,
        stuck: Vec::new(),
        back: Vec::new(),
        vital: Vec::new(),
        primary: Vec::new(),
        short: Vec::new(),
        folded,
        daemon_sets,
        cascade: Vec::new(),
        unreconciled: Vec::new(),
        plurals: Default::default(),
        gone: Vec::new(),
        majority: None,
        unknown: None,
        blind: None,
        alerting: None,
        linker: Default::default(),
        next: crate::loss::Next::default(),
    }
}

#[rstest]
#[case::one_machine(
    sidebar(&["alpha"], 12, 3, 4),
    ["If alpha were lost", "12 pods run there.", "Down until it returns"],
    [Some("3 DaemonSets' pods are lost with it"), Some("and 4 more namespaces one replica short")]
)]
#[case::two_machines(
    sidebar(&["alpha", "beta"], 1, 1, 1),
    ["If alpha and beta were lost", "1 pod runs there.", "Down until they return"],
    [Some("1 DaemonSet's pods are lost with them"), Some("and 1 more namespace one replica short")]
)]
#[case::nothing_to_fold(
    sidebar(&["alpha"], 0, 0, 0),
    ["If alpha were lost", "0 pods run there.", "Down until it returns"],
    [None, None]
)]
fn sidebar_text(
    #[case] sidebar: Sidebar,
    #[case] heads: [&str; 3],
    #[case] lines: [Option<&str>; 2],
) {
    assert_eq!(
        [sidebar.title(), sidebar.summary(), sidebar.stuck_heading()],
        heads.map(String::from)
    );
    assert_eq!(
        [sidebar.daemon_sets_text(), sidebar.folded_text()],
        lines.map(|line| line.map(String::from))
    );
}

/// The custom resources that a lost controller reconciles get one line
/// for the controller.  A kind with several objects appears as a count
/// linked to the kind's page, and a kind with one object appears as
/// that object's name.
#[rstest]
fn says_what_nothing_reconciles_until_its_controller_returns() {
    let object = |kind: &str, name: &str| {
        transect_graph::edges::Object::new("apply.example.com", kind, Some("shop"), name)
    };
    let controller =
        transect_graph::edges::Object::new("apps", "Deployment", Some("deploy"), "applier");
    let mut sidebar = sidebar(&["alpha"], 0, 0, 0);
    sidebar.plurals = std::collections::HashMap::from([(
        ("apply.example.com".to_string(), "Tree".to_string()),
        "trees".to_string(),
    )]);
    let line = crate::loss::sidebar::Unreconciled {
        controllers: vec![controller],
        objects: vec![
            object("Bucket", "charts"),
            object("Tree", "a"),
            object("Tree", "b"),
            object("Tree", "c"),
        ],
    };
    let pieces = sidebar.unreconciled_pieces(&line);
    let linked: Vec<_> = pieces
        .iter()
        .filter_map(|piece| match piece {
            transect_graph::links::Piece::Page(text, href) => Some((text.as_str(), href.as_str())),
            _ => None,
        })
        .collect();
    assert_eq!(
        (transect_graph::links::text(&pieces), linked),
        (
            "Until deploy/applier returns, nothing reconciles Bucket shop/charts and 3 Trees."
                .to_string(),
            vec![("3 Trees", "/kinds/trees.apply.example.com")]
        )
    );
}
