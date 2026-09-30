// The picker under a real compositor, with the unit's identity block at
// its bottom left. The block draws only from a status on the bus, so the
// run connects to the one-client broker and states a unit with one
// controller.

use iced_winit::core::{Color, Point, Rectangle, Size};

use super::*;

// The topic the operator names for the `Player`'s retained status.
const STATUS_TOPIC: &str = "liken/media/players/house/den/status";

// A unit with one controller at a full charge, which has the focus. The
// bar of a full charge is the line's own colour from end to end.
const STATUS: &str = r#"{"displayName":"The Den","activity":"Idle","components":[{"name":"Remote","kind":"remote","connected":true,"battery":100,"focused":true}]}"#;

// A point inside that bar in a 1920 by 1080 frame. The one part is on the
// bottom margin, 90 above the frame's bottom edge, and the bar is centered
// 12.6 above that line and ends 60 from the left edge, 45 long.
const BAR: (u32, u32) = (40, 977);

// The picker goes up on the first frame, because the run names people and
// no audience. The broker states the unit every quarter second, and the
// frame is taken well after the first one, because under the load of the
// whole suite the client can take seconds to connect.
#[test]
fn the_picker_draws_the_unit_the_status_names() {
    let dir = workspace("picker");
    let frames = dir.join("frames");
    let people = dir.join("people.json");
    std::fs::write(&people, r#"[{"name":"first","displayName":"First"}]"#)
        .expect("write the person list");
    let broker = broker::publishing(STATUS_TOPIC, STATUS);

    let run = headless_with(
        &dir,
        &[
            ("MEDIA_BUS_ADDRESS", &broker.address),
            ("MEDIA_PLAYER_NAME", "den"),
            ("MEDIA_PLAYER_STATUS_TOPIC", STATUS_TOPIC),
        ],
        &[
            "--people",
            &text(&people),
            "--capture",
            &text(&frames),
            "--capture-at",
            "4.0",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    let path = frames.join("004.00.png");
    drawn(&path, &run);
    let pixels = image::open(&path)
        .unwrap_or_else(|error| panic!("{}: {error}\n{}", path.display(), run.log))
        .to_rgb8();
    let (x, y) = BAR;
    let painted = pixels.get_pixel(x, y).0;
    let ink: Color = media_browser::look::text();
    let apart = |part: f32, drawn: u8| (part * 255.0 - f32::from(drawn)).abs();

    assert!(
        apart(ink.r, painted[0]) <= 1.0
            && apart(ink.g, painted[1]) <= 1.0
            && apart(ink.b, painted[2]) <= 1.0,
        "{} draws {painted:?} at {x},{y}, and the bar draws {ink:?} there\n{}",
        path.display(),
        run.log
    );
}

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
    let [red, green, blue] = pixels
        .get_pixel(face.center_x() as u32, face.center_y() as u32)
        .0;
    let slot = media_browser::look::slot();
    let under = pixels
        .get_pixel(letter.center_x() as u32, (letter.center_y() + 60.0) as u32)
        .0;
    let apart = |part: f32, drawn: u8| (part * 255.0 - f32::from(drawn)).abs();

    assert!(
        red > 180 && green < 80 && blue < 80,
        "the face draws {red},{green},{blue}\n{}",
        run.log
    );
    assert!(
        apart(slot.r, under[0]) <= 2.0
            && apart(slot.g, under[1]) <= 2.0
            && apart(slot.b, under[2]) <= 2.0,
        "the letter's tile draws {under:?}, and the slot is {slot:?}\n{}",
        run.log
    );
}
