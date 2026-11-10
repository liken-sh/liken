// The ground a title's page lays under its rows, read off a capture of a
// page over a white backdrop. A white backdrop is the worst case for
// every caption under a card, so the ground has to hold its stated
// strength there. The renderer composites in linear light, so a ground of
// 0.9 leaves a tenth of the backdrop's light: white reads about 89 of 255
// under it, not 25.

use super::*;

// The rows' part of the frame that the scrim does not shade: right of
// the point where it clears, at 0.68 of the width, and low enough on a
// 1920 by 1080 frame to be under the head of either page at rest.
const RIGHT: (u32, u32, u32, u32) = (1320, 800, 1920, 1080);

// How far over the ground's stated share of the backdrop's light a
// pixel may read: the dither of the ramps and the rounding of a byte.
const TOLERANCE: f32 = 0.02;

// A movie's page whose cast runs the whole width of the frame lays the
// ground under the stripe, so the captions at its right end read on
// near-black and not on the white backdrop.
#[test]
fn a_movie_page_lays_the_ground_under_its_rows_over_a_white_backdrop() {
    let dir = workspace("ground-movie");
    let frames = dir.join("frames");
    let (database, volume) = fixture(&dir);
    white(&volume);
    cast(&database, "movie:path:one", 16);

    let run = page(&dir, &database, &volume, "drill/films", &frames);
    grounded(&frames.join("002.50.png"), &run);
}

// A series' page lays the ground under its episode wall at the same
// strength, across the whole width and under the rail.
#[test]
fn a_series_page_lays_the_ground_under_its_wall_over_a_white_backdrop() {
    let dir = workspace("ground-series");
    let frames = dir.join("frames");
    let (database, volume) = series_fixture(&dir);
    white(&volume);

    let run = page(&dir, &database, &volume, "drill/shows", &frames);
    grounded(&frames.join("002.50.png"), &run);
}

// The page of the one title of the fixture's one library, which the
// home page's first row offers, captured once the page has opened and
// its backdrop has landed.
fn page(dir: &Path, database: &Path, volume: &Path, library: &str, frames: &Path) -> Run {
    let run = headless(
        dir,
        &[
            "--catalog",
            &text(database),
            "--updates",
            "http://127.0.0.1:1",
            "--library-root",
            &format!("{library}={}", text(volume)),
            "--script",
            "0.5:enter",
            "--capture",
            &text(frames),
            "--capture-at",
            "2.5",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );
    assert_eq!(run.exit, "0", "{}", run.log);
    run
}

// The share of the pixels of `RIGHT` read, from the darkest. The
// fixture's art is black and its placeholders are near-black, so the
// cards read darker than the ground. The captions are the only pixels
// brighter than it, and they cover far less than the fifth of the box
// this leaves out, so the reading lands on the ground, or on the bare
// backdrop where the ground is missing.
const SHARE: f32 = 0.8;

// The light in `RIGHT`, at `SHARE` of its pixels from the darkest, is at
// most the share of the backdrop's light the ground leaves.
fn grounded(frame: &Path, run: &Run) {
    drawn(frame, run);
    let pixels = image::open(frame)
        .unwrap_or_else(|error| panic!("{}: {error}\n{}", frame.display(), run.log))
        .to_rgb8();
    let (left, top, right, bottom) = RIGHT;
    let mut light: Vec<f32> = (top..bottom)
        .flat_map(|y| (left..right).map(move |x| (x, y)))
        .map(|(x, y)| luminance(pixels.get_pixel(x, y).0))
        .collect();
    light.sort_by(f32::total_cmp);
    let read = light[(light.len() as f32 * SHARE) as usize];
    let leaves = 1.0 - media_browser::look::ground().a;
    assert!(
        read <= leaves + TOLERANCE,
        "{} reads {read:.3} of a white backdrop's light inside {RIGHT:?}, \
         and the ground leaves {leaves:.3}\n{}",
        frame.display(),
        run.log
    );
}

// The relative luminance of one pixel, in linear light.
fn luminance([r, g, b]: [u8; 3]) -> f32 {
    let linear = |channel: u8| {
        let c = f32::from(channel) / 255.0;
        match c <= 0.04045 {
            true => c / 12.92,
            false => ((c + 0.055) / 1.055).powf(2.4),
        }
    };
    0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b)
}

// The fixture's backdrop, white.
fn white(volume: &Path) {
    image::RgbImage::from_pixel(640, 360, image::Rgb([255, 255, 255]))
        .save(volume.join("backdrop.jpg"))
        .expect("write the white backdrop");
}

// This many invented actors credited on one title, so its cast stripe
// runs past the right edge of the frame.
fn cast(database: &Path, item: &str, count: u32) {
    let connection = rusqlite::Connection::open(database).expect("open the fixture database");
    for billing in 0..count {
        connection
            .execute(
                "INSERT INTO credits (library, item, billing, name, part, role) \
                 VALUES ('drill/films', ?, ?, ?, 'actor', 'A Part')",
                (item, billing, format!("Player {billing}")),
            )
            .expect("insert the fixture credit");
    }
}

// A catalog with one library of one series: two seasons of four
// episodes each, and a backdrop, a black poster, and a black still on
// the volume.
fn series_fixture(dir: &Path) -> (PathBuf, PathBuf) {
    let database = dir.join("state.db");
    let connection = rusqlite::Connection::open(&database).expect("open the fixture database");
    let schema = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../corrosion/schema/catalog.sql"
    );
    connection
        .execute_batch(&std::fs::read_to_string(schema).expect("read the schema"))
        .expect("apply the schema");
    connection
        .execute(
            "INSERT INTO series (library, id, kind, title, sort_key, released, art, body) \
             VALUES ('drill/shows', 'series:path:one', 'series', 'Harrow Fen', \
             'harrow fen', '2001', 'poster.jpg', '{\"plot\":\"A plot.\"}')",
            (),
        )
        .expect("insert the fixture series");
    for season in 1..=2 {
        for episode in 1..=4 {
            connection
                .execute(
                    "INSERT INTO episodes (library, id, kind, title, released, art, \
                     duration, series, season, episode) \
                     VALUES ('drill/shows', ?, 'episodes', ?, '2001-01-01', 'still.jpg', \
                     2700, 'series:path:one', ?, ?)",
                    (
                        format!("episode:{season}:{episode}"),
                        format!("Episode {episode}"),
                        season,
                        episode,
                    ),
                )
                .expect("insert the fixture episode");
        }
    }
    connection
        .execute(
            "INSERT INTO files (library, path, type, role) \
             VALUES ('drill/shows', 'backdrop.jpg', 'image', 'backdrop')",
            (),
        )
        .expect("insert the fixture backdrop");
    connection
        .execute(
            "INSERT INTO file_items (library, path, item) \
             VALUES ('drill/shows', 'backdrop.jpg', 'series:path:one')",
            (),
        )
        .expect("link the fixture backdrop");

    let volume = dir.join("volume");
    std::fs::create_dir_all(&volume).expect("the volume needs a directory");
    for name in ["poster.jpg", "still.jpg"] {
        image::RgbImage::from_pixel(320, 180, image::Rgb([0, 0, 0]))
            .save(volume.join(name))
            .expect("write the fixture art");
    }
    (database, volume)
}
