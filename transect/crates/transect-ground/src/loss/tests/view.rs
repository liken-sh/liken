//! The loss as the sidebar and the map draw it, from a `Loss` built by
//! hand.

use rstest::rstest;

use super::super::*;
use super::on_disk;
use transect_observe::health::Level;

fn taken(namespace: &str, name: &str, state: State) -> Taken {
    Taken {
        object: Object::new("apps", "Deployment", Some(namespace), name),
        state,
        stranded: None,
        provides: None,
        vital: None,
    }
}

/// The text of the reasons that a line's stranded workloads stay down.
fn stranded_text(line: &sidebar::Line) -> Option<String> {
    let pieces = line.stranded_pieces();
    (!pieces.is_empty()).then(|| transect_graph::links::text(&pieces))
}

fn stuck(namespace: &str, name: &str) -> Taken {
    Taken {
        stranded: on_disk(),
        ..taken(namespace, name, State::Down { wanted: 1 })
    }
}

fn loss(workloads: Vec<Taken>) -> Loss {
    Loss {
        machines: Lost::of(["alpha"], ["alpha"]),
        pods: 0,
        workloads,
        daemon_sets: 0,
        next: Next::default(),
        cascade: Vec::new(),
    }
}

const ONE_SHORT: State = State::Short {
    ready: 1,
    wanted: 2,
};
const TWO_SHORT: State = State::Short {
    ready: 1,
    wanted: 3,
};

#[rstest]
#[case::folds_past_three(
    vec![
        taken("blog", "web", ONE_SHORT),
        taken("dns", "coredns", ONE_SHORT),
        taken("queue", "worker", TWO_SHORT),
        taken("shop", "web", ONE_SHORT),
        taken("wiki", "web", ONE_SHORT),
    ],
    vec!["queue", "blog", "dns"],
    2,
)]
#[case::keeps_every_namespace_more_than_one_short(
    vec![
        taken("blog", "web", TWO_SHORT),
        taken("dns", "coredns", TWO_SHORT),
        taken("queue", "worker", TWO_SHORT),
        taken("shop", "web", TWO_SHORT),
        taken("wiki", "web", ONE_SHORT),
    ],
    vec!["blog", "dns", "queue", "shop"],
    1,
)]
#[case::nothing_to_fold(
    vec![taken("blog", "web", ONE_SHORT), taken("shop", "db", State::Down { wanted: 1 })],
    vec!["blog"],
    0,
)]
fn the_sidebar_folds_namespaces_one_replica_short(
    #[case] workloads: Vec<Taken>,
    #[case] short: Vec<&str>,
    #[case] folded: usize,
) {
    let sidebar = Sidebar::build(&loss(workloads));
    let named: Vec<&str> = sidebar.short.iter().map(|l| l.namespace.as_str()).collect();
    assert_eq!((named, sidebar.folded), (short, folded));
}

#[rstest]
fn the_sidebar_lists_a_namespace_in_each_list() {
    let sidebar = Sidebar::build(&loss(vec![
        taken("shop", "db", State::Down { wanted: 1 }),
        taken("shop", "web", ONE_SHORT),
        taken("shop", "cache", State::Down { wanted: 1 }),
        stuck("shop", "radio"),
    ]));
    let lines = |lines: &[sidebar::Line]| -> Vec<String> {
        lines
            .iter()
            .map(|line| format!("{}: {line}", line.namespace))
            .collect()
    };
    assert_eq!(
        (
            lines(&sidebar.stuck),
            lines(&sidebar.back),
            lines(&sidebar.short)
        ),
        (
            vec!["shop: radio, its only instance".to_string()],
            vec!["shop: cache and db, one replica each".to_string()],
            vec!["shop: web 1 of 2".to_string()]
        )
    );
}

#[rstest]
fn a_line_names_the_kinds_of_workloads_that_share_a_name() {
    let jukebox = Taken {
        object: Object::new("media.example.com", "Jukebox", Some("media"), "den"),
        ..taken("media", "den", State::Down { wanted: 1 })
    };
    let dial = Taken {
        object: Object::new("media.example.com", "Dial", Some("media"), "den"),
        ..taken("media", "den", State::Down { wanted: 1 })
    };
    let sidebar = Sidebar::build(&loss(vec![jukebox, dial, taken("media", "web", ONE_SHORT)]));
    let lines: Vec<String> = sidebar
        .back
        .iter()
        .chain(&sidebar.short)
        .map(|line| line.to_string())
        .collect();
    assert_eq!(
        lines,
        vec!["Dial den and Jukebox den, one replica each", "web 1 of 2"]
    );
}

fn vital(namespace: &str, name: &str, state: State, vital: Vital) -> Taken {
    Taken {
        vital: Some(vital),
        ..taken(namespace, name, state)
    }
}

#[rstest]
#[case::down_leads(
    vec![vital("edge", "router", State::Down { wanted: 1 }, Vital::WayIn), vital("watch", "alertmanager", State::Down { wanted: 1 }, Vital::Alerting)],
    vec![
        "watch: alertmanager: no alert reaches anyone, so nothing below pages",
        "edge: router: web traffic from outside the cluster stops reaching it",
    ]
)]
#[case::short_still_works(vec![vital("kube-system", "dns", ONE_SHORT, Vital::Dns)], vec![])]
#[case::nothing_vital(vec![taken("shop", "web", State::Down { wanted: 1 })], vec![])]
fn the_sidebar_leads_with_what_blinds_or_cuts_off_the_cluster(
    #[case] workloads: Vec<Taken>,
    #[case] expected: Vec<&str>,
) {
    let sidebar = Sidebar::build(&loss(workloads));
    let lines: Vec<String> = sidebar
        .vital
        .iter()
        .map(|line| format!("{}: {line}", line.namespace))
        .collect();
    assert_eq!(lines, expected);
}

fn evicted() -> Next {
    Next {
        usual: Some(crate::placement::Eviction::After(300)),
        ..Next::default()
    }
}

fn majority(remain: usize, of: usize) -> Option<Majority> {
    Some(Majority {
        remain,
        of,
        counted: crate::loss::Counted::ControlPlane,
    })
}

#[rstest]
#[case::no_control_plane_lost(
    None,
    vec!["shop: radio, its only instance. Its volume is on alpha's own disk."],
    vec!["shop: db, its only instance"],
    false,
    None
)]
#[case::a_majority_kept(
    majority(2, 3),
    vec!["shop: radio, its only instance. Its volume is on alpha's own disk."],
    vec!["shop: db, its only instance"],
    false,
    None
)]
#[case::the_majority_lost(
    majority(1, 3),
    vec!["shop: db and radio, one replica each. radio's volume is on alpha's own disk."],
    vec![],
    true,
    Some(
        "With no majority, the API accepts no writes, so nothing is evicted or scheduled again. \
         Everything that ran on alpha is down until it returns; \
         everything else keeps running, and nothing reconciles it."
    )
)]
fn without_a_majority_nothing_comes_back_elsewhere(
    #[case] majority: Option<Majority>,
    #[case] down: Vec<&str>,
    #[case] back: Vec<&str>,
    #[case] nothing_next: bool,
    #[case] headless: Option<&str>,
) {
    let loss = Loss {
        next: evicted(),
        ..loss(vec![
            taken("shop", "db", State::Down { wanted: 1 }),
            stuck("shop", "radio"),
        ])
    };
    let sidebar = Sidebar::build(&loss).with_majority(majority);
    let lines = |lines: &[sidebar::Line]| -> Vec<String> {
        let lines = lines.iter();
        lines
            .map(|line| format!("{}: {line}", line.namespace))
            .collect()
    };
    let why = |line: &sidebar::Line| {
        let why = stranded_text(line).map(|why| format!(". {why}"));
        format!("{}: {line}{}", line.namespace, why.unwrap_or_default())
    };
    assert_eq!(
        (
            sidebar.stuck.iter().map(why).collect::<Vec<_>>(),
            lines(&sidebar.back),
            sidebar.next.is_empty(),
            sidebar.headless_text()
        ),
        (
            down.into_iter().map(String::from).collect(),
            back.into_iter().map(String::from).collect(),
            nothing_next,
            headless.map(String::from)
        )
    );
}

#[rstest]
#[case::a_workload_down(
    vec![taken("shop", "db", State::Down { wanted: 1 }), taken("shop", "web", ONE_SHORT)],
    Some(Level::Failing)
)]
#[case::only_short(vec![taken("shop", "web", ONE_SHORT)], Some(Level::Warning))]
#[case::another_namespace(vec![taken("blog", "web", State::Down { wanted: 1 })], None)]
fn a_tile_has_the_level_of_its_worst_loss(
    #[case] workloads: Vec<Taken>,
    #[case] level: Option<Level>,
) {
    let tile = loss(workloads).tile("shop", 2);
    assert_eq!(tile.map(|tile| tile.level), level);
}

#[rstest]
#[case::a_caller_elsewhere("blog", Some((Level::Warning, vec!["db"])))]
#[case::a_caller_beside_it("shop", Some((Level::Failing, vec![])))]
fn a_tile_whose_workloads_lose_another_namespaces_is_tagged(
    #[case] namespace: &str,
    #[case] expected: Option<(Level, Vec<&str>)>,
) {
    let caller = |namespace: &str, name: &str| {
        cascade::Dependent::Caller(Object::new("apps", "Deployment", Some(namespace), name))
    };
    let db = Object::new("apps", "Deployment", Some("shop"), "db");
    let mut loss = loss(vec![taken("shop", "db", State::Down { wanted: 1 })]);
    loss.cascade = vec![
        cascade::Loss {
            dependent: caller("blog", "feed"),
            lost: db.clone(),
            provides: None,
            declared_in: None,
        },
        cascade::Loss {
            dependent: caller("shop", "web"),
            lost: db,
            provides: None,
            declared_in: None,
        },
    ];
    let tile = loss.tile(namespace, 2);
    let found = tile
        .as_ref()
        .map(|p| (p.level, p.loses.iter().map(String::as_str).collect()));
    assert_eq!(found, expected);
}

fn storage_line() -> transect_resources::intent::Line {
    transect_resources::intent::Line {
        text: "order storage".into(),
        provenance: transect_graph::edges::Provenance::Kubernetes,
    }
}

/// `db` in shop provides order storage and is left down, and `feed` in
/// blog calls it.
#[rstest]
#[case::its_own_tile("shop", "no order storage")]
#[case::a_callers_tile("blog", "no order storage")]
fn a_tile_names_what_a_down_workload_provides(#[case] namespace: &str, #[case] tag: &str) {
    let db = Object::new("apps", "Deployment", Some("shop"), "db");
    let down = Taken {
        provides: Some(storage_line()),
        ..taken("shop", "db", State::Down { wanted: 1 })
    };
    let mut loss = loss(vec![down]);
    loss.cascade = vec![cascade::Loss {
        dependent: cascade::Dependent::Caller(Object::new(
            "apps",
            "Deployment",
            Some("blog"),
            "feed",
        )),
        lost: db,
        provides: Some(storage_line()),
        declared_in: None,
    }];
    let tile = loss.tile(namespace, 2).unwrap();
    assert_eq!(tile.to_string(), tag);
}

/// `login` in auth is left down, and `wiki` in shop signs in through it.
#[rstest]
#[case::the_applications_tile("shop", Some("no sign-in"))]
#[case::the_providers_own("auth", Some("down"))]
fn a_tile_shows_that_it_loses_sign_in(#[case] namespace: &str, #[case] tag: Option<&str>) {
    let mut loss = loss(vec![taken("auth", "login", State::Down { wanted: 1 })]);
    loss.cascade = vec![cascade::Loss {
        dependent: cascade::Dependent::SignsIn(Object::new(
            "apps",
            "Deployment",
            Some("shop"),
            "wiki",
        )),
        lost: Object::new("apps", "Deployment", Some("auth"), "login"),
        provides: None,
        declared_in: None,
    }];
    let tile = loss.tile(namespace, 1);
    assert_eq!(tile.map(|tile| tile.to_string()).as_deref(), tag);
}
