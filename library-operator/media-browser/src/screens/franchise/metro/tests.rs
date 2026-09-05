// The strip's measures over invented rows: the runs it packs into lanes,
// its width, the colors, where the dots stand, and where a run's name
// draws beside its line.

use super::*;
use crate::catalog::franchise::Standing;
use crate::screens::franchise::wall::{Cell, HEAD, THIN, card_height};

fn row(universes: &[usize], held: bool) -> Row {
    Row {
        cell: Cell {
            universes: universes.to_vec(),
            standing: match held {
                true => Standing::Held,
                false => Standing::Missing,
            },
            ..Cell::default()
        },
        ..Row::default()
    }
}

// The names of this many universes, in the order a file would name them.
fn named(count: usize) -> Vec<String> {
    (0..count)
        .map(|index| format!("Universe {index}"))
        .collect()
}

// One row per entry of the story, each naming the universes it is in.
fn wall(story: &[&[usize]]) -> Vec<Row> {
    story.iter().map(|named| row(named, true)).collect()
}

// The lane, the first row, and the last row of every run, in the order
// the runs start.
fn laid(rows: &[Row], universes: usize) -> Vec<(usize, usize, usize, usize)> {
    runs(rows, &named(universes))
        .iter()
        .map(|run| (run.universe, run.first, run.last, run.lane))
        .collect()
}

#[test]
fn a_run_reaches_from_a_universes_first_row_to_its_last() {
    let rows = wall(&[&[0], &[1], &[0, 1, 2], &[0]]);
    assert_eq!(laid(&rows, 4), [(0, 0, 3, 0), (1, 1, 2, 1), (2, 2, 2, 2)]);
    assert!(runs(&[], &named(2)).is_empty());
    assert!(runs(&[row(&[9], true)], &named(1)).is_empty());
}

#[test]
fn a_run_that_has_ended_leaves_its_lane_to_the_next_one() {
    // The first universe ends on row 1 and the third starts on row 2, so
    // the third takes the first one's lane.
    let rows = wall(&[&[0, 1], &[0, 1], &[1, 2], &[1, 2]]);
    assert_eq!(laid(&rows, 3), [(0, 0, 1, 0), (1, 0, 3, 1), (2, 2, 3, 0)]);
    assert_eq!(lanes(&runs(&rows, &named(3))), 2);
}

#[test]
fn a_run_that_starts_where_another_ends_takes_a_lane_of_its_own() {
    let rows = wall(&[&[0], &[0, 1], &[1]]);
    assert_eq!(laid(&rows, 2), [(0, 0, 1, 0), (1, 1, 2, 1)]);
}

#[test]
fn seven_runs_alive_at_once_take_seven_lanes() {
    let all: Vec<usize> = (0..7).collect();
    let rows = wall(&[&all, &all]);
    assert_eq!(lanes(&runs(&rows, &named(7))), 7);
    assert_eq!(width(&runs(&rows, &named(7))), 7.0 * PITCH);
}

#[test]
fn twenty_universes_one_after_another_take_one_lane() {
    let story: Vec<Vec<usize>> = (0..20).map(|universe| vec![universe]).collect();
    let rows = wall(&story.iter().map(Vec::as_slice).collect::<Vec<_>>());
    let runs = runs(&rows, &named(20));
    assert_eq!(runs.len(), 20);
    assert!(runs.iter().all(|run| run.lane == 0));
    assert_eq!(width(&runs), PITCH);
}

#[test]
fn the_strip_takes_a_pitch_per_lane_and_nothing_for_one_run() {
    let one = runs(&wall(&[&[0], &[0]]), &named(1));
    assert_eq!(width(&one), 0.0);
    assert_eq!(lanes(&one), 1);
    assert_eq!(width(&[]), 0.0);
    assert_eq!(lanes(&[]), 0);
    let two = runs(&wall(&[&[0, 1]]), &named(2));
    assert_eq!(width(&two), 2.0 * PITCH);
}

#[test]
fn a_run_carries_the_name_of_its_own_universe() {
    let runs = runs(&wall(&[&[0], &[1]]), &named(2));
    assert_eq!(runs[0].name, "Universe 0");
    assert_eq!(runs[1].name, "Universe 1");
}

#[test]
fn a_line_stands_in_the_middle_of_its_pitch() {
    let strip = area(300.0, 0.0, 3.0 * PITCH, 900.0);
    assert_eq!(line_x(strip, 0), 300.0 + PITCH / 2.0);
    assert_eq!(line_x(strip, 2), 300.0 + 2.5 * PITCH);
}

#[test]
fn the_runs_take_the_palettes_hues_in_the_order_they_start() {
    let story: Vec<Vec<usize>> = (0..9).map(|universe| vec![universe]).collect();
    let rows = wall(&story.iter().map(Vec::as_slice).collect::<Vec<_>>());
    let hues: Vec<usize> = runs(&rows, &named(9)).iter().map(|run| run.hue).collect();
    assert_eq!(hues, [0, 1, 2, 3, 4, 5, 6, 7, 0]);
    assert_eq!(color(8), color(0));
}

#[test]
fn the_palettes_eight_hues_are_distinct_and_light_enough_on_black() {
    let colors: Vec<Color> = (0..HUES.len()).map(color).collect();
    for (index, one) in colors.iter().enumerate() {
        assert!(one.r + one.g + one.b > 1.5, "{one:?}");
        assert!(one.r <= 1.0 && one.g <= 1.0 && one.b <= 1.0);
        for other in &colors[index + 1..] {
            let apart = (one.r - other.r).abs() + (one.g - other.g).abs() + (one.b - other.b).abs();
            assert!(apart > 0.15, "{one:?} {other:?}");
        }
    }
    assert_eq!(color(0), oklch(LIGHTNESS, CHROMA, HUES[0]));
}

#[test]
fn a_dot_stands_in_the_middle_of_its_row_and_scrolls_with_the_wall() {
    let art = 100.0;
    let rows = vec![row(&[0], true), row(&[1], false)];
    let tops = crate::screens::franchise::wall::tops(&rows, art, HEAD);
    let strip = area(300.0, 200.0, 2.0 * PITCH, 900.0);
    assert_eq!(
        middle(strip, 0, &tops, 0.0),
        strip.y + HEAD + card_height(art) / 2.0
    );
    assert_eq!(
        middle(strip, 1, &tops, 50.0),
        strip.y + tops[1] + THIN / 2.0 - 50.0
    );
}

#[test]
fn a_bar_spans_the_lanes_the_rows_runs_occupy() {
    let strip = area(300.0, 200.0, 3.0 * PITCH, 900.0);
    let span = bar(strip, &[2, 0], 400.0, DOT).expect("a row of two lanes carries a bar");
    assert_eq!(span.x, line_x(strip, 0) - DOT);
    assert_eq!(span.x + span.width, line_x(strip, 2) + DOT);
    assert_eq!(span.center_y(), 400.0);
    assert_eq!(span.height, 2.0 * DOT);
}

#[test]
fn a_row_on_one_lane_draws_no_bar() {
    let strip = area(300.0, 200.0, 3.0 * PITCH, 900.0);
    assert_eq!(bar(strip, &[1], 400.0, DOT), None);
    assert_eq!(bar(strip, &[], 400.0, DOT), None);
}

#[test]
fn a_row_takes_a_dot_on_the_run_of_every_universe_it_names() {
    let rows = wall(&[&[0, 1], &[0, 1], &[1, 2], &[1, 2]]);
    let runs = runs(&rows, &named(3));
    let lanes: Vec<usize> = dotted(&runs, &rows[2]).iter().map(|run| run.lane).collect();
    // The third universe took the first one's lane, so the third row's
    // dots stand on lanes one and zero.
    assert_eq!(lanes, [1, 0]);
    assert!(dotted(&runs, &row(&[9], true)).is_empty());
}

// The strip of a wall of rows of one height, and where every row starts.
fn strip(rows: usize) -> (Rectangle, Vec<f32>) {
    let tops: Vec<f32> = (0..=rows).map(|row| HEAD + row as f32 * 200.0).collect();
    (area(300.0, 100.0, 3.0 * PITCH, 900.0), tops)
}

fn run(first: usize, last: usize, lane: usize) -> Run {
    Run {
        name: "A Universe".into(),
        universe: lane,
        first,
        last,
        lane,
        hue: 0,
    }
}

#[test]
fn a_name_stands_over_its_first_dot_in_the_gutter_beside_its_line() {
    let (strip, tops) = strip(8);
    let box_of = name_box(strip, &run(1, 5, 1), &tops, 0.0, 120.0).expect("the dot is on screen");
    assert_eq!(
        box_of.y + box_of.height,
        middle(strip, 1, &tops, 0.0) - DOT - NAME_GAP
    );
    assert_eq!(box_of.height, 120.0);
    assert_eq!(box_of.center_x(), name_x(strip, 1));
}

#[test]
fn a_name_covers_no_dot_of_its_own_lane_and_none_of_the_next() {
    let (strip, tops) = strip(8);
    for lane in 0..3 {
        let box_of =
            name_box(strip, &run(1, 5, lane), &tops, 0.0, 120.0).expect("the dot is on screen");
        assert!(box_of.x >= line_x(strip, lane) + DOT, "lane {lane}");
        assert!(
            box_of.x + box_of.width <= line_x(strip, lane + 1) - DOT,
            "lane {lane}"
        );
    }
}

#[test]
fn a_name_whose_first_dot_is_above_the_strip_holds_the_top_of_it() {
    let (strip, tops) = strip(8);
    let box_of = name_box(strip, &run(0, 7, 0), &tops, 900.0, 120.0).expect("the run is on screen");
    assert!(middle(strip, 0, &tops, 900.0) < strip.y);
    assert_eq!(box_of.y, strip.y);
    assert_eq!(box_of.height, 120.0);
}

#[test]
fn a_run_whose_first_dot_is_below_the_strip_draws_no_name() {
    let (strip, tops) = strip(8);
    assert!(middle(strip, 7, &tops, 0.0) > strip.y + strip.height);
    assert_eq!(name_box(strip, &run(7, 8, 0), &tops, 0.0, 120.0), None);
    // The dot arrives at the foot of the strip, and the name arrives
    // with it.
    let down = middle(strip, 7, &tops, 0.0) - (strip.y + strip.height);
    assert!(name_box(strip, &run(7, 8, 0), &tops, down, 120.0).is_some());
}

#[test]
fn a_run_that_has_scrolled_over_the_head_of_the_strip_draws_no_name() {
    let (strip, tops) = strip(8);
    assert!(middle(strip, 2, &tops, 900.0) < strip.y);
    assert_eq!(name_box(strip, &run(0, 2, 0), &tops, 900.0, 120.0), None);
}

#[test]
fn the_last_dot_of_a_run_pushes_its_name_off_with_it() {
    let (strip, tops) = strip(8);
    let box_of = name_box(strip, &run(0, 2, 0), &tops, 400.0, 120.0).expect("the run is on screen");
    assert_eq!(box_of.y + box_of.height, middle(strip, 2, &tops, 400.0));
    assert!(box_of.y < strip.y);
}

#[test]
fn black_encodes_as_black_and_white_as_white() {
    let black = oklch(0.0, 0.0, 0.0);
    assert!(
        black.r < 0.01 && black.g < 0.01 && black.b < 0.01,
        "{black:?}"
    );
    let white = oklch(1.0, 0.0, 0.0);
    assert!(
        white.r > 0.99 && white.g > 0.99 && white.b > 0.99,
        "{white:?}"
    );
}
