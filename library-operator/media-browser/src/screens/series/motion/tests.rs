// The series page's scrolls under a press: the wall down to the next
// season, and the outline's place at the end of it, in the wall and in
// the header that never scrolls.

use super::*;
use crate::sample::Catalog;

const FRAME: Rectangle = Rectangle {
    x: 0.0,
    y: 0.0,
    width: 1920.0,
    height: 1080.0,
};

// The sample's first serial, at rest with focus on this rung. Its first
// season fills two rows of stills, so a press down from the second row
// crosses into the next season and scrolls the wall.
fn on(focus: Focus) -> Series {
    let mut page =
        Series::open("sample/serials", "series:sample:01", &mut Catalog).expect("a sample serial");
    page.focus = focus;
    page.follow(FRAME, 0.0, None);
    page
}

// The focused still as the wall draws it at this scroll down.
fn drawn_still(page: &Series, offset: f32) -> Option<Rectangle> {
    let placed = placed(page, FRAME);
    let season = page
        .seasons
        .iter()
        .position(|season| season.run.first > 0)
        .expect("a second season");
    wall::focused(&stills(page, &placed, season, offset, Some(page.focus)))
}

#[test]
fn a_press_down_eases_the_wall_into_the_next_season() {
    let mut page = on(Focus::Still(4));
    let from = placed(&page, FRAME).offset;
    page.key("down", &mut Catalog);
    let to = placed(&page, FRAME).offset;
    assert!(to > from, "{from} {to}");

    assert_eq!(page.follow(FRAME, 1.0, Some(1.0)), 1.0 + look::SCROLL);

    let half = page.scroll.toward(to, 1.0 + look::SCROLL / 2.0);
    assert!(from < half && half < to, "{half}");
    assert_eq!(page.scroll.toward(to, 1.0 + look::SCROLL), to);
}

#[test]
fn the_outline_goes_to_the_still_where_the_wall_comes_to_rest() {
    let mut page = on(Focus::Still(4));
    page.key("down", &mut Catalog);
    page.follow(FRAME, 1.0, Some(1.0));

    // The wall is still on its way, and the outline already goes to the
    // still's place at the end of the scroll.
    let at = |second: f64| {
        let offset = page.scroll.toward(placed(&page, FRAME).offset, second);
        drawn_still(&page, offset).map(views::marked)
    };
    assert_ne!(page.marked(FRAME), at(1.0 + look::SCROLL / 2.0));
    assert_eq!(page.marked(FRAME), at(1.0 + look::SCROLL));
}

#[test]
fn the_outline_goes_into_the_header_for_an_episodes_row_and_its_marks() {
    let mut page = on(Focus::Still(0));
    let still = page.marked(FRAME).expect("the wall holds a still");
    page.key("enter", &mut Catalog);
    let button = page.marked(FRAME).expect("the row holds a button");
    page.key("down", &mut Catalog);
    let mark = page.marked(FRAME).expect("the line holds a mark");

    assert!(
        matches!(page.focus, Focus::EpisodeMark(0, 0)),
        "{:?}",
        page.focus
    );
    assert!(button.y + button.height < still.y, "{button:?} {still:?}");
    assert!(mark.y > button.y + button.height, "{button:?} {mark:?}");
    assert!(mark.y + mark.height < still.y, "{mark:?} {still:?}");
}

// The cast's stripe stands under the last season, so focus there scrolls
// the wall up until the headshot is in view under the header.
#[test]
fn the_outline_goes_around_a_headshot_of_the_cast_under_the_seasons() {
    let page = on(Focus::Stripe(0, 2));
    let region = placed(&page, FRAME).region;

    let headshot = page.marked(FRAME).expect("the stripe holds a headshot");

    assert!(placed(&page, FRAME).offset > 0.0);
    assert!(headshot.y > region.y, "{headshot:?} {region:?}");
    assert!(headshot.y + headshot.height < FRAME.height, "{headshot:?}");
}
