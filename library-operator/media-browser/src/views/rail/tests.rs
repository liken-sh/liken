use super::*;

fn bars() -> Vec<Bar> {
    vec![
        Bar {
            label: "The Long Survey".into(),
            first: 0,
            last: 7,
            lane: 0,
        },
        Bar {
            label: "The Coppice Years".into(),
            first: 4,
            last: 5,
            lane: 1,
        },
    ]
}

fn region() -> Rectangle {
    area(100.0, 200.0, 1720.0, 800.0)
}

#[test]
fn a_rail_takes_one_lane_for_every_lane_its_bars_fill() {
    assert_eq!(lanes(&bars()), 2);
    assert_eq!(width(&bars()), 2.0 * LANE);
    assert_eq!(lanes(&bars()[..1]), 1);
    assert_eq!(width(&[]), 0.0);
}

// The tops of this many rows of one height, and where the last ends.
fn tops(rows: usize, height: f32) -> Vec<f32> {
    (0..=rows).map(|row| row as f32 * height).collect()
}

#[test]
fn a_bar_deeper_than_the_last_lane_draws_in_the_last_lane() {
    let deep = [Bar {
        label: "Deeper".into(),
        first: 0,
        last: 1,
        lane: 4,
    }];
    assert_eq!(lanes(&deep), LANES);
    assert_eq!(
        bar(region(), &deep[0], &tops(8, 100.0), 0.0).x,
        region().x + LANE
    );
}

#[test]
fn a_bar_stands_over_the_rows_it_covers() {
    let rows = tops(8, 100.0);
    let survey = bar(region(), &bars()[0], &rows, 0.0);
    assert_eq!(survey.x, region().x);
    assert_eq!(survey.y, region().y);
    assert_eq!(survey.height, 8.0 * 100.0 - GAP);
    assert_eq!(survey.width, LANE - GAP);

    let years = bar(region(), &bars()[1], &rows, 0.0);
    assert_eq!(years.x, region().x + LANE);
    assert_eq!(years.y, region().y + 4.0 * 100.0);
    assert_eq!(years.height, 2.0 * 100.0 - GAP);
}

#[test]
fn a_bar_over_rows_of_two_heights_reaches_the_end_of_its_last() {
    let rows = [0.0, 300.0, 350.0, 650.0];
    let over = bar(region(), &bars()[0], &rows, 0.0);
    assert_eq!(over.height, 650.0 - GAP);
    let short = bar(
        region(),
        &Bar {
            first: 1,
            last: 1,
            ..Bar::default()
        },
        &rows,
        0.0,
    );
    assert_eq!(short.y, region().y + 300.0);
    assert_eq!(short.height, 50.0 - GAP);
    assert_eq!(bar(region(), &bars()[0], &[], 0.0).height, 0.0);
}

#[test]
fn a_scrolled_rail_moves_with_its_wall() {
    let scrolled = bar(region(), &bars()[1], &tops(8, 100.0), 250.0);
    assert_eq!(scrolled.y, region().y + 4.0 * 100.0 - 250.0);
}

#[test]
fn the_wall_stands_to_the_right_of_the_last_lane() {
    let wall = beside(region(), &bars());
    assert_eq!(wall.x, region().x + 2.0 * LANE);
    assert_eq!(wall.width, region().width - 2.0 * LANE);
    assert_eq!(wall.x + wall.width, region().x + region().width);
    assert_eq!(beside(region(), &[]), region());
}

#[test]
fn a_rail_at_the_right_takes_its_lanes_off_that_edge() {
    let region = region();
    let wall = beside_at(region, &bars(), Side::Right);
    assert_eq!(wall.x, region.x);
    assert_eq!(wall.width, region.width - 2.0 * LANE - EDGE);
    assert_eq!(
        beside_at(region, &bars(), Side::Left),
        beside(region, &bars())
    );
    assert_eq!(beside_at(region, &[], Side::Right), region);
}

#[test]
fn a_bar_at_the_right_stands_at_that_edge_and_counts_its_lanes_inward() {
    let region = region();
    let rows = tops(8, 100.0);
    let edge = region.x + region.width;
    let survey = bar_at(region, &bars()[0], &rows, 0.0, Side::Right);
    assert_eq!(survey.x + survey.width, edge - EDGE);
    assert_eq!(survey.width, LANE - GAP);
    assert_eq!(survey.y, region.y);
    assert_eq!(survey.height, 8.0 * 100.0 - GAP);

    let years = bar_at(region, &bars()[1], &rows, 0.0, Side::Right);
    assert_eq!(years.x + years.width, edge - EDGE - LANE);
    assert_eq!(years.y, region.y + 4.0 * 100.0);
    assert_eq!(
        bar_at(region, &bars()[0], &rows, 0.0, Side::Left),
        bar(region, &bars()[0], &rows, 0.0)
    );
}

#[test]
fn a_label_sits_in_the_middle_of_a_bar_the_region_holds_whole() {
    let region = region();
    let bar = area(region.x, region.y + 40.0, LANE, 200.0);
    let at = label_box(bar, region, 120.0);
    assert_eq!(at.y, bar.y + 40.0);
    assert_eq!(at.height, 120.0);
    assert_eq!(at.y + at.height / 2.0, bar.y + bar.height / 2.0);
}

#[test]
fn a_label_sits_in_the_middle_of_what_the_region_shows_of_its_bar() {
    let region = region();
    let bar = area(region.x, region.y - 3000.0, LANE, 9000.0);
    let at = label_box(bar, region, 120.0);
    assert_eq!(at.y + at.height / 2.0, region.y + region.height / 2.0);
}

#[test]
fn the_bottom_end_of_a_bar_pushes_its_label_off_with_it() {
    let region = region();
    let bar = area(region.x, region.y - 3000.0, LANE, 3060.0);
    let at = label_box(bar, region, 120.0);
    assert!(at.y + at.height <= bar.y + bar.height);
    assert!(at.y < region.y);
}

#[test]
fn a_label_whose_bar_has_ended_above_the_region_is_not_drawn_at_the_top() {
    let region = region();
    let bar = area(region.x, region.y - 3000.0, LANE, 2900.0);
    let at = label_box(bar, region, 120.0);
    assert_eq!(at.y + at.height, bar.y + bar.height);
    assert!(at.y + at.height < region.y);
}

#[test]
fn a_bar_shorter_than_its_label_carries_what_it_holds() {
    let region = region();
    let bar = area(region.x, region.y + 10.0, LANE, 60.0);
    let at = label_box(bar, region, 120.0);
    assert_eq!(at.y, bar.y);
    assert_eq!(at.height, bar.height);
}

#[test]
fn a_label_of_a_bar_over_a_few_rows_stays_inside_those_rows() {
    // A bar over rows 0 to 100 of a wall of 100-tall rows, with the
    // region over rows 40 to 43.
    let region = area(100.0, 200.0, 1720.0, 300.0);
    let bar = bar(
        region,
        &Bar {
            label: "The Long Saga".into(),
            first: 0,
            last: 100,
            lane: 0,
        },
        &tops(101, 100.0),
        4000.0,
    );
    let at = label_box(bar, region, 120.0);
    assert!(at.y >= region.y);
    assert!(at.y + at.height <= region.y + region.height);
}

#[test]
fn fitted_bars_take_equal_shares_of_the_region_and_never_move() {
    let region = region();
    let boxes = fitted(region, &bars(), Side::Right);
    let edge = region.x + region.width - EDGE;
    assert_eq!(boxes.len(), 2);
    assert_eq!(boxes[0].y, region.y);
    assert_eq!(boxes[0].height, region.height / 2.0 - GAP);
    assert_eq!(boxes[1].y, region.y + region.height / 2.0);
    assert_eq!(boxes[0].x + boxes[0].width, edge);
    assert_eq!(boxes[1].x + boxes[1].width, edge - LANE);
    assert_eq!(fitted(region, &bars()[..1], Side::Left)[0].x, region.x);
    assert!(fitted(region, &[], Side::Left).is_empty());
}

#[test]
fn the_boxes_of_a_rail_are_the_geometry_the_caller_names() {
    let region = region();
    let rows = tops(8, 100.0);
    let scrolled = Fit::Scrolled {
        tops: &rows,
        offset: 0.0,
    };
    assert_eq!(
        boxes(region, &bars(), Side::Left, scrolled),
        vec![
            bar(region, &bars()[0], &rows, 0.0),
            bar(region, &bars()[1], &rows, 0.0)
        ]
    );
    assert_eq!(
        boxes(region, &bars(), Side::Right, Fit::Fitted),
        fitted(region, &bars(), Side::Right)
    );
}

#[test]
fn a_move_onto_the_rail_lands_on_the_widest_bar_over_the_row() {
    assert_eq!(covering(&bars(), 0), Some(0));
    assert_eq!(covering(&bars(), 4), Some(0));
    assert_eq!(covering(&bars(), 9), None);
    assert_eq!(covering(&[], 0), None);
}
