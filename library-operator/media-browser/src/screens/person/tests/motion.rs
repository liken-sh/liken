// A person's page in motion: each press moves the outline, the row, the
// bright part of the line, and the label once, on one length, and each
// lands where the row comes to rest.

use iced_winit::core::Rectangle;

use super::opened;
use super::people::People;
use crate::look;
use crate::screens::person::Person;
use crate::screens::person::page::{line, region};
use crate::screens::person::walk::Focus;
use crate::views;

const FRAME: Rectangle = Rectangle {
    x: 0.0,
    y: 0.0,
    width: 1920.0,
    height: 1080.0,
};

// A page of twenty credits, one a year, at rest on its newest one.
fn resting() -> (Person, People) {
    let (mut page, source) = opened(People {
        works: 20,
        born: "1960-01-01",
        ..People::default()
    });
    page.follow(FRAME, 0.0, None);
    (page, source)
}

#[test]
fn a_press_along_the_row_eases_it_to_its_new_rest() {
    let (mut page, mut source) = resting();
    page.key("left", &mut source);
    page.key("left", &mut source);
    page.key("left", &mut source);
    page.key("left", &mut source);
    page.follow(FRAME, 0.5, None);
    let from = page.row_at(FRAME, 0.5);

    page.key("left", &mut source);
    assert_eq!(page.follow(FRAME, 1.0, Some(1.0)), 1.0 + look::GLIDE);
    let to = page.row_at(FRAME, 1.0 + look::GLIDE);
    let half = page.row_at(FRAME, 1.0 + look::GLIDE / 2.0);
    assert!(to < half && half < from, "{from} {half} {to}");
}

#[test]
fn the_outline_goes_to_the_poster_where_the_row_comes_to_rest() {
    let (mut page, mut source) = resting();
    for _ in 0..6 {
        page.key("left", &mut source);
    }
    page.follow(FRAME, 1.0, Some(1.0));
    let Focus::Poster(index) = page.walk.focus else {
        panic!("a poster holds focus");
    };
    let at = |second: f64| {
        views::marked(crate::screens::person::row::slot(
            region(FRAME, true),
            index,
            page.row_at(FRAME, second),
        ))
    };
    assert_ne!(page.marked(FRAME), Some(at(1.0)));
    assert_eq!(page.marked(FRAME), Some(at(1.0 + look::GLIDE)));
}

#[test]
fn up_from_a_poster_moves_the_outline_and_nothing_else() {
    let (mut page, mut source) = resting();
    let row = page.row_at(FRAME, 0.0);
    let window = page.window_at(FRAME, 0.0);
    page.key("up", &mut source);
    page.follow(FRAME, 1.0, Some(1.0));

    assert_eq!(page.row_at(FRAME, 1.0), row);
    assert_eq!(page.window_at(FRAME, 1.0), window);
    let axis = page.axis.as_ref().expect("an axis");
    let Focus::Axis(year) = page.walk.focus else {
        panic!("the axis holds focus");
    };
    let column = axis.column(line(FRAME), year);
    assert_eq!(page.marked(FRAME), Some(views::marked(column)));
    let (words, middle) = page.label_at(FRAME, 1.0).expect("a label");
    assert_eq!(words, "1999 · 1 credit");
    assert_eq!(middle, column.center_x());
}

#[test]
fn a_press_along_the_axis_moves_the_row_the_window_and_the_label_as_one() {
    let (mut page, mut source) = resting();
    page.key("up", &mut source);
    page.follow(FRAME, 0.5, None);
    let row = page.row_at(FRAME, 0.5);
    let window = page.window_at(FRAME, 0.5).expect("a window");
    let label = page.label_at(FRAME, 0.5).expect("a label").1;

    for _ in 0..8 {
        page.key("left", &mut source);
    }
    let rests = page.follow(FRAME, 1.0, Some(0.5));
    assert_eq!(rests, 1.0 + look::GLIDE * 0.5);

    let half = 1.0 + look::GLIDE * 0.25;
    assert!(page.row_at(FRAME, half) < row);
    assert!(page.window_at(FRAME, half).expect("a window").0 < window.0);
    assert!(page.label_at(FRAME, half).expect("a label").1 < label);
    assert_eq!(page.row_at(FRAME, rests), page.row_at(FRAME, rests + 1.0));
}

#[test]
fn the_label_shows_only_while_the_axis_holds_focus() {
    let (page, _) = resting();
    assert_eq!(page.label_at(FRAME, 0.0), None);
}

#[test]
fn the_window_spans_the_years_the_row_shows_whole() {
    let (page, _) = resting();
    let axis = page.axis.as_ref().expect("an axis");
    let (left, right) = page.window_at(FRAME, 0.0).expect("a window");
    assert_eq!((left, right), axis.window(line(FRAME), (1994, 1999)));
}
