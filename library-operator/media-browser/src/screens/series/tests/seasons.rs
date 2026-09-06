// The seasons rail of a long series: the bars it draws, how a press
// moves between them and the stills, and the room it takes off the
// wall's right.

use super::super::{COLUMNS, Focus, Season, Series, layout, seasons};
use super::serials::{Serials, page, pressed, still};
use crate::catalog::Episode;
use crate::focus::Run;
use crate::look;
use crate::screens::Step;
use crate::views::{area, rail, text, wall};
use iced_winit::core::Rectangle;

// Five seasons of five, three, six, four, and seven episodes: the
// shortest series that draws a rail, filling two, one, two, one, and
// two rows at four across.
fn long() -> Serials {
    Serials {
        seasons: vec![5, 3, 6, 4, 7],
        ..Serials::default()
    }
}

// The page of that series.
fn longer() -> (Series, Serials) {
    page(long())
}

#[test]
fn a_series_of_four_seasons_or_fewer_draws_no_rail() {
    let (four, _) = page(Serials {
        seasons: vec![5, 3, 6, 4],
        ..Serials::default()
    });
    assert_eq!(four.seasons.len(), 4);
    assert!(four.bars.is_empty());

    let (five, _) = longer();
    assert_eq!(five.seasons.len(), 5);
    assert_eq!(five.bars.len(), 5);
}

#[test]
fn every_bar_names_its_season_and_stands_over_the_rows_it_fills() {
    let (screen, _) = longer();
    let named: Vec<&str> = screen.bars.iter().map(|bar| bar.label.as_str()).collect();
    assert_eq!(named, ["1", "2", "3", "4", "5"]);
    assert_eq!(screen.seasons[0].name, "Season 1 · 2004 · 5 episodes");
    let rows: Vec<(usize, usize)> = screen
        .bars
        .iter()
        .map(|bar| (bar.first, bar.last))
        .collect();
    assert_eq!(rows, [(0, 1), (2, 2), (3, 4), (5, 5), (6, 7)]);
    assert!(screen.bars.iter().all(|bar| bar.lane == 0));
}

#[test]
fn right_from_the_last_column_enters_the_rail_at_the_bar_over_that_row() {
    let (mut screen, mut source) = longer();
    screen.focus = Focus::Still(3);
    assert_eq!(pressed(&mut screen, &mut source, "right"), Focus::Rail(0));

    // The last still of a short row is at the wall's edge too.
    screen.focus = Focus::Still(7);
    assert_eq!(pressed(&mut screen, &mut source, "right"), Focus::Rail(1));

    screen.focus = Focus::Still(11);
    assert_eq!(pressed(&mut screen, &mut source, "right"), Focus::Rail(2));
}

#[test]
fn right_from_any_other_still_stays_on_the_wall() {
    let (mut screen, mut source) = longer();
    assert_eq!(still(pressed(&mut screen, &mut source, "right")), 1);

    let (mut short, mut source) = page(Serials::default());
    short.focus = Focus::Still(3);
    assert_eq!(still(pressed(&mut short, &mut source, "right")), 4);
}

#[test]
fn a_select_on_a_bar_lands_on_the_first_still_of_its_season() {
    let (mut screen, mut source) = longer();
    screen.focus = Focus::Rail(2);
    assert_eq!(pressed(&mut screen, &mut source, "enter"), Focus::Still(8));

    screen.focus = Focus::Rail(4);
    assert_eq!(pressed(&mut screen, &mut source, "enter"), Focus::Still(18));
}

#[test]
fn left_returns_to_the_still_the_rail_was_entered_from() {
    let (mut screen, mut source) = longer();
    screen.focus = Focus::Still(11);
    pressed(&mut screen, &mut source, "right");
    assert_eq!(pressed(&mut screen, &mut source, "left"), Focus::Still(11));
}

#[test]
fn left_from_a_bar_the_wall_did_not_enter_lands_on_its_first_still() {
    let (mut screen, mut source) = longer();
    screen.focus = Focus::Still(11);
    pressed(&mut screen, &mut source, "right");
    assert_eq!(pressed(&mut screen, &mut source, "down"), Focus::Rail(3));
    assert_eq!(pressed(&mut screen, &mut source, "left"), Focus::Still(14));
}

#[test]
fn up_and_down_move_along_the_bars_and_stop_at_the_ends() {
    let (mut screen, mut source) = longer();
    screen.focus = Focus::Rail(0);
    assert_eq!(pressed(&mut screen, &mut source, "up"), Focus::Rail(0));
    assert_eq!(pressed(&mut screen, &mut source, "down"), Focus::Rail(1));
    screen.focus = Focus::Rail(4);
    assert_eq!(pressed(&mut screen, &mut source, "down"), Focus::Rail(4));
    assert_eq!(pressed(&mut screen, &mut source, "up"), Focus::Rail(3));
}

#[test]
fn a_key_the_rail_does_not_answer_holds_the_bar() {
    let (mut screen, mut source) = longer();
    screen.focus = Focus::Rail(1);
    assert_eq!(pressed(&mut screen, &mut source, "search"), Focus::Rail(1));
    screen.focus = Focus::Rail(9);
    assert_eq!(pressed(&mut screen, &mut source, "left"), Focus::Rail(9));
}

#[test]
fn the_foot_keeps_the_still_the_wall_last_held_while_a_bar_has_focus() {
    let (mut screen, mut source) = longer();
    assert_eq!(still(pressed(&mut screen, &mut source, "right")), 1);
    assert_eq!(still(pressed(&mut screen, &mut source, "right")), 2);
    assert_eq!(still(pressed(&mut screen, &mut source, "right")), 3);
    assert_eq!(pressed(&mut screen, &mut source, "right"), Focus::Rail(0));

    assert!(screen.focused().is_none());
    let lines: Vec<String> = screen
        .foot
        .rows()
        .map(|row| row.content.to_string())
        .collect();
    assert_eq!(lines, ["A Studio", "1920×1080 · x264 · AAC · 4.0 GB"]);
}

#[test]
fn the_rail_takes_its_width_off_the_right_of_the_wall() {
    let region = area(0.0, 0.0, 1920.0, 1080.0);
    let (screen, _) = longer();
    let held = rail::beside_at(region, &screen.bars, rail::Side::Right);
    assert_eq!(held.x, region.x);
    assert_eq!(held.width, region.width - rail::LANE - rail::EDGE);

    let (short, _) = page(Serials::default());
    assert_eq!(
        rail::beside_at(region, &short.bars, rail::Side::Right),
        region
    );
}

#[test]
fn a_reread_that_leaves_fewer_seasons_returns_focus_to_the_wall() {
    let (mut screen, mut source) = longer();
    screen.focus = Focus::Rail(4);
    screen.reread(&mut source);
    assert_eq!(screen.focus, Focus::Rail(4));

    source.seasons = vec![5, 3, 6];
    screen.reread(&mut source);
    assert_eq!(screen.focus, Focus::Still(0));
}

#[test]
fn a_reread_that_drops_a_season_clamps_the_bar() {
    let (mut screen, mut source) = page(Serials {
        seasons: vec![5, 3, 6, 4, 7, 4],
        ..Serials::default()
    });
    screen.focus = Focus::Rail(5);
    source.seasons = long().seasons;
    screen.reread(&mut source);
    assert_eq!(screen.focus, Focus::Rail(4));
}

#[test]
fn a_still_whose_name_runs_past_its_cell_is_cut_at_the_read() {
    let band = wall::band(COLUMNS);
    let still = seasons::still_of(
        Episode {
            season: 1,
            episode: 2,
            title: "The Segment That Ran Long ".repeat(4),
            duration: 2_760,
            ..Episode::default()
        },
        "2026-09-04",
        band,
    );
    assert!(still.fitted.ends_with('\u{2026}'));
    assert!(crate::views::text::measured(&still.fitted, crate::look::CAPTION) <= band);
    assert_eq!(still.under, "E02 · 46m");
}

#[test]
fn a_series_with_no_season_at_all_draws_no_bar() {
    assert!(seasons::bars(&[], region()).is_empty());
}

// This many seasons of four episodes each, for a rail with fewer slots
// than seasons.
fn many(count: i64) -> Vec<Season> {
    (1..=count)
        .map(|number| Season {
            number,
            name: format!("Season {number} (2004)"),
            run: Run {
                first: (number as usize - 1) * 4,
                count: 4,
            },
        })
        .collect()
}

// The region a series page draws its rail in at 1080: the frame under
// the header.
fn region() -> Rectangle {
    layout::rail_region()
}

#[test]
fn more_seasons_than_the_region_fits_merge_into_ranges_that_cover_them_all() {
    let bars = seasons::bars(&many(28), region());
    assert_eq!(bars.len(), 7);
    assert_eq!(bars[0].label, "1\u{2013}4");
    assert_eq!(bars[6].label, "25\u{2013}28");
    assert_eq!((bars[0].first, bars[0].last), (0, 3));
    assert_eq!((bars[6].first, bars[6].last), (24, 27));
    // The bars leave no row of the wall uncovered.
    assert!(
        bars.windows(2)
            .all(|pair| pair[1].first == pair[0].last + 1)
    );
}

#[test]
fn a_dozen_seasons_take_a_bar_each_because_their_numbers_fit() {
    let bars = seasons::bars(&many(12), region());
    let named: Vec<&str> = bars.iter().map(|bar| bar.label.as_str()).collect();
    assert_eq!(
        named,
        [
            "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"
        ]
    );
}

#[test]
fn a_select_on_a_merged_bar_lands_on_the_first_still_of_its_first_season() {
    let (mut screen, mut source) = page(Serials {
        seasons: vec![4; 28],
        ..Serials::default()
    });
    screen.focus = Focus::Rail(1);
    let landed = pressed(&mut screen, &mut source, "enter");
    let first = 28 / screen.bars.len() * 4;
    assert_eq!(landed, Focus::Still(first));
}

#[test]
fn every_label_of_a_merged_rail_fits_the_slot_it_draws_in() {
    let region = region();
    let bars = seasons::bars(&many(28), region);
    let boxes = rail::fitted(region, &bars, rail::Side::Right);
    assert!(
        bars.iter()
            .zip(&boxes)
            .all(|(bar, at)| text::fits(look::HEADING, at.height) >= bar.label.chars().count()),
        "{:?}",
        bars.iter()
            .map(|bar| bar.label.as_str())
            .collect::<Vec<_>>()
    );
}

#[test]
fn up_from_the_first_bar_of_the_rail_moves_nothing() {
    let (mut screen, mut source) = longer();
    screen.focus = Focus::Rail(0);
    assert!(matches!(screen.key("up", &mut source), Step::Still));
    assert!(matches!(screen.key("down", &mut source), Step::Stay));
}

#[test]
fn up_from_the_first_row_of_stills_moves_nothing() {
    let (mut screen, mut source) = longer();
    assert!(matches!(screen.key("up", &mut source), Step::Still));
    screen.focus = Focus::Still(4);
    assert!(matches!(screen.key("up", &mut source), Step::Stay));
}

#[test]
fn up_from_the_topmost_focus_of_a_series_with_no_still_moves_nothing() {
    let (mut screen, mut source) = page(Serials {
        empty: true,
        credits: true,
        ..Serials::default()
    });
    screen.focus = Focus::Stripe(0, 0);
    assert!(matches!(screen.key("up", &mut source), Step::Still));
}

#[test]
fn the_wall_stands_at_the_still_the_focused_bar_lands_on() {
    let (mut screen, _) = longer();
    screen.focus = Focus::Rail(2);
    assert_eq!(seasons::standing(&screen), Focus::Still(8));
    screen.focus = Focus::Rail(9);
    assert_eq!(seasons::standing(&screen), Focus::Still(0));
    screen.focus = Focus::Still(3);
    assert_eq!(seasons::standing(&screen), Focus::Still(3));
}

#[test]
fn the_labels_of_a_long_rail_fit_a_window_shorter_than_the_screen() {
    let bars = seasons::bars(&many(18), region());
    let shorter = layout::region(area(0.0, 0.0, 1920.0, 1000.0));
    let boxes = rail::fitted(shorter, &bars, rail::Side::Right);
    assert!(
        bars.iter()
            .zip(&boxes)
            .all(|(bar, at)| text::fits(look::HEADING, at.height) >= bar.label.chars().count()),
        "{:?}",
        bars.iter()
            .map(|bar| bar.label.as_str())
            .collect::<Vec<_>>()
    );
}
