// The faces of the people under a real compositor: the picker's tiles,
// the strip's circles, and the continue-watching row's heading each draw
// a person's picture in place of the letter.

use iced_winit::core::{Point, Rectangle, Size};

use super::*;

// A thumbnail of one colour, as people-operator writes it: a square JPEG in
// a `data:` URI.
fn thumbnail(colour: [u8; 3]) -> String {
    use base64::Engine;

    let picture = image::RgbImage::from_pixel(256, 256, image::Rgb(colour));
    let mut bytes = Vec::new();
    image::codecs::jpeg::JpegEncoder::new_with_quality(&mut bytes, 85)
        .encode_image(&picture)
        .expect("a solid picture encodes");
    let data = base64::engine::general_purpose::STANDARD.encode(bytes);
    format!("data:image/jpeg;base64,{data}")
}

// Whether a pixel is the red of the pictures these runs draw. No part of
// the catalog fixture is this red: its poster is orange and its backdrop
// is blue.
fn red([red, green, blue]: [u8; 3]) -> bool {
    red > 180 && green < 80 && blue < 80
}

// The picker draws a person's picture in their tile and the letter in the
// tile of a person with none. The first tile's centre is the picture's
// red. The second tile's circle is the slot's grey below its letter.
#[test]
fn the_picker_draws_the_picture_of_a_person_who_has_one() {
    let dir = workspace("picker-faces");
    let frames = dir.join("frames");
    let people = dir.join("people.json");
    let list = serde_json::json!([
        {"name": "first", "displayName": "First", "thumbnail": thumbnail([220, 30, 30])},
        {"name": "second", "displayName": "Second"},
    ]);
    std::fs::write(&people, list.to_string()).expect("write the person list");

    let run = headless(
        &dir,
        &[
            "--people",
            &text(&people),
            "--capture",
            &text(&frames),
            "--capture-at",
            "1.0",
            "--size",
            "1920x1080",
            "--quit-after",
            "10",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    let path = frames.join("001.00.png");
    drawn(&path, &run);
    let pixels = image::open(&path)
        .unwrap_or_else(|error| panic!("{}: {error}\n{}", path.display(), run.log))
        .to_rgb8();
    let frame = Rectangle::new(Point::ORIGIN, Size::new(1920.0, 1080.0));
    let face = media_browser::screens::audience::tile(frame, 2, 0);
    let letter = media_browser::screens::audience::tile(frame, 2, 1);
    let [r, g, b] = pixels
        .get_pixel(face.center_x() as u32, face.center_y() as u32)
        .0;
    let slot = media_browser::look::slot();
    let under = pixels
        .get_pixel(letter.center_x() as u32, (letter.center_y() + 60.0) as u32)
        .0;
    let apart = |part: f32, drawn: u8| (part * 255.0 - f32::from(drawn)).abs();

    assert!(red([r, g, b]), "the face draws {r},{g},{b}\n{}", run.log);
    assert!(
        apart(slot.r, under[0]) <= 2.0
            && apart(slot.g, under[1]) <= 2.0
            && apart(slot.b, under[2]) <= 2.0,
        "the letter's tile draws {under:?}, and the slot is {slot:?}\n{}",
        run.log
    );
}

// The strip and the continue-watching row's heading draw the face of the
// person in the room. The run names the audience, so no picker goes up,
// and the progress store holds a play of theirs, so the home page draws
// the row. The strip's circle is at a place the layout names. The row's
// heading is under the band, so a red circle there is the heading's.
#[test]
fn the_strip_and_the_rows_heading_draw_the_picture_of_a_person_in_the_room() {
    let dir = workspace("room-faces");
    let frames = dir.join("frames");
    let (database, _volume) = fixture(&dir);
    let progress = super::progress::store(&dir, &database);
    let people = dir.join("people.json");
    let list = serde_json::json!([
        {"name": "first", "displayName": "First", "thumbnail": thumbnail([220, 30, 30])},
    ]);
    std::fs::write(&people, list.to_string()).expect("write the person list");

    let run = headless(
        &dir,
        &[
            "--catalog",
            &text(&database),
            "--updates",
            "http://127.0.0.1:1",
            "--progress",
            &text(&progress),
            "--people",
            &text(&people),
            "--audience",
            "first",
            "--capture",
            &text(&frames),
            "--capture-at",
            "1.5",
            "--size",
            "1920x1080",
            "--quit-after",
            "20",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    let path = frames.join("001.50.png");
    drawn(&path, &run);
    let pixels = image::open(&path)
        .unwrap_or_else(|error| panic!("{}: {error}\n{}", path.display(), run.log))
        .to_rgb8();
    let circle = media_browser::views::clock::strip::circle_at(1920.0, 1, 0);
    let centre = pixels
        .get_pixel(circle.center_x() as u32, circle.center_y() as u32)
        .0;
    let band = media_browser::views::band::HEIGHT as u32;
    let under = pixels
        .enumerate_pixels()
        .filter(|(_, y, pixel)| *y > band && red(pixel.0))
        .count();
    // A circle 26 pixels across holds about 530 pixels.
    let side = media_browser::views::clock::strip::CIRCLE;
    let area = (std::f32::consts::PI * side * side / 4.0) as usize;

    assert!(
        red(centre),
        "the strip's circle draws {centre:?}\n{}",
        run.log
    );
    assert!(
        under > area / 2 && under < 2 * area,
        "the frame under the band holds {under} red pixels, and one face holds about {area}\n{}",
        run.log
    );
}
