// The screens the browser draws under a real compositor: the wall and
// its band, and a movie's page over the art a volume holds.

use iced_winit::core::Color;

use super::*;

#[test]
fn a_catalog_run_draws_the_poster_the_volume_holds() {
    let dir = workspace("catalog");
    let frames = dir.join("frames");
    let (database, volume) = fixture(&dir);

    // No agent answers on port 1, so this run reads the file alone
    // and its update streams find nothing.
    let run = headless(
        &dir,
        &[
            "--catalog",
            &text(&database),
            "--updates",
            "http://127.0.0.1:1",
            "--library-root",
            &format!("drill/films={}", text(&volume)),
            "--script",
            "0.5:enter",
            "--capture",
            &text(&frames),
            "--capture-at",
            "2.0",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    drawn(&frames.join("002.00.png"), &run);
}

#[test]
fn a_descent_draws_the_wall() {
    let dir = workspace("wall");
    let frames = dir.join("frames");

    let run = headless(
        &dir,
        &[
            "--script",
            "0.5:enter,0.8:right,1.1:down",
            "--capture",
            &text(&frames),
            "--capture-at",
            "1.6",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);

    let frame = frames.join("001.60.png");
    assert_eq!(
        image::image_dimensions(&frame).ok(),
        Some((1920, 1080)),
        "{}\n{}",
        frame.display(),
        run.log
    );
    drawn(&frame, &run);
}

// Up from the first row of posters reaches the band, and the frame draws
// with a control marked.
#[test]
fn the_band_takes_focus_on_a_wall() {
    let dir = workspace("band");
    let frames = dir.join("frames");

    let run = headless(
        &dir,
        &[
            "--script",
            "0.5:enter,0.9:up,1.1:right",
            "--capture",
            &text(&frames),
            "--capture-at",
            "1.4",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    drawn(&frames.join("001.40.png"), &run);
}

// A select on a movie opens its page, which draws the backdrop, the
// text, the buttons, and the set strip on one frame.
#[test]
fn a_select_on_a_movie_draws_its_page() {
    let dir = workspace("page");
    let frames = dir.join("frames");

    let run = headless(
        &dir,
        &[
            "--script",
            "0.5:enter,1.0:enter,1.4:down",
            "--capture",
            &text(&frames),
            "--capture-at",
            "1.8",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    drawn(&frames.join("001.80.png"), &run);
}

// A page over a catalog draws the art the volume holds: the backdrop
// under the whole frame, and the logo where the title would otherwise
// be.
#[test]
fn a_catalog_page_draws_the_backdrop_and_the_logo() {
    let dir = workspace("catalog-page");
    let frames = dir.join("frames");
    let (database, volume) = fixture(&dir);

    let run = headless(
        &dir,
        &[
            "--catalog",
            &text(&database),
            "--updates",
            "http://127.0.0.1:1",
            "--library-root",
            &format!("drill/films={}", text(&volume)),
            "--script",
            "0.5:enter,1.5:enter",
            "--capture",
            &text(&frames),
            "--capture-at",
            "2.5",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    drawn(&frames.join("002.50.png"), &run);
}

// A select on a series opens its page, and three presses down cross the
// first season's divider into the second. The header stands still at the
// top of the frame and the wall of stills scrolls in the region under it.
#[test]
fn a_series_page_scrolls_into_its_second_season() {
    let dir = workspace("series");
    let frames = dir.join("frames");

    let run = headless(
        &dir,
        &[
            "--script",
            "0.5:down,0.9:enter,1.3:enter,1.7:down,2.0:down,2.3:down",
            "--capture",
            &text(&frames),
            "--capture-at",
            "2.7",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    drawn(&frames.join("002.70.png"), &run);
}

// A select on a headshot opens the person's page, which draws the
// headshot, the words beside it, and the wall of their works.
#[test]
fn a_select_on_a_headshot_draws_the_persons_page() {
    let dir = workspace("person");
    let frames = dir.join("frames");

    let run = headless(
        &dir,
        &[
            "--script",
            "0.5:enter,1.0:enter,1.4:down,1.8:down,2.2:enter",
            "--capture",
            &text(&frames),
            "--capture-at",
            "2.6",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    drawn(&frames.join("002.60.png"), &run);
}

// A select on Play enters the loading state. Nothing answers the
// request here, so the state holds: the frame draws the backdrop whole,
// the logo at the centre of it, and the mark under the logo.
#[test]
fn a_play_press_draws_the_loading_state() {
    let dir = workspace("loading");
    let frames = dir.join("frames");
    let (database, volume) = fixture(&dir);
    playable(&database);

    let run = headless(
        &dir,
        &[
            "--catalog",
            &text(&database),
            "--updates",
            "http://127.0.0.1:1",
            "--library-root",
            &format!("drill/films={}", text(&volume)),
            "--script",
            "0.5:enter,1.5:enter,2.5:enter",
            "--capture",
            &text(&frames),
            "--capture-at",
            "3.5",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    drawn(&frames.join("003.50.png"), &run);
}

// A level a remote pressed on the bus brings up the volume row, which draws
// over whatever screen the browser holds. The broker states the level again
// every quarter second, so the row is up for the whole run and the captured
// frame carries it.
#[test]
fn a_level_on_the_bus_draws_the_volume_row() {
    let frame = a_level_of("volume", "{\"level\":40,\"muted\":false}");

    paints(&frame, BAR, media_browser::look::accent());
}

// The muted flag draws on the glyph alone, so the bar still fills to the
// level and the speaker draws in the muted ink.
#[test]
fn a_muted_level_draws_the_slash_on_the_glyph() {
    let frame = a_level_of("muted", "{\"level\":40,\"muted\":true}");

    paints(&frame, BAR, media_browser::look::accent());
    paints(&frame, GLYPH, media_browser::look::muted());
}

// The topic the operator names for a unit with sinks.
const VOLUME_TOPIC: &str = "liken/media/players/house/den-tv/volume";

// Two points of the row in a 1920 by 1080 frame, which the frames below are
// read at. The row hangs off the top right corner: the number's box ends
// 120 from the right edge, the bar is the 220 before the 84 the number
// reserves, and 40 of 100 fills the first 88 of it. The glyph stands 26
// wide and 16 to the left of the bar, and the point below is inside the
// speaker's driver box.
const BAR: (u32, u32) = (1540, 74);
const GLYPH: (u32, u32) = (1467, 75);

// One run of the browser on a broker that states this level, and the frame
// it captured while the row was up. The frame is taken well after the
// press, because under the load of the whole suite the client can take
// seconds to connect, and the broker restates the level every quarter
// second so a later frame still holds the row.
fn a_level_of(name: &str, payload: &str) -> Frame {
    let dir = workspace(name);
    let frames = dir.join("frames");
    let broker = broker::publishing(VOLUME_TOPIC, payload);

    let run = headless_with(
        &dir,
        &[
            ("MEDIA_BUS_ADDRESS", &broker.address),
            ("MEDIA_PLAYER_NAME", "den-tv"),
            ("MEDIA_PLAYER_VOLUME_TOPIC", VOLUME_TOPIC),
        ],
        &[
            "--script",
            "0.5:enter",
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
    Frame { path, run }
}

// A captured frame, and the run that wrote it, so a failed reading reports
// the log of the run it came from.
struct Frame {
    path: PathBuf,
    run: Run,
}

// The row draws its parts at full opacity over the frame under it, so a
// point inside one of them carries that part's own colour, and the frame
// proves the row by holding it.
fn paints(frame: &Frame, at: (u32, u32), color: Color) {
    let pixels = image::open(&frame.path)
        .unwrap_or_else(|error| panic!("{}: {error}\n{}", frame.path.display(), frame.run.log))
        .to_rgb8();

    let (x, y) = at;
    let drawn = *pixels.get_pixel(x, y);
    let apart = |part: f32, drawn: u8| (part * 255.0 - f32::from(drawn)).abs();
    assert!(
        apart(color.r, drawn.0[0]) <= 1.0
            && apart(color.g, drawn.0[1]) <= 1.0
            && apart(color.b, drawn.0[2]) <= 1.0,
        "{} draws {drawn:?} at {x},{y}, and the row draws {color:?} there\n{}",
        frame.path.display(),
        frame.run.log
    );
}

// The film the fixture movie plays, so a select on Play resolves a list
// and the page enters the loading state. The other runs need no film,
// because they never press Play.
fn playable(database: &Path) {
    let connection = rusqlite::Connection::open(database).expect("open the fixture database");
    connection
        .execute(
            "INSERT INTO files (library, path, type, role, duration_ms) \
             VALUES ('drill/films', 'Vespera Coppice (1994).mkv', 'video', 'primary', 5400000)",
            (),
        )
        .expect("insert the fixture film");
    connection
        .execute(
            "INSERT INTO file_items (library, path, item) \
             VALUES ('drill/films', 'Vespera Coppice (1994).mkv', 'movie:path:one')",
            (),
        )
        .expect("link the fixture film");
}
