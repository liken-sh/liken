use rstest::rstest;

use super::*;
use crate::map::section::layout::{Shape, layout};
use crate::map::section::lines::{Style, draw};

fn link(from: At, to: At, label: &str) -> Link {
    Link {
        from,
        to,
        label: label.into(),
        style: Style::Dashed,
        title: None,
    }
}

/// One tier of `count` boxes with `left` and `right` side boxes beside.
fn row(count: usize, left: usize, right: usize) -> Shape {
    Shape {
        tiers: vec![vec![2; count]],
        left: vec![2; left],
        right: vec![2; right],
        ..Shape::default()
    }
}

type Drawn = (Layout, Vec<Link>);

/// A workload with a caller on its left and what it depends on on its
/// right, both in its own row.
fn beside_the_workload() -> Drawn {
    let links = vec![
        link(At::side(0, Side::Left, 0), At::new(0, 0, 0), "depends on"),
        link(At::new(0, 0, 0), At::side(0, Side::Right, 0), "depends on"),
    ];
    (layout(&[row(1, 1, 1)]), links)
}

/// A workload with three boxes stacked on its right, each joined by
/// another kind of edge.
fn a_stack_of_three() -> Drawn {
    let labels = ["depends on", "reconciles from", "controlled by"];
    let links = (0..3)
        .map(|i| link(At::new(0, 0, 0), At::side(0, Side::Right, i), labels[i]))
        .collect();
    (layout(&[row(1, 0, 3)]), links)
}

/// A workload whose callers are in the rows above and below it, with
/// its GitOps object two rows down.
fn callers_in_other_layers() -> Drawn {
    let shapes = [row(0, 1, 0), row(1, 1, 0), row(0, 1, 1), row(0, 0, 1)];
    let links = vec![
        link(At::side(0, Side::Left, 0), At::new(1, 0, 0), "depends on"),
        link(At::side(1, Side::Left, 0), At::new(1, 0, 0), "depends on"),
        link(At::side(2, Side::Left, 0), At::new(1, 0, 0), "depends on"),
        link(At::new(1, 0, 0), At::side(2, Side::Right, 0), "depends on"),
        link(
            At::new(1, 0, 0),
            At::side(3, Side::Right, 0),
            "reconciles from",
        ),
    ];
    (layout(&shapes), links)
}

/// A StatefulSet on `machines` machines, each holding a device, under a
/// workload with a caller and a dependency.
fn machines_holding_devices(machines: usize, devices: usize) -> Drawn {
    let shapes = [row(1, 1, 1), row(1, 0, 0), row(machines, 0, devices)];
    let mut links = vec![
        link(At::side(0, Side::Left, 0), At::new(0, 0, 0), "depends on"),
        link(At::new(0, 0, 0), At::side(0, Side::Right, 0), "depends on"),
    ];
    links.extend((0..devices).map(|i| {
        let machine = machines - 1 - i % machines;
        link(At::new(2, 0, machine), At::side(2, Side::Right, i), "holds")
    }));
    (layout(&shapes), links)
}

#[rstest]
#[case::beside_the_workload(beside_the_workload())]
#[case::a_stack_of_three(a_stack_of_three())]
#[case::callers_in_other_layers(callers_in_other_layers())]
#[case::one_machine_and_a_device(machines_holding_devices(1, 1))]
#[case::four_machines_and_a_device(machines_holding_devices(4, 1))]
#[case::four_machines_and_three_devices(machines_holding_devices(4, 3))]
#[case::five_machines_and_two_devices(machines_holding_devices(5, 2))]
fn every_side_line_runs_from_edge_to_edge(#[case] given: Drawn) {
    let (layout, links) = given;
    let drawn = draw(&layout, &links);
    assert_eq!(drawn.lines.len(), links.len());
    for (link, line) in links.iter().zip(&drawn.lines) {
        let from = layout.rect(link.from).unwrap();
        let to = layout.rect(link.to).unwrap();
        assert!(
            from.on_edge(line.from.x, line.from.y),
            "{link:?} starts off {from:?}"
        );
        assert!(to.on_edge(line.to.x, line.to.y), "{link:?} ends off {to:?}");
        assert!(
            line.d
                .starts_with(&format!("M {} {} ", line.from.x, line.from.y))
        );
    }
}

#[rstest]
fn a_caller_level_with_the_workload_runs_straight_into_it() {
    let (layout, links) = beside_the_workload();
    let drawn = draw(&layout, &links);
    let paths: Vec<&str> = drawn.lines.iter().map(|l| l.d.as_str()).collect();
    assert_eq!(paths, vec!["M 264 75 L 372 75", "M 616 75 L 724 75"]);
}

#[rstest]
fn lines_into_one_edge_enter_at_points_spread_along_it() {
    let (layout, links) = a_stack_of_three();
    let drawn = draw(&layout, &links);
    let workload = layout.rect(At::new(0, 0, 0)).unwrap();
    let starts: Vec<(i32, i32)> = drawn.lines.iter().map(|l| (l.from.x, l.from.y)).collect();
    let quarter = workload.height / 4;
    assert_eq!(
        starts,
        vec![
            (616, workload.y + quarter),
            (616, workload.y + 2 * quarter),
            (616, workload.y + 3 * quarter),
        ]
    );
}

#[rstest]
fn callers_into_one_box_share_a_lane_and_an_entry() {
    let (layout, links) = callers_in_other_layers();
    let drawn = draw(&layout, &links);
    let ends: Vec<Point> = drawn.lines[..3].iter().map(|l| l.to).collect();
    assert!(ends.iter().all(|end| *end == ends[0]), "{ends:?}");
}

#[rstest]
fn a_bundle_carries_its_label_once() {
    let (layout, links) = callers_in_other_layers();
    let drawn = draw(&layout, &links);
    let texts: Vec<&str> = drawn.labels.iter().map(|l| l.text.as_str()).collect();
    assert_eq!(texts, vec!["depends on", "depends on", "reconciles from"]);
}

#[rstest]
fn a_machine_behind_another_takes_its_line_from_below() {
    let (layout, links) = machines_holding_devices(4, 2);
    let drawn = draw(&layout, &links);
    let behind = layout.rect(At::new(2, 0, 2)).unwrap();
    let line = &drawn.lines[3];
    assert_eq!(line.from.y, behind.bottom());
}

#[rstest]
fn the_farthest_line_takes_the_lane_nearest_the_spine() {
    let (layout, links) = callers_in_other_layers();
    let plans: Vec<Plan> = links.iter().filter_map(|l| plan(&layout, l)).collect();
    let lanes = lanes(&plans);
    assert!(lanes[4] < lanes[3], "{lanes:?}");
}

#[rstest]
fn labels_each_side_line_beside_its_side_box() {
    let (layout, links) = beside_the_workload();
    let drawn = draw(&layout, &links);
    let labels: Vec<(i32, i32, Anchor)> =
        drawn.labels.iter().map(|l| (l.x, l.y, l.anchor)).collect();
    assert_eq!(
        labels,
        vec![(272, 64, Anchor::Start), (720, 64, Anchor::End)]
    );
}

#[rstest]
fn a_line_that_turns_up_carries_its_label_below() {
    let (layout, links) = a_stack_of_three();
    let drawn = draw(&layout, &links);
    let lowest = layout.rect(At::side(0, Side::Right, 2)).unwrap();
    let ys: Vec<i32> = drawn.labels.iter().map(|l| l.y).collect();
    assert_eq!(ys[2], lowest.middle() + RISE);
}
