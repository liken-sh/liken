// The faces the picker draws: cut from each person's thumbnail when the
// picker goes up, at the panel's scale, and the letter for a person with
// no picture.

use super::*;
use crate::audience::thumbnail::solid;
use crate::screens::audience::Face;
use crate::screens::audience::faces::side;

// The browser over one person with a picture and one without, on a panel
// at this scale, after the first frame asked who is watching.
fn asked_at(scale: f32) -> Browser<Fake, NoArt> {
    let (browser, _bus) = on_bus(3, Vec::new());
    let people = vec![
        crate::audience::Person {
            name: "first".into(),
            display_name: "First".into(),
            thumbnail: Some(solid(32, [200, 40, 40])),
        },
        crate::audience::Person {
            name: "second".into(),
            display_name: "Second".into(),
            thumbnail: None,
        },
    ];
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

// A face on a panel at scale two is cut at twice the pixels, and draws in
// the same logical square.
#[test]
fn a_face_is_cut_at_the_panel_scale() {
    let browser = asked_at(2.0);
    let layer = browser.picker_layer().expect("the picker is up");
    let Face::Picture(image) = layer.face(0) else {
        panic!("the first person has a picture");
    };

    let (_, handle) = image
        .bands(views::area(0.0, 0.0, 152.0, 152.0))
        .next()
        .expect("the face draws as one band");
    let iced_widget::image::Handle::Rgba { width, .. } = handle else {
        panic!("a face is an Rgba handle");
    };

    assert_eq!(image.size(), (side(1.0), side(1.0)));
    assert_eq!(width, side(2.0));
}
