//! The display crops a trickplay tile from a Jellyfin sprite sheet, so a scrub
//! shows the frame at the target time. The library precomputes the sheets, so
//! the playback machine decodes no second video stream for a thumbnail. The
//! scrub time maps to a tile, and the display reads the geometry off the sheet,
//! crops the cell, and scales it to the box.

use std::path::Path;
use std::sync::Mutex;

use image::DynamicImage;

use super::bitmap::Bitmap;
use super::decode;

/// How long one tile covers. The operator writes the Play's own interval into
/// the pod, and an unset, unreadable, or sub-millisecond value falls back to
/// Jellyfin's own default, because the tile mapping divides by this value.
pub const INTERVAL_VARIABLE: &str = "MEDIA_TRICKPLAY_INTERVAL";
const DEFAULT_INTERVAL_MS: i64 = 10_000;

/// The sheets on disk, one JPEG per grid of tiles.
const SHEET_SUFFIX: &str = ".jpg";

/// The pod's interval as milliseconds.
pub fn interval_ms() -> i64 {
    std::env::var(INTERVAL_VARIABLE)
        .ok()
        .as_deref()
        .and_then(duration_ms)
        .filter(|milliseconds| *milliseconds >= 1)
        .unwrap_or(DEFAULT_INTERVAL_MS)
}

/// One duration as the operator writes it, which is Go's own spelling: a run
/// of signed decimal numbers, each with a unit, such as `10s` or `1m30s`.
fn duration_ms(text: &str) -> Option<i64> {
    let (text, sign) = match text.strip_prefix('-') {
        Some(rest) => (rest, -1.0),
        None => (text.strip_prefix('+').unwrap_or(text), 1.0),
    };
    if text.is_empty() {
        return None;
    }
    let mut rest = text;
    let mut milliseconds = 0.0;
    while !rest.is_empty() {
        let digits = rest
            .find(|letter: char| !letter.is_ascii_digit() && letter != '.')
            .filter(|end| *end > 0)?;
        let value: f64 = rest[..digits].parse().ok()?;
        rest = &rest[digits..];
        let unit = UNITS
            .iter()
            .find(|(name, _)| rest.starts_with(name))
            .map(|(name, scale)| {
                rest = &rest[name.len()..];
                scale
            })?;
        milliseconds += value * unit;
    }
    Some((sign * milliseconds) as i64)
}

/// Every unit Go's own duration spells, longest name first so `ms` is read
/// before `m`.
const UNITS: [(&str, f64); 7] = [
    ("ns", 1e-6),
    ("us", 1e-3),
    ("\u{00B5}s", 1e-3),
    ("ms", 1.0),
    ("s", 1000.0),
    ("m", 60_000.0),
    ("h", 3_600_000.0),
];

/// The one decoded sheet the display holds. A scrub stays within one sheet for
/// a long stretch, one hundred tiles at the interval, so holding one sheet is
/// what keeps a scrub off the disk.
#[derive(Debug, Default)]
pub struct Sheets {
    key: String,
    sheet: Option<DynamicImage>,
}

/// Map the cursor time to a tile, read the sheet geometry off disk, crop the
/// cell, and scale it to the box. A directory with no layout inside it, or a
/// sheet the display cannot read, crops nothing.
pub fn tile(dir: &str, ms: i64, box_w: u32, box_h: u32, sheets: &Mutex<Sheets>) -> Option<Bitmap> {
    let index = ms / interval_ms();
    let (name, tile_w, cols, rows) = find_layout(dir)?;
    let layout = Path::new(dir).join(name);

    let per_sheet = cols * rows;
    let cell = index % per_sheet;
    let row = cell / cols;
    let col = cell % cols;
    // A time past the end of the film maps past the last sheet. The highest
    // sheet on disk is the last one, so clamp to it.
    let sheet = (index / per_sheet).min(highest_sheet(&layout));

    let held = sheet_image(&layout, sheet, sheets)?;
    // The tile height is the sheet height over the rows. It is the film's
    // aspect, so a scope film gives a short wide tile, not a 16:9 one.
    let tile_h = held.height() / rows as u32;
    if tile_h == 0 {
        return None;
    }
    let region = held.crop_imm(col as u32 * tile_w, row as u32 * tile_h, tile_w, tile_h);
    decode::scale(&region, box_w, box_h)
}

/// The held sheet when the crop names it, and a sheet decoded once and held
/// otherwise.
fn sheet_image(layout: &Path, sheet: i64, sheets: &Mutex<Sheets>) -> Option<DynamicImage> {
    let key = format!("{}:{sheet}", layout.to_string_lossy());
    if let Ok(held) = sheets.lock()
        && held.key == key
        && let Some(sheet) = &held.sheet
    {
        return Some(sheet.clone());
    }

    let path = layout.join(format!("{sheet}{SHEET_SUFFIX}"));
    let decoded = decode::read(&std::fs::read(path).ok()?)?;

    if let Ok(mut held) = sheets.lock() {
        held.key = key;
        held.sheet = Some(decoded.clone());
    }
    Some(decoded)
}

/// The one layout directory inside a trickplay directory. Its name carries the
/// tile width and the grid, like `320 - 10x10`, so the display reads the
/// geometry from the name and opens no sheet to learn it.
fn find_layout(dir: &str) -> Option<(String, u32, i64, i64)> {
    let mut names: Vec<String> = std::fs::read_dir(dir)
        .ok()?
        .flatten()
        .filter(|entry| entry.file_type().is_ok_and(|kind| kind.is_dir()))
        .map(|entry| entry.file_name().to_string_lossy().to_string())
        .collect();
    names.sort();
    names.into_iter().find_map(|name| {
        let (tile_w, cols, rows) = parse_layout(&name)?;
        Some((name, tile_w, cols, rows))
    })
}

/// One layout name, as the tile width, the columns, and the rows.
fn parse_layout(name: &str) -> Option<(u32, i64, i64)> {
    let (left, right) = name.split_once(" - ")?;
    let tile_w: u32 = left.trim().parse().ok()?;
    let (columns, lines) = right.trim().split_once('x')?;
    let cols: i64 = columns.parse().ok()?;
    let rows: i64 = lines.parse().ok()?;
    (tile_w > 0 && cols > 0 && rows > 0).then_some((tile_w, cols, rows))
}

/// The highest N among the N.jpg sheets. A time past the end of the film
/// clamps to it.
fn highest_sheet(dir: &Path) -> i64 {
    std::fs::read_dir(dir)
        .into_iter()
        .flatten()
        .flatten()
        .filter(|entry| !entry.file_type().is_ok_and(|kind| kind.is_dir()))
        .filter_map(|entry| {
            entry
                .file_name()
                .to_string_lossy()
                .strip_suffix(SHEET_SUFFIX)?
                .parse::<i64>()
                .ok()
                .filter(|number| *number >= 0)
        })
        .max()
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::super::decode::tests::{centre, encoded, encoded_from};
    use super::*;
    use image::{ImageFormat, Rgba, RgbaImage};
    use std::path::PathBuf;

    /// The sheet every crop test reads: a 32 by 32 JPEG of four 16 by 16
    /// cells, laid out the way a sprite sheet lays them out, cell 0 top left
    /// and cell 3 bottom right.
    const CELLS: [[u8; 4]; 4] = [
        [200, 0, 0, 255],
        [0, 200, 0, 255],
        [0, 0, 200, 255],
        [200, 200, 0, 255],
    ];

    fn sheet() -> Vec<u8> {
        let mut picture = RgbaImage::new(32, 32);
        for (cell, color) in CELLS.iter().enumerate() {
            let (col, row) = (cell as u32 % 2, cell as u32 / 2);
            for y in 0..16 {
                for x in 0..16 {
                    picture.put_pixel(col * 16 + x, row * 16 + y, Rgba(*color));
                }
            }
        }
        encoded_from(&picture, ImageFormat::Jpeg)
    }

    /// One trickplay directory: a layout directory of the stated name, holding
    /// the stated sheets in order.
    fn trickplay(name: &str, layout: &str, sheets: &[Vec<u8>]) -> PathBuf {
        let dir = std::env::temp_dir().join(format!("media-display-{name}.trickplay"));
        let _ = std::fs::remove_dir_all(&dir);
        let layout = dir.join(layout);
        std::fs::create_dir_all(&layout).expect("a layout directory");
        for (index, bytes) in sheets.iter().enumerate() {
            std::fs::write(layout.join(format!("{index}.jpg")), bytes)
                .expect("a sheet to read back");
        }
        dir
    }

    fn held() -> Mutex<Sheets> {
        Mutex::new(Sheets::default())
    }

    /// The interval as the pod states it. Every test states one, because the
    /// tile mapping divides by this value.
    fn interval(text: &str) {
        // SAFETY: the tests in this module are the one reader of this
        // variable, and each one sets it before it reads it.
        unsafe { std::env::set_var(INTERVAL_VARIABLE, text) };
    }

    /// The interval falls back to ten seconds, which is Jellyfin's own
    /// default, for an unset value and for one no reader can use.
    #[test]
    fn the_interval_falls_back_to_ten_seconds() {
        for (text, want) in [
            ("10s", 10_000),
            ("5s", 5_000),
            ("1m30s", 90_000),
            ("500ms", 500),
            ("1.5s", 1_500),
            ("1h", 3_600_000),
            ("", DEFAULT_INTERVAL_MS),
            ("ten", DEFAULT_INTERVAL_MS),
            ("10", DEFAULT_INTERVAL_MS),
            ("10x", DEFAULT_INTERVAL_MS),
            ("100us", DEFAULT_INTERVAL_MS),
        ] {
            interval(text);
            assert_eq!(interval_ms(), want, "{text}");
        }
        // SAFETY: as above, and no other test reads this variable unset.
        unsafe { std::env::remove_var(INTERVAL_VARIABLE) };
        assert_eq!(interval_ms(), DEFAULT_INTERVAL_MS);
    }

    /// A layout name carries the tile width and the grid, and a name that
    /// spells neither is no layout.
    #[test]
    fn a_layout_name_carries_the_tile_width_and_the_grid() {
        assert_eq!(parse_layout("320 - 10x10"), Some((320, 10, 10)));
        assert_eq!(parse_layout("240 - 8x12"), Some((240, 8, 12)));
        for name in [
            "no dash 10x10",
            "320 - 10",
            "wide - 10x10",
            "320 - 0x10",
            "320 - 10x0",
            "0 - 10x10",
            "320 - ax10",
            "320 - 10xa",
            "",
        ] {
            assert_eq!(parse_layout(name), None, "{name}");
        }
    }

    /// The layout directory is the one subdirectory whose name spells a grid,
    /// and the highest sheet is the top number among the N.jpg files.
    #[test]
    fn the_layout_and_the_highest_sheet_read_off_the_directory() {
        let dir = trickplay("layout", "16 - 2x2", &[sheet(), sheet(), sheet()]);
        assert_eq!(
            find_layout(&dir.to_string_lossy()),
            Some(("16 - 2x2".to_string(), 16, 2, 2))
        );
        assert_eq!(highest_sheet(&dir.join("16 - 2x2")), 2);
    }

    /// A directory that is not a trickplay set has no layout, and one with no
    /// numbered sheet reads as the first sheet. A directory that is not there
    /// answers the same way, so the two cannot be told apart.
    #[test]
    fn a_directory_that_is_not_a_trickplay_set_has_no_layout() {
        let loose = trickplay("loose", "extras", &[]);
        std::fs::write(loose.join("poster.jpg"), "x").expect("a file to skip");
        assert_eq!(find_layout(&loose.to_string_lossy()), None);
        assert_eq!(find_layout(&loose.join("absent").to_string_lossy()), None);

        let mixed = trickplay("mixed", "sheets", &[]);
        for name in ["notes.txt", "cover.png", "first.jpg"] {
            std::fs::write(mixed.join("sheets").join(name), "x").expect("a file to skip");
        }
        assert_eq!(highest_sheet(&mixed.join("sheets")), 0);
        assert_eq!(highest_sheet(&mixed.join("absent")), 0);
    }

    /// The scrub time maps to a cell: the time divides by the interval, the
    /// index divides by the grid into a sheet and a cell, and the cell reads as
    /// a row and a column.
    #[test]
    fn the_scrub_time_maps_to_a_cell_of_a_sheet() {
        let dir = trickplay("mapping", "16 - 2x2", &[sheet()]);
        interval("10s");
        for (milliseconds, cell) in [(0, 0), (9_999, 0), (15_000, 1), (25_000, 2), (39_999, 3)] {
            let tile = tile(&dir.to_string_lossy(), milliseconds, 32, 32, &held()).expect("a tile");
            assert_eq!((tile.width, tile.height), (32, 32), "{milliseconds}");
            let [red, green, blue, _] = centre(&tile);
            let want = CELLS[cell];
            assert!(
                red.abs_diff(want[0]) < 60
                    && green.abs_diff(want[1]) < 60
                    && blue.abs_diff(want[2]) < 60,
                "{milliseconds} read {red} {green} {blue}"
            );
        }
    }

    /// A time past the end of the film clamps to the highest sheet on disk.
    /// The cell is not read again after the clamp, so the tile is the cell the
    /// index named.
    #[test]
    fn a_time_past_the_end_clamps_to_the_highest_sheet() {
        let dir = trickplay("clamp", "16 - 2x2", &[sheet()]);
        interval("10s");
        // 9,000,000 ms is index 900, which is sheet 225 and cell 0.
        let tile = tile(&dir.to_string_lossy(), 9_000_000, 24, 24, &held()).expect("a tile");
        assert_eq!((tile.width, tile.height), (24, 24));
        let [red, green, blue, _] = centre(&tile);
        assert!(red > 150 && green < 60 && blue < 60, "{red} {green} {blue}");
    }

    /// The tile width is the layout's own, and the tile height is the sheet
    /// height over the rows. It is the film's aspect, so a scope film gives a
    /// short wide tile and not a 16:9 one.
    #[test]
    fn the_tile_height_is_the_sheet_height_over_the_rows() {
        let scope = encoded(200, 84, ImageFormat::Jpeg, [0, 0, 0, 255]);
        let dir = trickplay("scope", "100 - 2x2", &[scope]);
        interval("10s");
        let tile = tile(&dir.to_string_lossy(), 0, 360, 220, &held()).expect("a tile");
        assert_eq!((tile.width, tile.height), (360, 151));
    }

    /// The sheet is decoded once and held, because a scrub stays within one
    /// sheet for a long stretch. The held sheet answers after the file goes.
    #[test]
    fn the_sheet_is_decoded_once_and_held() {
        let dir = trickplay("held", "16 - 2x2", &[sheet()]);
        interval("10s");
        let sheets = held();
        assert!(tile(&dir.to_string_lossy(), 0, 16, 16, &sheets).is_some());
        std::fs::remove_file(dir.join("16 - 2x2").join("0.jpg")).expect("the sheet to go");
        assert!(tile(&dir.to_string_lossy(), 15_000, 16, 16, &sheets).is_some());
    }

    /// Every failure along the crop path crops nothing: a directory that is
    /// not there, one with no layout inside it, a layout with no sheet, a
    /// sheet that is not a picture, and a sheet too short to hold its rows.
    #[test]
    fn a_directory_the_display_cannot_read_crops_nothing() {
        interval("10s");
        assert!(tile("/art/nothing", 5_000, 24, 24, &held()).is_none());

        let empty = trickplay("empty", "not a layout", &[]);
        assert!(tile(&empty.to_string_lossy(), 5_000, 24, 24, &held()).is_none());

        let missing = trickplay("missing", "16 - 2x2", &[]);
        assert!(tile(&missing.to_string_lossy(), 5_000, 24, 24, &held()).is_none());

        let garbage = trickplay("garbage", "16 - 2x2", &[b"not an image".to_vec()]);
        assert!(tile(&garbage.to_string_lossy(), 5_000, 24, 24, &held()).is_none());

        let short = trickplay(
            "short",
            "16 - 2x2",
            &[encoded(32, 1, ImageFormat::Jpeg, [0, 0, 0, 255])],
        );
        assert!(tile(&short.to_string_lossy(), 5_000, 24, 24, &held()).is_none());
    }
}
