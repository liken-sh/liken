// The faces every screen draws: cut from each person's thumbnail when the
// browser learns the list or the panel's scale, at the size each screen
// draws them, and the letter for a person with no picture.

use super::*;
use crate::audience::Person;
use crate::audience::faces::{Face, side};
use crate::audience::thumbnail::solid;
use crate::screens::audience::FACE;
use crate::views::clock::strip::CIRCLE;

// A person with this name, their name in capitals as the display name,
// and this picture or none.
fn person(name: &str, colour: Option<[u8; 3]>) -> Person {
    Person {
        name: name.into(),
        display_name: name.to_uppercase(),
        thumbnail: colour.map(|colour| solid(32, colour)),
    }
}

// The browser over one person with a picture and one without, on a panel
// at this scale, after the first frame asked who is watching.
fn asked_at(scale: f32) -> Browser<Fake, NoArt> {
    let (browser, _bus) = on_bus(3, Vec::new());
    let people = vec![person("first", Some([200, 40, 40])), person("second", None)];
    let mut browser = browser.with_audience(people, Vec::new());
    browser.scaled((1920, 1080), scale);
    browser.tick(0.0);
    browser
}

#[test]
fn the_picker_draws_the_face_of_a_person_with_a_picture_and_the_letter_without() {
    let browser = asked_at(1.0);
    let layer = browser.picker_layer().expect("the picker is up");

    assert!(matches!(layer.face(0), Face::Picture(_)));
    assert_eq!(layer.face(1), Face::Letter("S".into()));
}

// The width in panel pixels of the one band a face draws as.
fn panel_width(image: &crate::art::Image) -> u32 {
    let (_, handle) = image
        .bands(views::area(0.0, 0.0, 1.0, 1.0))
        .next()
        .expect("the face draws as one band");
    let iced_widget::image::Handle::Rgba { width, .. } = handle else {
        panic!("a face is an Rgba handle");
    };
    width
}

// A face on a panel at scale two is cut at twice the pixels, and draws in
// the same logical square.
#[test]
fn a_face_is_cut_at_the_panel_scale() {
    let browser = asked_at(2.0);
    let layer = browser.picker_layer().expect("the picker is up");
    let Face::Picture(image) = layer.face(0) else {
        panic!("the first person has a picture");
    };

    assert_eq!(image.size(), (side(FACE, 1.0), side(FACE, 1.0)));
    assert_eq!(panel_width(image), side(FACE, 2.0));
}

// The browser with both people of `asked_at` in the room, after the
// first frame.
fn a_room_with_one_picture() -> Browser<Fake, NoArt> {
    let (browser, _bus) = on_bus(3, Vec::new());
    let people = vec![person("first", Some([200, 40, 40])), person("second", None)];
    let mut browser = browser.with_audience(people, vec!["first".into(), "second".into()]);
    browser.tick(0.0);
    browser
}

#[test]
fn the_strip_draws_the_face_of_a_person_with_a_picture_and_the_letter_without() {
    let browser = a_room_with_one_picture();
    let strip = browser.strip().expect("the strip draws");

    let Face::Picture(image) = strip.face(0) else {
        panic!("the first circle draws the letter");
    };
    assert_eq!(image.size(), (side(CIRCLE, 1.0), side(CIRCLE, 1.0)));
    assert_eq!(strip.face(1), Face::Letter("S".into()));
}

// The "?" of nobody names no `Person`, so it draws its mark and no face.
#[test]
fn the_question_mark_of_nobody_draws_no_face() {
    let (browser, _bus) = on_bus(3, Vec::new());
    let mut browser = browser.with_audience(vec![person("first", Some([200, 40, 40]))], Vec::new());
    browser.tick(0.0);
    browser.key("escape");

    let strip = browser.strip().expect("the strip draws");
    assert_eq!(strip.face(0), Face::Letter("?".into()));
}

// What the continue-watching row's heading draws for the person at this
// index of the room.
fn heading_face(browser: &Browser<Fake, NoArt>, index: usize) -> Face<'_> {
    let viewer = &super::resume::row(browser).viewers[index];
    browser.faces.face(viewer, CIRCLE)
}

// How many times the source read the continue-watching row.
fn continue_reads(browser: &Browser<Fake, NoArt>) -> usize {
    let calls = browser.source.calls.iter();
    calls.filter(|call| **call == "continue_watching").count()
}

// The row's heading holds each person's `Person` name, so a picture that
// arrives with a new list reaches the heading and the strip on the next
// frame, with no read of the row.
#[test]
fn a_new_picture_reaches_the_strip_and_the_rows_heading_with_no_read() {
    let (mut browser, _bus) = super::resume::watched(super::resume::plays(), false);
    assert_eq!(heading_face(&browser, 0), Face::Letter("F".into()));
    let reads = continue_reads(&browser);
    assert!(reads > 0, "the row was read before the new list");

    browser.relist(vec![Person {
        name: "first".into(),
        display_name: "First".into(),
        thumbnail: Some(solid(32, [200, 40, 40])),
    }]);
    browser.pump(1.0);

    assert!(matches!(heading_face(&browser, 0), Face::Picture(_)));
    let strip = browser.strip().expect("the strip draws");
    assert!(matches!(strip.face(0), Face::Picture(_)));
    assert_eq!(continue_reads(&browser), reads);
}

// A person whose picture the new list drops draws their letter again.
#[test]
fn a_dropped_picture_returns_the_letter_to_every_circle() {
    let mut browser = a_room_with_one_picture();

    browser.relist(vec![person("first", None), person("second", None)]);

    let strip = browser.strip().expect("the strip draws");
    assert_eq!(strip.face(0), Face::Letter("F".into()));
}

// One picture draws at two sizes, the picker's and the strip's, and each
// is cut once: a second list with the same picture keeps both decodes.
#[test]
fn a_picture_is_cut_once_for_each_size_it_draws_at() {
    let mut browser = a_room_with_one_picture();
    let strip = browser.faces.get("first", CIRCLE).cloned();
    let tile = browser.faces.get("first", FACE).cloned();
    assert!(strip.is_some() && tile.is_some());
    assert_ne!(strip, tile);

    browser.relist(vec![
        person("first", Some([200, 40, 40])),
        person("third", None),
    ]);

    assert_eq!(browser.faces.get("first", CIRCLE).cloned(), strip);
    assert_eq!(browser.faces.get("first", FACE).cloned(), tile);
}
