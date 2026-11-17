//! Whole section views with side boxes, from their parts to their lines: no two
//! boxes overlap, and every line ends on the edges of its boxes.

use rstest::rstest;

use super::*;
use crate::map::section::layout::{Layout, WIDTH, layout};
use crate::map::section::lines::{Style, draw};

fn side(band: Band, side: Side, name: &str, label: &str) -> SideBox {
    let boxed = SectionBox::new(name.into(), vec!["Deployment".into()], Look::Plain);
    SideBox::new(band, side, boxed, label, Style::Dashed)
}

fn caller(layer: &str, name: &str) -> SideBox {
    side(Band::Layer(layer.into()), Side::Left, name, "depends on").inward()
}

fn dependency(layer: &str, name: &str) -> SideBox {
    side(Band::Layer(layer.into()), Side::Right, name, "depends on")
}

fn device(machine: &str) -> SideBox {
    let held = side(
        Band::Machines,
        Side::Right,
        &format!("gpu {machine}"),
        "holds",
    );
    held.on(Some(machine.into()))
}

fn on_a_machine(machine: &str) -> SideBox {
    side(Band::Machines, Side::Right, machine, "depends on").naming(machine)
}

/// `parts` in a map of three layers, with `sides` beside its spine.
fn with_sides(parts: Parts, sides: Vec<SideBox>) -> Parts {
    Parts {
        layers: vec!["batch".into(), "apps".into(), "platform".into()],
        sides,
        ..parts
    }
}

/// A web application called from its own layer and the one above, that
/// calls a database and reconciles from an applier below.
fn a_web_application() -> Parts {
    let backing = mounted(nfs(&["media"]), &["machine-1"]);
    with_sides(
        parts(1, 1, vec![backing], hosts(1)),
        vec![
            caller("batch", "worker"),
            caller("apps", "admin"),
            dependency("apps", "database"),
            dependency("platform", "deploy"),
        ],
    )
}

/// A StatefulSet on `machines` machines, holding a device on each of
/// `held`, with a caller and a dependency beside it.
fn a_stateful_set(machines: usize, held: &[usize]) -> Parts {
    let names: Vec<String> = (1..=machines).map(|i| format!("machine-{i}")).collect();
    let names: Vec<&str> = names.iter().map(String::as_str).collect();
    let backing = mounted(nfs(&["data"]), &names);
    let mut sides = vec![caller("apps", "client"), dependency("apps", "cache")];
    sides.extend(held.iter().map(|i| device(&format!("machine-{i}"))));
    with_sides(parts(0, 1, vec![backing], hosts(machines)), sides)
}

/// A workload configured with a machine's address, whose line crosses
/// the claims row on its way down.
fn calls_a_machine() -> Parts {
    let backing = mounted(nfs(&["media"]), &["machine-1", "machine-2"]);
    let backings = vec![backing, local("machine-2")];
    with_sides(
        parts(0, 0, backings, hosts(3)),
        vec![on_a_machine("printer")],
    )
}

/// A workload on `hosts` machines, configured with the address of the
/// machine named `machine`.
fn calls(machine: &str, hosts: usize) -> Parts {
    let sides = vec![on_a_machine(machine)];
    with_sides(parts(0, 0, vec![], self::hosts(hosts)), sides)
}

fn many_callers() -> Parts {
    let callers = (0..7).map(|i| caller("batch", &format!("caller-{i}")));
    with_sides(parts(0, 1, vec![], hosts(1)), callers.collect())
}

fn drawn(parts: &Parts) -> (Layout, Vec<Link>) {
    let rows = rows(parts);
    let links = links(parts, &rows);
    (layout(&shapes(&rows, &links)), links)
}

#[rstest]
#[case::a_web_application(a_web_application())]
#[case::four_machines_and_a_device(a_stateful_set(4, &[2]))]
#[case::four_machines_each_with_a_device(a_stateful_set(4, &[1, 2, 3, 4]))]
#[case::five_machines_fold_under_their_devices(a_stateful_set(5, &[1, 5]))]
#[case::calls_a_machine(calls_a_machine())]
#[case::calls_its_own_machine(calls("machine-2", 3))]
#[case::many_callers(many_callers())]
fn no_two_boxes_overlap(#[case] parts: Parts) {
    let (layout, _) = drawn(&parts);
    let all = layout.all();
    for (i, a) in all.iter().enumerate() {
        for b in &all[i + 1..] {
            assert!(!a.overlaps(b), "{a:?} overlaps {b:?}");
        }
        assert!(a.x >= 0 && a.right() <= WIDTH, "{a:?} leaves the boxed");
    }
}

#[rstest]
#[case::a_web_application(a_web_application())]
#[case::four_machines_and_a_device(a_stateful_set(4, &[2]))]
#[case::four_machines_each_with_a_device(a_stateful_set(4, &[1, 2, 3, 4]))]
#[case::five_machines_fold_under_their_devices(a_stateful_set(5, &[1, 5]))]
#[case::calls_a_machine(calls_a_machine())]
#[case::calls_its_own_machine(calls("machine-2", 3))]
#[case::many_callers(many_callers())]
fn every_line_ends_on_its_boxes_edges(#[case] parts: Parts) {
    let (layout, links) = drawn(&parts);
    let lines = draw(&layout, &links).lines;
    assert_eq!(lines.len(), links.len());
    // `draw` gives the spine's lines first and the side lines after.
    let mut spine: Vec<&Link> = links
        .iter()
        .filter(|l| l.from.on_side().is_none() && l.to.on_side().is_none())
        .collect();
    spine.extend(
        links
            .iter()
            .filter(|l| l.from.on_side().is_some() || l.to.on_side().is_some()),
    );
    for (link, line) in spine.into_iter().zip(&lines) {
        let (from, to) = (
            layout.rect(link.from).unwrap(),
            layout.rect(link.to).unwrap(),
        );
        assert!(
            from.on_edge(line.from.x, line.from.y),
            "{link:?} starts off {from:?}"
        );
        assert!(to.on_edge(line.to.x, line.to.y), "{link:?} ends off {to:?}");
    }
}

#[rstest]
fn puts_each_layers_row_in_the_maps_order() {
    let bands: Vec<Band> = rows(&a_web_application())
        .into_iter()
        .map(|r| r.band)
        .collect();
    let layer = |name: &str| Band::Layer(name.into());
    assert_eq!(
        bands,
        vec![
            Band::Entry,
            layer("batch"),
            layer("apps"),
            layer("platform"),
            Band::Volumes,
            Band::Machines,
            Band::Ground,
        ]
    );
}

#[rstest]
#[case::a_web_application(
    a_web_application(),
    vec![
        wire("batch left.0", "apps 0.0", "depends on"),
        wire("apps left.0", "apps 0.0", "depends on"),
        wire("apps 0.0", "apps right.0", "depends on"),
        wire("apps 0.0", "platform right.0", "depends on"),
    ]
)]
#[case::a_device_hangs_off_its_machine(
    a_stateful_set(3, &[2]),
    vec![
        wire("apps left.0", "apps 0.0", "depends on"),
        wire("apps 0.0", "apps right.0", "depends on"),
        wire("Machines 0.1", "Machines right.0", "holds"),
    ]
)]
#[case::a_device_on_folded_machines(
    a_stateful_set(6, &[4]),
    vec![
        wire("apps left.0", "apps 0.0", "depends on"),
        wire("apps 0.0", "apps right.0", "depends on"),
        wire("Machines 0.0", "Machines right.0", "holds"),
    ]
)]
#[case::a_folded_column_joins_once(
    many_callers(),
    vec![
        wire("batch left.0", "apps 0.0", "depends on"),
        wire("batch left.1", "apps 0.0", "depends on"),
        wire("batch left.2", "apps 0.0", "depends on"),
        wire("batch left.3", "apps 0.0", "depends on"),
    ]
)]
fn wires_the_sides(#[case] parts: Parts, #[case] expected: Vec<(String, String, String)>) {
    let sides: Vec<(String, String, String)> = wiring(&parts)
        .into_iter()
        .filter(|(from, to, _)| {
            from.contains("left")
                || from.contains("right")
                || to.contains("left")
                || to.contains("right")
        })
        .collect();
    assert_eq!(sides, expected);
}

#[rstest]
#[case::another_machine(calls("printer", 3), "Machines right.0", 1)]
#[case::one_it_runs_on(calls("machine-2", 3), "Machines 0.1", 0)]
#[case::one_among_folded_machines(calls("machine-5", 6), "Machines 0.0", 0)]
fn reaches_a_machine_the_spine_draws_at_its_box(
    #[case] parts: Parts,
    #[case] reached: &str,
    #[case] side_boxes: usize,
) {
    let calls: Vec<(String, String, String)> = wiring(&parts)
        .into_iter()
        .filter(|(_, _, label)| label == "depends on")
        .collect();
    assert_eq!(calls, vec![wire("apps 0.0", reached, "depends on")]);
    let machines = rows(&parts).into_iter().find(|r| r.band == Band::Machines);
    assert_eq!(machines.unwrap().right.len(), side_boxes);
}

#[rstest]
fn a_fan_beside_a_device_keeps_clear_of_its_column() {
    let (layout, _) = drawn(&a_stateful_set(4, &[2]));
    let machines = &layout.rows[3];
    let fan = &machines.boxes[0];
    assert!(fan[3].right() < machines.right[0].x);
}

#[rstest]
fn inline_nfs_volumes_hang_off_the_machines_that_mount_them() {
    let backing = Backing {
        inline: vec!["movies".into(), "series".into()],
        ..mounted(nfs(&[]), &["machine-1"])
    };
    let parts = parts(0, 0, vec![backing], hosts(1));
    assert_eq!(
        wiring(&parts),
        vec![wire("Machines 0.0", "Ground 0.0", "NFS")]
    );
    assert_eq!(
        texts(&rows(&parts), Band::Ground),
        vec![text("nas.example.com", &["NFS: movies, series"])]
    );
}
