use rstest::rstest;

use super::*;

fn rect(x: i32, y: i32, width: i32, height: i32) -> Rect {
    Rect {
        x,
        y,
        width,
        height,
    }
}

/// One row for each count, each row a single tier of `counts[i]` boxes
/// of two lines.
fn rows(counts: &[usize]) -> Vec<Shape> {
    counts
        .iter()
        .map(|&count| Shape::tiers(vec![vec![2; count]]))
        .collect()
}

/// One tier of `count` boxes of two lines with `left` and `right` side
/// boxes of two lines beside it.
fn beside(count: usize, left: usize, right: usize) -> Shape {
    Shape {
        tiers: vec![vec![2; count]],
        left: vec![2; left],
        right: vec![2; right],
        crossed: Sides::default(),
    }
}

/// Whether any two boxes of `layout` overlap or touch.
fn overlapping(layout: &Layout) -> bool {
    let all = layout.all();
    let pairs = all
        .iter()
        .enumerate()
        .flat_map(|(i, a)| all[i + 1..].iter().map(move |b| (a, b)));
    pairs.into_iter().any(|(a, b)| a.overlaps(b))
}

#[rstest]
#[case::one_box_on_the_spine(1, vec![rect(376, 43, 240, 64)])]
#[case::two_backends(2, vec![rect(292, 43, 192, 64), rect(508, 43, 192, 64)])]
#[case::three_machines(3, vec![rect(184, 43, 192, 64), rect(400, 43, 192, 64), rect(616, 43, 192, 64)])]
#[case::four_machines(4, vec![rect(76, 43, 192, 64), rect(292, 43, 192, 64), rect(508, 43, 192, 64), rect(724, 43, 192, 64)])]
#[case::five_shrink_to_fit(5, vec![rect(26, 43, 169, 64), rect(219, 43, 169, 64), rect(412, 43, 169, 64), rect(605, 43, 169, 64), rect(798, 43, 169, 64)])]
fn centers_a_tier_on_the_spine(#[case] count: usize, #[case] expected: Vec<Rect>) {
    let found = layout(&rows(&[count]));
    assert_eq!(found.rows[0].boxes[0], expected);
}

#[rstest]
#[case::a_workload_with_no_ingress(&[1, 1, 1, 1], &[(0, 150), (160, 150), (320, 150), (480, 150)], 630)]
#[case::one_row(&[1], &[(0, 150)], 150)]
fn stacks_rows_with_a_gap(
    #[case] counts: &[usize],
    #[case] expected: &[(i32, i32)],
    #[case] height: i32,
) {
    let found = layout(&rows(counts));
    let tops: Vec<(i32, i32)> = found.rows.iter().map(|r| (r.top, r.height)).collect();
    assert_eq!((tops.as_slice(), found.height), (expected, height));
}

#[rstest]
fn stacks_the_tiers_of_a_row() {
    let entry = Shape::tiers(vec![vec![2], vec![2, 2]]);
    let found = layout(&[entry, Shape::tiers(vec![vec![2]])]);
    let row = &found.rows[0];
    assert_eq!(
        (row.boxes[0][0].y, row.boxes[1][0].y, row.height),
        (43, 179, 286)
    );
    assert_eq!(found.rows[1].top, 296);
}

#[rstest]
#[case::two_lines(2, 64)]
#[case::three_lines(3, 84)]
#[case::five_lines(5, 124)]
fn a_tier_is_as_tall_as_its_tallest_box(#[case] lines: usize, #[case] height: i32) {
    let found = layout(&[Shape::tiers(vec![vec![2, lines]])]);
    let heights: Vec<i32> = found.rows[0].boxes[0].iter().map(|r| r.height).collect();
    assert_eq!(heights, vec![height, height]);
}

#[rstest]
fn lays_out_the_same_section_the_same_way() {
    let boxed = vec![
        Shape::tiers(vec![vec![2], vec![2]]),
        beside(1, 2, 1),
        Shape::tiers(vec![vec![2, 2, 3]]),
    ];
    assert_eq!(layout(&boxed), layout(&boxed));
}

#[rstest]
fn finds_a_box_by_where_it_is() {
    let found = layout(&rows(&[1, 3]));
    let third = found.rect(At::new(1, 0, 2));
    assert_eq!(third, Some(rect(616, 203, 192, 64)));
    assert_eq!(found.rect(At::new(1, 0, 3)), None);
}

#[rstest]
#[case::a_caller_beside_the_workload(beside(1, 1, 0), vec![rect(72, 43, 192, 64)], vec![])]
#[case::one_on_each_side(beside(1, 1, 1), vec![rect(72, 43, 192, 64)], vec![rect(728, 43, 192, 64)])]
#[case::three_stack_around_the_middle(
    beside(1, 0, 3),
    vec![],
    vec![rect(728, 43, 192, 64), rect(728, 123, 192, 64), rect(728, 203, 192, 64)]
)]
fn places_side_boxes_in_their_columns(
    #[case] shape: Shape,
    #[case] left: Vec<Rect>,
    #[case] right: Vec<Rect>,
) {
    let found = layout(&[shape]);
    assert_eq!((&found.rows[0].left, &found.rows[0].right), (&left, &right));
}

#[rstest]
fn a_lone_box_sits_level_with_the_middle_of_its_side_column() {
    let found = layout(&[beside(1, 0, 3)]);
    let row = &found.rows[0];
    assert_eq!(row.boxes[0][0].middle(), row.right[1].middle());
    assert_eq!(row.height, 2 * 43 + 224);
}

#[rstest]
fn a_row_of_only_side_boxes_is_as_tall_as_its_column() {
    let found = layout(&[beside(0, 2, 0)]);
    assert_eq!(found.rows[0].height, 2 * 43 + 144);
}

#[rstest]
#[case::four_machines_and_a_device(beside(4, 0, 1), 16, 680)]
#[case::three_machines_and_a_device(beside(3, 0, 1), 56, 680)]
#[case::two_backends_and_a_caller(beside(2, 1, 0), 312, 728)]
#[case::four_between_two_sides(beside(4, 1, 1), 312, 680)]
fn a_fan_keeps_clear_of_a_side_column_that_holds_a_box(
    #[case] shape: Shape,
    #[case] left: i32,
    #[case] right: i32,
) {
    let found = layout(&[shape]);
    let tier = &found.rows[0].boxes[0];
    let ends = (tier[0].x, tier[tier.len() - 1].right());
    assert!(
        ends.0 >= left && ends.1 <= right,
        "the fan spans {ends:?}, want within {left}..{right}"
    );
    assert_eq!(ends.0, left);
    assert!(!overlapping(&found));
}

#[rstest]
fn a_fan_keeps_clear_of_a_column_a_line_crosses() {
    let crossed = Shape {
        crossed: Sides {
            left: false,
            right: true,
        },
        ..beside(4, 0, 0)
    };
    let found = layout(&[crossed]);
    let tier = &found.rows[0].boxes[0];
    assert!(tier[3].right() <= RIGHT_X - CLEAR);
}

#[rstest]
#[case::a_web_application(vec![Shape::tiers(vec![vec![2], vec![2]]), beside(1, 3, 3), beside(1, 0, 0)])]
#[case::four_machines_and_two_devices(vec![beside(1, 1, 2), beside(2, 0, 0), beside(4, 0, 2), beside(1, 0, 0)])]
#[case::six_machines_and_a_device(vec![beside(1, 0, 0), beside(6, 0, 1)])]
#[case::three_folded_columns(vec![beside(1, 4, 4)])]
fn no_two_boxes_overlap(#[case] boxed: Vec<Shape>) {
    assert!(!overlapping(&layout(&boxed)));
}
