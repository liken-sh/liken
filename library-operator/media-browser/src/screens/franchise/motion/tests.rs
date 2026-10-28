// The franchise page's scroll under a press, and the outline's place at
// the end of it.

use super::*;
use crate::sample::Catalog;

const FRAME: Rectangle = Rectangle {
    x: 0.0,
    y: 0.0,
    width: 1920.0,
    height: 1080.0,
};

// The sample's saga, whose nine rows run past the foot of the frame, at
// rest on its first row.
fn on_the_first_row() -> Franchise {
    let mut page = Franchise::open(
        "sample/orders",
        "franchise:name:the-specimen-saga",
        &mut Catalog,
    )
    .expect("a sample franchise");
    page.follow(FRAME, 0.0, None);
    page
}

// Focus on the saga's last row, where a press that walks the story to its
// end puts it.
fn to_the_end(page: &mut Franchise) {
    page.focus = page.rows.len() - 1;
}

#[test]
fn a_press_that_leaves_the_view_eases_the_wall_to_the_row() {
    let mut page = on_the_first_row();
    let from = placed(&page, FRAME).settled;
    to_the_end(&mut page);
    let to = placed(&page, FRAME).settled;
    assert!(to > from, "{from} {to}");

    assert_eq!(page.follow(FRAME, 1.0, Some(1.0)), 1.0 + look::SCROLL);

    let half = page.scroll.toward(to, 1.0 + look::SCROLL / 2.0);
    assert!(from < half && half < to, "{half}");
    assert_eq!(page.scroll.toward(to, 1.0 + look::SCROLL), to);
}

#[test]
fn the_outline_goes_to_the_row_where_the_wall_comes_to_rest() {
    let mut page = on_the_first_row();
    to_the_end(&mut page);
    page.follow(FRAME, 1.0, Some(1.0));

    // The wall is still on its way, and the outline already goes to the
    // row's place at the end of the scroll.
    let at = |second: f64| {
        let placed = placed(&page, FRAME);
        let down = page.scroll.toward(placed.settled, second);
        let row = wall::cell_box(
            placed.lane.cards,
            page.focus,
            &page.headings,
            &placed.tops,
            down,
        );
        views::marked(row)
    };
    assert_ne!(page.marked(FRAME), Some(at(1.0 + look::SCROLL / 2.0)));
    assert_eq!(page.marked(FRAME), Some(at(1.0 + look::SCROLL)));
}

#[test]
fn a_page_with_no_rows_has_no_outline() {
    let mut page = on_the_first_row();
    page.rows.clear();

    assert_eq!(page.marked(FRAME), None);
}
