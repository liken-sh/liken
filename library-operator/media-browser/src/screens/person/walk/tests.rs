// The presses across a person's page: between the axis and the posters,
// along each row, at the ends, on open, and over a re-read.

use super::*;
use crate::screens::person::credit::Seen;
use crate::screens::person::life::Life;
use crate::screens::person::tests::careers::{career, film};

// Seven credits over four years with credits: one in 1990, three in
// 2000, two in 2005, and one in 2010.
const YEARS: &[i64] = &[1990, 2000, 2000, 2000, 2005, 2005, 2010];

fn page() -> (Vec<Credit>, Axis) {
    let credits = career(YEARS);
    let axis = Axis::of(&credits, &Life::default(), 2026).expect("an axis");
    (credits, axis)
}

fn walk(focus: Focus, rest: Rest) -> Walk {
    Walk {
        focus,
        rest,
        left: None,
    }
}

// The walk after each of these presses, from a walk at rest on a poster.
fn pressed(from: usize, keys: &[&str]) -> (Walk, Vec<Pressed>) {
    let (credits, axis) = page();
    let mut walk = walk(Focus::Poster(from), Rest::Centered(from));
    let answers = keys
        .iter()
        .map(|key| walk.press(key, Some(&axis), credits.len()))
        .collect();
    (walk, answers)
}

#[test]
fn up_from_a_poster_goes_to_its_year_and_leaves_the_row_at_rest() {
    let (walk, answers) = pressed(5, &["up"]);
    assert_eq!(answers, [Pressed::Moved]);
    assert_eq!(walk.focus, Focus::Axis(2));
    assert_eq!(walk.rest, Rest::Centered(5));
}

#[test]
fn up_from_the_axis_moves_nothing_so_the_strip_takes_the_press() {
    let (walk, answers) = pressed(5, &["up", "up"]);
    assert_eq!(answers, [Pressed::Moved, Pressed::Still]);
    assert_eq!(walk.focus, Focus::Axis(2));
}

#[test]
fn up_then_down_returns_to_the_poster_focus_left() {
    for key in ["down", "enter"] {
        let (walk, _) = pressed(5, &["up", key]);
        assert_eq!(walk.focus, Focus::Poster(5), "{key}");
        assert_eq!(walk.rest, Rest::Centered(5), "{key}");
    }
}

#[test]
fn left_and_right_on_the_axis_step_through_the_years_with_credits() {
    let cases: [(&[&str], usize, usize); 4] = [
        (&["up", "left"], 1, 1),
        (&["up", "left", "left"], 0, 0),
        (&["up", "right"], 3, 6),
        (&["up", "left", "right"], 2, 4),
    ];
    for (keys, year, anchor) in cases {
        let (walk, _) = pressed(5, keys);
        assert_eq!(walk.focus, Focus::Axis(year), "{keys:?}");
        assert_eq!(walk.rest, Rest::Leading(anchor), "{keys:?}");
    }
}

#[test]
fn down_after_a_move_on_the_axis_lands_on_the_years_first_credit() {
    let (walk, answers) = pressed(5, &["up", "left", "down"]);
    assert_eq!(answers.last(), Some(&Pressed::Moved));
    assert_eq!(walk.focus, Focus::Poster(1));
    assert_eq!(walk.rest, Rest::Leading(1));
}

#[test]
fn left_and_right_on_the_posters_move_one_credit_and_center_it() {
    let (walk, _) = pressed(3, &["right"]);
    assert_eq!(walk.focus, Focus::Poster(4));
    assert_eq!(walk.rest, Rest::Centered(4));
    let (walk, _) = pressed(3, &["left", "left"]);
    assert_eq!(walk.focus, Focus::Poster(1));
}

#[test]
fn the_ends_of_both_rows_move_nothing() {
    let cases: [(usize, &[&str]); 5] = [
        (0, &["left"]),
        (6, &["right"]),
        (0, &["up", "left"]),
        (6, &["up", "right"]),
        (3, &["down"]),
    ];
    for (from, keys) in cases {
        let (_, answers) = pressed(from, keys);
        assert_eq!(answers.last(), Some(&Pressed::Still), "{from} {keys:?}");
    }
}

#[test]
fn select_on_a_poster_opens_it() {
    let (_, answers) = pressed(2, &["enter"]);
    assert_eq!(answers, [Pressed::Open(2)]);
}

#[test]
fn a_page_with_no_axis_has_one_focus_row() {
    let credits = career(&[2000, 2001, 2002]);
    let mut walk = walk(Focus::Poster(1), Rest::Centered(1));
    assert_eq!(walk.press("up", None, credits.len()), Pressed::Still);
    assert_eq!(walk.press("right", None, credits.len()), Pressed::Moved);
    assert_eq!(walk.focus, Focus::Poster(2));
}

#[test]
fn a_page_with_no_credits_moves_nothing() {
    let mut walk = Walk::opened(&[], None);
    assert_eq!(walk.press("enter", None, 0), Pressed::Still);
    assert_eq!(walk.press("right", None, 0), Pressed::Still);
}

// An undated credit has no year, so up goes to the newest year, and down
// returns to the credit.
#[test]
fn up_from_an_undated_credit_goes_to_the_newest_year() {
    let mut credits = career(YEARS);
    credits.push(film(9, None, Seen::Nobody));
    let axis = Axis::of(&credits, &Life::default(), 2026).expect("an axis");
    let mut walk = walk(Focus::Poster(7), Rest::Centered(7));
    walk.press("up", Some(&axis), credits.len());
    assert_eq!(walk.focus, Focus::Axis(3));
    walk.press("down", Some(&axis), credits.len());
    assert_eq!(walk.focus, Focus::Poster(7));
}

#[test]
fn a_page_opens_on_the_title_it_was_opened_from() {
    let (credits, _) = page();
    let via = credits[2].work();
    let walk = Walk::opened(&credits, Some(&via));
    assert_eq!(walk.focus, Focus::Poster(2));
    assert_eq!(walk.rest, Rest::Centered(2));
}

#[test]
fn a_page_opens_on_the_newest_dated_credit_with_the_row_at_its_end() {
    let mut credits = career(YEARS);
    credits.push(film(9, None, Seen::Nobody));
    let gone = ("screening/films".to_string(), "film:none".to_string());
    let walk = Walk::opened(&credits, Some(&gone));
    assert_eq!(walk.focus, Focus::Poster(6));
    assert_eq!(walk.rest, Rest::Leading(7));
}

// A read that adds an earlier credit moves every work one place along,
// and focus, the rest, and the poster left for the axis go with them.
#[test]
fn a_reread_keeps_focus_on_the_same_works() {
    let (credits, axis) = page();
    let mut walk = walk(Focus::Poster(5), Rest::Centered(4));
    walk.press("up", Some(&axis), credits.len());
    let held = walk.held(&credits, Some(&axis));

    let mut longer = vec![film(99, Some(1980), Seen::Nobody)];
    longer.extend(credits);
    let axis = Axis::of(&longer, &Life::default(), 2026).expect("an axis");
    walk.hold(&held, &longer, Some(&axis));
    assert_eq!(walk.focus, Focus::Axis(3));
    assert_eq!(walk.rest, Rest::Centered(5));
    walk.press("down", Some(&axis), longer.len());
    assert_eq!(walk.focus, Focus::Poster(6));
}

#[test]
fn a_reread_that_drops_the_focused_work_keeps_the_place() {
    let (credits, axis) = page();
    let walk_on = walk(Focus::Poster(6), Rest::Centered(6));
    let held = walk_on.held(&credits, Some(&axis));
    let shorter = career(&[1990, 2000, 2000, 2000, 2005]);
    let mut walk = walk_on;
    walk.hold(&held, &shorter, None);
    assert_eq!(walk.focus, Focus::Poster(4));
    assert_eq!(walk.rest, Rest::Centered(4));
}
