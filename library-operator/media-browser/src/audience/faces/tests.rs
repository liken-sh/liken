// The faces every screen draws: the circle each picture is cut to, the
// size it is decoded at, and the decode each face is spared on a second
// cut of an unchanged list.

use super::*;
use crate::audience::thumbnail::solid;

// A person with this picture, or none.
fn person(name: &str, thumbnail: Option<Thumbnail>) -> Person {
    Person {
        name: name.to_string(),
        display_name: name.to_string(),
        thumbnail,
    }
}

// The alpha of one pixel of a cut face.
fn alpha(face: &RgbaImage, x: u32, y: u32) -> u8 {
    face.get_pixel(x, y).0[3]
}

#[test]
fn a_face_is_clear_at_its_corners_and_opaque_at_its_centre() {
    let face = circle(&solid(256, [200, 40, 40]), 64).expect("the picture decodes");

    assert_eq!(face.dimensions(), (64, 64));
    for (x, y) in [(0, 0), (63, 0), (0, 63), (63, 63)] {
        assert_eq!(alpha(&face, x, y), 0, "the corner at {x},{y}");
    }
    assert_eq!(alpha(&face, 32, 32), 255);
    assert_eq!(alpha(&face, 32, 1), 255, "just inside the top edge");
}

// The edge of the circle is antialiased: a pixel the edge crosses takes
// the share of it the circle covers, so the rim does not step.
#[test]
fn the_edge_of_a_face_is_partly_clear() {
    let face = circle(&solid(256, [200, 40, 40]), 64).expect("the picture decodes");
    // Along the diagonal the edge crosses pixel 9 of 64: its centre is
    // 32 - 9.5 = 22.5 pixels out on each axis, 31.8 from the centre.
    let edge = alpha(&face, 9, 9);

    assert!(edge > 0 && edge < 255, "the edge pixel holds {edge}");
}

#[test]
fn a_face_keeps_the_colour_of_its_picture() {
    let face = circle(&solid(256, [200, 40, 40]), 64).expect("the picture decodes");
    let [red, green, blue, _] = face.get_pixel(32, 32).0;

    assert!(red > 180 && green < 70 && blue < 70, "{red},{green},{blue}");
}

// Bytes that open as base64 but hold no picture leave the person their
// letter.
#[test]
fn bytes_that_are_not_a_picture_cut_no_face() {
    let broken = Thumbnail::from_uri("data:image/jpeg;base64,aGVsbG8=").expect("the URI opens");

    assert!(circle(&broken, 64).is_none());
}

// The two sizes the tests cut at: a large circle and a small one, the
// way the picker and the strip draw the same person.
const LARGE: f32 = 152.0;
const SMALL: f32 = 26.0;

#[test]
fn a_face_is_decoded_at_the_panel_pixels_of_the_size_it_draws_at() {
    assert_eq!(side(LARGE, 1.0), 152);
    assert_eq!(side(SMALL, 1.5), 39);
}

#[test]
fn the_faces_follow_the_list_by_name_and_skip_a_person_with_none() {
    let people = [
        person("first", Some(solid(32, [200, 40, 40]))),
        person("second", None),
    ];
    let mut faces = Faces::default();

    faces.refresh(&people, &[LARGE], 1.0);

    let first = faces
        .get("first", LARGE)
        .expect("the first person has a face");
    assert_eq!(first.size(), (152, 152));
    assert!(faces.get("second", LARGE).is_none());
    assert!(faces.get("third", LARGE).is_none());
}

#[test]
fn a_person_has_a_face_at_each_size_the_cut_named_and_no_other() {
    let mut faces = Faces::default();

    faces.refresh(
        &[person("first", Some(solid(32, [200, 40, 40])))],
        &[LARGE, SMALL],
        1.0,
    );

    assert_eq!(
        faces.get("first", LARGE).expect("a large face").size(),
        (152, 152)
    );
    assert_eq!(
        faces.get("first", SMALL).expect("a small face").size(),
        (26, 26)
    );
    assert!(faces.get("first", 64.0).is_none());
}

#[test]
fn a_face_decoded_at_scale_two_draws_at_the_logical_size() {
    let mut faces = Faces::default();

    faces.refresh(
        &[person("first", Some(solid(32, [200, 40, 40])))],
        &[SMALL],
        2.0,
    );

    assert_eq!(faces.get("first", SMALL).expect("a face").size(), (26, 26));
}

// Bytes that do not decode leave the person their letter at every size.
#[test]
fn a_picture_that_does_not_decode_gives_no_face() {
    let broken = Thumbnail::from_uri("data:image/jpeg;base64,aGVsbG8=").expect("the URI opens");
    let mut faces = Faces::default();

    faces.refresh(&[person("first", Some(broken))], &[LARGE, SMALL], 1.0);

    assert!(faces.get("first", LARGE).is_none());
    assert!(faces.get("first", SMALL).is_none());
}

// The renderer keys an upload by the handle's id, and a decode makes a new
// id, so an equal image is the same decode, kept.
#[test]
fn a_second_cut_of_an_unchanged_list_decodes_nothing() {
    let people = [person("first", Some(solid(32, [200, 40, 40])))];
    let mut faces = Faces::default();
    faces.refresh(&people, &[LARGE, SMALL], 1.0);
    let before = (
        faces.get("first", LARGE).cloned(),
        faces.get("first", SMALL).cloned(),
    );

    faces.refresh(&people, &[LARGE, SMALL], 1.0);

    assert_eq!(faces.get("first", LARGE).cloned(), before.0);
    assert_eq!(faces.get("first", SMALL).cloned(), before.1);
}

// Two people who carry one picture share one decode at each size.
#[test]
fn one_picture_is_decoded_once_per_size() {
    let picture = solid(32, [200, 40, 40]);
    let mut faces = Faces::default();

    faces.refresh(
        &[
            person("first", Some(picture.clone())),
            person("second", Some(picture)),
        ],
        &[SMALL],
        1.0,
    );

    assert_eq!(faces.get("first", SMALL), faces.get("second", SMALL));
}

// A rename keeps the person's picture, so the face moves to the new name
// with no decode.
#[test]
fn a_renamed_person_keeps_the_decode_of_their_picture() {
    let picture = solid(32, [200, 40, 40]);
    let mut faces = Faces::default();
    faces.refresh(&[person("first", Some(picture.clone()))], &[SMALL], 1.0);
    let before = faces.get("first", SMALL).cloned();

    faces.refresh(&[person("renamed", Some(picture))], &[SMALL], 1.0);

    assert_eq!(faces.get("renamed", SMALL).cloned(), before);
    assert!(faces.get("first", SMALL).is_none());
}

#[test]
fn a_new_picture_or_a_new_scale_decodes_again() {
    let mut faces = Faces::default();
    faces.refresh(
        &[person("first", Some(solid(32, [200, 40, 40])))],
        &[SMALL],
        1.0,
    );
    let before = faces.get("first", SMALL).cloned();

    faces.refresh(
        &[person("first", Some(solid(32, [40, 40, 200])))],
        &[SMALL],
        1.0,
    );
    let repainted = faces.get("first", SMALL).cloned();
    faces.refresh(
        &[person("first", Some(solid(32, [40, 40, 200])))],
        &[SMALL],
        2.0,
    );

    assert!(repainted.is_some());
    assert_ne!(repainted, before);
    assert_ne!(faces.get("first", SMALL).cloned(), repainted);
}
