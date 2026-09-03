// The screens the browser draws under a real compositor: the wall and
// its band, and a movie's page over the art a volume holds.

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
