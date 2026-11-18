use rstest::rstest;

use super::change::{self, Change, Changer, Target};
use super::reach::{self, Group, Inside, Out, Path, Port, Reach, Right, ServiceBox, WayBox};
use super::{Drawing, FOLLOWS, Gate, PLAIN};
use transect_map::map::section_drawing::{Look, SectionBox};

fn boxed(title: &str) -> SectionBox {
    SectionBox::new(title.into(), vec!["a line under it".into()], Look::Plain)
}

fn gate() -> Gate {
    Gate {
        class: "ax-gate ax-gate-off",
        title: "No gate.".into(),
        declared: false,
    }
}

fn port(number: u16) -> Port {
    Port {
        text: format!(":{number}"),
        title: format!("Container port {number}."),
    }
}

fn right(calls: usize) -> Right {
    Right {
        runs_as: boxed("ServiceAccount web"),
        holds: vec![boxed("data-web-0")],
        calls: (0..calls).map(|_| boxed("Deployment db")).collect(),
        out: Out::Any(gate()),
    }
}

/// A workload reached through the ingress controller and directly
/// through the Service `web-debug`, and that any pod may call.
fn behind_the_controller() -> Reach {
    let way = |path| WayBox {
        boxed: boxed("shop.example.test"),
        tls: gate(),
        sign_in: gate(),
        path,
    };
    Reach {
        ways: vec![
            way(Path::Controller),
            way(Path::Controller),
            way(Path::Service(1)),
        ],
        controller: Some(boxed("proxy")),
        services: vec![
            ServiceBox {
                boxed: boxed("Service web"),
                ports: vec![0],
                policy: gate(),
                from_controller: true,
            },
            ServiceBox {
                boxed: boxed("Service web-debug"),
                ports: vec![0, 1],
                policy: gate(),
                from_controller: false,
            },
        ],
        inside: Inside::Any {
            boxed: Box::new(boxed("any pod")),
            label: "no NetworkPolicy selects its pods".into(),
            gate: gate(),
        },
        ports: vec![port(8080), port(9090)],
        workload: boxed("Deployment web"),
        right: right(2),
    }
}

/// A database that nothing outside the cluster reaches.  One rule
/// admits callers on any port, one on its second port, and one on its
/// first.  No rule names its third port.
fn no_way_in() -> Reach {
    let group = |callers: usize, ports: Vec<usize>| Group {
        callers: (0..callers).map(|_| boxed("Deployment web")).collect(),
        policy: "db-from-web".into(),
        href: None,
        gate: gate(),
        ports,
    };
    Reach {
        ways: Vec::new(),
        controller: None,
        services: Vec::new(),
        inside: Inside::Admitted(vec![
            group(3, Vec::new()),
            group(1, vec![1]),
            group(2, vec![0]),
        ]),
        ports: vec![port(5432), port(8000), port(9187)],
        workload: boxed("StatefulSet db"),
        right: right(0),
    }
}

/// The database of `no_way_in` with three Services that forward to its
/// first port, as a Postgres operator creates them.
fn fronted_by_services() -> Reach {
    let service = |name: &str| ServiceBox {
        boxed: boxed(name),
        ports: vec![0],
        policy: gate(),
        from_controller: false,
    };
    let group = |callers: usize, ports: Vec<usize>| Group {
        callers: (0..callers).map(|_| boxed("Deployment web")).collect(),
        policy: "db-from-web".into(),
        href: None,
        gate: gate(),
        ports,
    };
    Reach {
        services: vec![
            service("Service db-rw"),
            service("Service db-ro"),
            service("Service db-r"),
        ],
        inside: Inside::Admitted(vec![
            group(3, Vec::new()),
            group(1, vec![1]),
            group(2, vec![0]),
        ]),
        ..no_way_in()
    }
}

#[rstest]
#[case::no_way_in(no_way_in())]
#[case::fronted_by_services(fronted_by_services())]
#[case::behind_the_controller(behind_the_controller())]
fn no_two_lines_cross(#[case] reach: Reach) {
    assert_eq!(reach::draw(reach).crossings(), vec![]);
}

#[rstest]
#[case::behind_the_controller(reach::draw(behind_the_controller()))]
#[case::no_way_in(reach::draw(no_way_in()))]
#[case::fronted_by_services(reach::draw(fronted_by_services()))]
fn every_line_ends_on_an_edge_and_no_boxes_overlap(#[case] drawing: Drawing) {
    assert_eq!(
        (drawing.ends_on_edges(), drawing.boxes_apart()),
        (true, true)
    );
}

#[rstest]
fn draws_a_port_for_each_the_workload_serves() {
    let drawing = reach::draw(no_way_in());
    let ports: Vec<&str> = drawing.sockets.iter().map(|s| s.text.as_str()).collect();
    let mut sorted = ports.clone();
    sorted.sort();
    assert_eq!(sorted, vec![":5432", ":8000", ":9187"]);
}

fn changer(title: &str, lines: Vec<(Target, &str)>, class: &'static str) -> Changer {
    Changer {
        boxed: boxed(title),
        class,
        lines: lines.into_iter().map(|(t, l)| (t, l.to_string())).collect(),
    }
}

#[rstest]
fn who_may_change_it_draws_each_line_to_an_edge() {
    let drawing = change::draw(Change {
        changers: vec![
            changer(
                "ServiceAccount ops/console",
                vec![
                    (Target::Applier(0), "may patch"),
                    (Target::Workload, "may change it"),
                ],
                PLAIN,
            ),
            changer(
                "2 accounts bound to cluster-admin",
                vec![(Target::Workload, "every verb")],
                PLAIN,
            ),
            changer(
                "3 accounts",
                vec![(Target::Workload, "may change it")],
                PLAIN,
            ),
            changer(
                "4 accounts, by running as another",
                vec![(Target::Workload, "follows")],
                FOLLOWS,
            ),
        ],
        appliers: vec![
            boxed("Kustomization apps"),
            boxed("Kustomization unpatched"),
        ],
        workload: boxed("Deployment web"),
    });
    assert_eq!(
        (drawing.ends_on_edges(), drawing.boxes_apart()),
        (true, true)
    );
}

/// An account that may change the workload and patch its applier.
fn both(title: &str) -> Changer {
    let lines = vec![
        (Target::Workload, "may change it"),
        (Target::Applier(0), "may patch"),
    ];
    changer(title, lines, PLAIN)
}

/// Who may change a workload that a GitOps tool applies, as a page
/// draws it: some accounts may patch the applier, others change the
/// workload directly, in an order that mixes the two, and two do both.
fn through_an_applier() -> Change {
    Change {
        changers: vec![
            changer(
                "ServiceAccount ops/console",
                vec![(Target::Applier(0), "may patch")],
                PLAIN,
            ),
            changer(
                "ServiceAccount ops/operator",
                vec![(Target::Workload, "may change it")],
                PLAIN,
            ),
            changer(
                "2 accounts bound to cluster-admin",
                vec![(Target::Workload, "every verb")],
                PLAIN,
            ),
            changer("6 accounts", vec![(Target::Applier(0), "may patch")], PLAIN),
            both("ServiceAccount ops/both"),
            both("ServiceAccount ops/also-both"),
            changer(
                "3 controllers",
                vec![(Target::Workload, "may change it")],
                PLAIN,
            ),
            changer(
                "4 accounts, by running as another",
                vec![(Target::Workload, "follows")],
                FOLLOWS,
            ),
        ],
        appliers: vec![boxed("Kustomization apps")],
        workload: boxed("Deployment web"),
    }
}

#[test]
fn who_may_change_it_crosses_no_line() {
    let drawing = change::draw(through_an_applier());
    assert_eq!(
        (
            drawing.crossings(),
            drawing.ends_on_edges(),
            drawing.boxes_apart()
        ),
        (vec![], true, true)
    );
}

/// Three accounts that each patch the applier and change the workload
/// cannot be drawn without a crossing.  The lines from the first and
/// the last into the applier enclose a region, and the middle account's
/// line to the workload starts inside it, so it must cross one of them.
/// The drawing has only that one crossing.
#[test]
fn a_third_account_that_does_both_crosses_one_line() {
    let mut change = through_an_applier();
    change.changers.push(both("ServiceAccount ops/third"));
    assert_eq!(change::draw(change).crossings().len(), 1);
}

#[rstest]
#[case::one_line("every pod", 20, 1)]
#[case::wraps_at_a_space("any pod at its machine's address", 20, 2)]
#[case::wraps_after_a_slash("kube-system/storage-version-migrator-controller", 20, 3)]
#[case::breaks_a_piece_longer_than_a_line("abcdefghijklmnopqrstuvwxyz", 10, 3)]
fn wraps_as_a_browser_wraps(#[case] text: &str, #[case] most: usize, #[case] lines: usize) {
    assert_eq!(super::wrapped(text, most), lines);
}

#[test]
fn a_box_that_names_many_grows_to_hold_every_name() {
    let names = |count: usize| {
        let parts = (0..count).map(|at| transect_graph::links::Part {
            text: format!("kube-system/generic-controller-number-{at}"),
            href: None,
        });
        SectionBox::new("controllers".into(), vec![String::new()], Look::Plain)
            .with_names(parts.collect())
    };
    let one = super::height(&names(1), 340);
    let six = super::height(&names(6), 340);
    assert!(
        six >= one + 18 * 5,
        "a box of 6 names is {six} high, want at least {one} + 90 (a box of 1 name is {one})"
    );
}

#[test]
fn the_workload_keeps_its_height_when_any_pod_may_call_it() {
    let drawing = reach::draw(behind_the_controller());
    let rect = |title: &str| {
        let found = drawing.boxes.iter().find(|b| b.boxed.title == title);
        found.map(|b| b.rect).unwrap()
    };
    let (workload, any) = (rect("Deployment web"), rect("any pod"));
    assert!(
        workload.bottom() < any.middle(),
        "the workload box {workload:?} ends below the middle of the any-pod box {any:?}, want above"
    );
}

#[test]
fn a_long_title_in_a_narrow_box_takes_three_lines() {
    let boxed = SectionBox::new(
        "Deployment cert-manager/cert-manager-webhook".into(),
        vec!["port 10250".into()],
        Look::Plain,
    );
    assert_eq!(super::height(&boxed, 208), 28 + 18 * 4);
}
