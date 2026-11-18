//! Crops the thumbnail for a scrub position out of a Jellyfin trickplay
//! sprite sheet. Jellyfin generates the sheets ahead of time, so the player
//! decodes no second video stream to show the frame at the cursor time.
//! The tile grid comes from the layout directory's name, and `tile` picks
//! the cell for the time, crops it, and scales it to the requested box.

use std::path::Path;
use std::sync::Mutex;

use image::DynamicImage;

use super::bitmap::Bitmap;
use super::decode;

/// The environment variable that holds the time one tile covers, as a Go
/// duration. The operator sets it from the `Play`. `interval_ms` falls back
/// to Jellyfin's default of ten seconds when it is unset, unparseable, or
/// under one millisecond, because the tile index divides by it.
pub const INTERVAL_VARIABLE: &str = "MEDIA_TRICKPLAY_INTERVAL";
const DEFAULT_INTERVAL_MS: i64 = 10_000;

/// Each sheet is a JPEG named `<number>.jpg` inside the layout directory.
const SHEET_SUFFIX: &str = ".jpg";

/// The time one tile covers, in milliseconds.
pub fn interval_ms() -> i64 {
    std::env::var(INTERVAL_VARIABLE)
        .ok()
        .as_deref()
        .and_then(duration_ms)
        .filter(|milliseconds| *milliseconds >= 1)
        .unwrap_or(DEFAULT_INTERVAL_MS)
}

/// Parses a Go `time.Duration` string such as `10s` or `1m30s`, which is the
/// format the operator writes. Returns `None` for a number with no unit.
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

/// The units of a Go duration, in milliseconds. `ms` comes before `m` and
/// `s` so that the longer unit matches first.
const UNITS: [(&str, f64); 7] = [
    ("ns", 1e-6),
    ("us", 1e-3),
    ("\u{00B5}s", 1e-3),
    ("ms", 1.0),
    ("s", 1000.0),
    ("m", 60_000.0),
    ("h", 3_600_000.0),
];

/// A cache of the last decoded sheet, keyed by its layout path and number.
/// Consecutive scrub positions usually fall on the same sheet, so the cache
/// saves a file read and a JPEG decode on most steps.
#[derive(Debug, Default)]
pub struct Sheets {
    key: String,
    sheet: Option<DynamicImage>,
}

/// Returns the thumbnail for `ms` from the sheets in `dir`, scaled to the
/// box. Returns `None` when `dir` has no layout directory or the sheet
/// cannot be read or cropped.
pub fn tile(dir: &str, ms: i64, box_w: u32, box_h: u32, sheets: &Mutex<Sheets>) -> Option<Bitmap> {
    let index = ms / interval_ms();
    let (name, tile_w, cols, rows) = find_layout(dir)?;
    let layout = Path::new(dir).join(name);

    let per_sheet = cols * rows;
    let cell = index % per_sheet;
    let row = cell / cols;
    let col = cell % cols;
    // A time past the end of the film indexes a sheet that does not exist,
    // so use the highest numbered sheet on disk.
    let sheet = (index / per_sheet).min(highest_sheet(&layout));

    let image = sheet_image(&layout, sheet, sheets)?;
    // The layout name gives the tile width but not the height. The height is
    // the sheet height over the rows, which follows the film's aspect ratio.
    let tile_h = image.height() / rows as u32;
    if tile_h == 0 {
        return None;
    }
    let region = image.crop_imm(col as u32 * tile_w, row as u32 * tile_h, tile_w, tile_h);
    decode::scale(&region, box_w, box_h)
}

/// Returns the decoded sheet from the cache, or reads and decodes it and
/// replaces the cached one.
fn sheet_image(layout: &Path, sheet: i64, sheets: &Mutex<Sheets>) -> Option<DynamicImage> {
    let key = format!("{}:{sheet}", layout.to_string_lossy());
    if let Ok(cached) = sheets.lock()
        && cached.key == key
        && let Some(sheet) = &cached.sheet
    {
        return Some(sheet.clone());
    }

    let path = layout.join(format!("{sheet}{SHEET_SUFFIX}"));
    let decoded = decode::read(&std::fs::read(path).ok()?)?;

    if let Ok(mut cached) = sheets.lock() {
        cached.key = key;
        cached.sheet = Some(decoded.clone());
    }
    Some(decoded)
}

/// Finds the first subdirectory of `dir`, in name order, whose name parses
/// as a layout. Jellyfin names it `<tile width> - <columns>x<rows>`, such as
/// `320 - 10x10`, so the geometry is known without opening a sheet. Returns
/// the name, tile width, columns, and rows.
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

/// Parses `<tile width> - <columns>x<rows>`. All three numbers must be
/// positive.
fn parse_layout(name: &str) -> Option<(u32, i64, i64)> {
    let (left, right) = name.split_once(" - ")?;
    let tile_w: u32 = left.trim().parse().ok()?;
    let (columns, lines) = right.trim().split_once('x')?;
    let cols: i64 = columns.parse().ok()?;
    let rows: i64 = lines.parse().ok()?;
    (tile_w > 0 && cols > 0 && rows > 0).then_some((tile_w, cols, rows))
}

/// The highest number N among the `N.jpg` files in `dir`, or 0 when there
/// are none.
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

    /// The color of each cell of the test sheet: a 32 by 32 JPEG of four
    /// 16 by 16 cells, numbered row by row, cell 0 top left and cell 3
    /// bottom right.
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

    /// Writes a trickplay directory with one layout directory named
    /// `layout`, holding `sheets` as `0.jpg`, `1.jpg`, and so on.
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

    fn cache() -> Mutex<Sheets> {
        Mutex::new(Sheets::default())
    }

    /// Serializes the tests that set the interval variable. Cargo runs tests
    /// on parallel threads and the variable is process-wide, so without the
    /// lock one test can change it between another test's set and read.
    static ENV: std::sync::Mutex<()> = std::sync::Mutex::new(());

    fn env() -> std::sync::MutexGuard<'static, ()> {
        ENV.lock().unwrap_or_else(|poisoned| poisoned.into_inner())
    }

    fn set_interval(text: &str) {
        // SAFETY: only tests in this module use this variable, and each
        // holds ENV while it sets and reads it.
        unsafe { std::env::set_var(INTERVAL_VARIABLE, text) };
    }

    /// Sets the interval variable and returns the lock, which the test keeps
    /// until it ends.
    fn interval(text: &str) -> std::sync::MutexGuard<'static, ()> {
        let guard = env();
        set_interval(text);
        guard
    }

    #[test]
    fn the_interval_reads_go_durations_and_falls_back_to_ten_seconds() {
        let _env = env();
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
            set_interval(text);
            assert_eq!(interval_ms(), want, "{text}");
        }
        // SAFETY: as above, and no other test reads this variable unset.
        unsafe { std::env::remove_var(INTERVAL_VARIABLE) };
        assert_eq!(interval_ms(), DEFAULT_INTERVAL_MS);
    }

    #[test]
    fn a_layout_name_parses_into_the_tile_width_and_the_grid() {
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

    #[test]
    fn the_layout_and_the_highest_sheet_come_from_the_directory_listing() {
        let dir = trickplay("layout", "16 - 2x2", &[sheet(), sheet(), sheet()]);
        assert_eq!(
            find_layout(&dir.to_string_lossy()),
            Some(("16 - 2x2".to_string(), 16, 2, 2))
        );
        assert_eq!(highest_sheet(&dir.join("16 - 2x2")), 2);
    }

    /// A directory with no layout subdirectory has no layout, and a layout
    /// directory with no numbered sheet has highest sheet 0. A directory that
    /// does not exist gives the same results.
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

    /// The time divided by the interval is the tile index. The index divided
    /// by `cols * rows` gives the sheet and the remainder gives the cell,
    /// which is read row by row.
    #[test]
    fn the_scrub_time_maps_to_a_cell_of_a_sheet() {
        let dir = trickplay("mapping", "16 - 2x2", &[sheet()]);
        let _env = interval("10s");
        for (milliseconds, cell) in [(0, 0), (9_999, 0), (15_000, 1), (25_000, 2), (39_999, 3)] {
            let tile =
                tile(&dir.to_string_lossy(), milliseconds, 32, 32, &cache()).expect("a tile");
            assert_eq!(
                (tile.width, tile.height),
                (32, 32),
                "the tile size at {milliseconds} ms is wrong, want 32x32"
            );
            let [red, green, blue, _] = centre(&tile);
            let want = CELLS[cell];
            assert!(
                red.abs_diff(want[0]) < 60
                    && green.abs_diff(want[1]) < 60
                    && blue.abs_diff(want[2]) < 60,
                "at {milliseconds} ms the tile centre is {red} {green} {blue}, want near {want:?}"
            );
        }
    }

    /// A time past the end of the film uses the highest sheet on disk. The
    /// cell within the sheet still comes from the unclamped index.
    #[test]
    fn a_time_past_the_end_clamps_to_the_highest_sheet() {
        let dir = trickplay("clamp", "16 - 2x2", &[sheet()]);
        let _env = interval("10s");
        // 9,000,000 ms is index 900, which is sheet 225 and cell 0.
        let tile = tile(&dir.to_string_lossy(), 9_000_000, 24, 24, &cache()).expect("a tile");
        assert_eq!((tile.width, tile.height), (24, 24));
        let [red, green, blue, _] = centre(&tile);
        assert!(
            red > 150 && green < 60 && blue < 60,
            "the tile centre is {red} {green} {blue}, want red"
        );
    }

    /// The tile width comes from the layout name and the tile height is the
    /// sheet height over the rows, so a wide film gives a short, wide tile.
    #[test]
    fn the_tile_height_is_the_sheet_height_over_the_rows() {
        let scope = encoded(200, 84, ImageFormat::Jpeg, [0, 0, 0, 255]);
        let dir = trickplay("scope", "100 - 2x2", &[scope]);
        let _env = interval("10s");
        let tile = tile(&dir.to_string_lossy(), 0, 360, 220, &cache()).expect("a tile");
        assert_eq!((tile.width, tile.height), (360, 151));
    }

    /// A second tile from the same sheet reads no file: it still succeeds
    /// after the sheet is deleted.
    #[test]
    fn a_second_tile_from_the_same_sheet_comes_from_the_cache() {
        let dir = trickplay("cached", "16 - 2x2", &[sheet()]);
        let _env = interval("10s");
        let sheets = cache();
        assert!(tile(&dir.to_string_lossy(), 0, 16, 16, &sheets).is_some());
        std::fs::remove_file(dir.join("16 - 2x2").join("0.jpg")).expect("the sheet to go");
        assert!(tile(&dir.to_string_lossy(), 15_000, 16, 16, &sheets).is_some());
    }

    /// Each failure returns `None`: a missing directory, a directory with no
    /// layout, a layout with no sheet, a sheet that is not an image, and a
    /// sheet shorter than its row count.
    #[test]
    fn an_unreadable_trickplay_directory_returns_no_tile() {
        let _env = interval("10s");
        assert!(tile("/art/nothing", 5_000, 24, 24, &cache()).is_none());

        let empty = trickplay("empty", "not a layout", &[]);
        assert!(tile(&empty.to_string_lossy(), 5_000, 24, 24, &cache()).is_none());

        let missing = trickplay("missing", "16 - 2x2", &[]);
        assert!(tile(&missing.to_string_lossy(), 5_000, 24, 24, &cache()).is_none());

        let garbage = trickplay("garbage", "16 - 2x2", &[b"not an image".to_vec()]);
        assert!(tile(&garbage.to_string_lossy(), 5_000, 24, 24, &cache()).is_none());

        let short = trickplay(
            "short",
            "16 - 2x2",
            &[encoded(32, 1, ImageFormat::Jpeg, [0, 0, 0, 255])],
        );
        assert!(tile(&short.to_string_lossy(), 5_000, 24, 24, &cache()).is_none());
    }
}
