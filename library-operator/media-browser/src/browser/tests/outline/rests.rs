// The last frame of a motion. A press starts the outline and the scrolls
// toward their rest, and the loop draws a frame at each second the
// browser names until the rest. The frame that holds the glass after the
// motion must show the rest, because nothing else draws until the breath
// starts `look::BREATH_DELAY` later. Every case walks the invented
// catalog through one screen's presses and steps the clock the way the
// loop does: a frame at each second the harness asks for, no sooner than
// `frame::STEP` after the last, with the harness asking only after the
// last frame's work is done.

use super::*;
use crate::harness::frame::{STEP, screen_due};
use crate::sample;

// The second the first press of each case lands on, long after the
// browser opened.
const FIRST: f64 = 20.0;

// How long the loop draws after the last press before the case reads
// the glass: the motion's whole length and a few frames more, and less
// than the prefetch's rest of 0.3 seconds, whose frame would draw the
// rest and hide a short motion on the screens that prefetch.
const SETTLE: f64 = look::GLIDE + 0.1;

// The time one frame's work takes before the loop asks for the next: no
// time, a frame cheaper than `STEP`, and a frame slower than `STEP`, as
// on a small machine.
const WORK: [f64; 3] = [0.0, 0.012, 0.025];

// The time between two presses: one at a time, with the motion at rest
// in between, and a held key's repeat.
const ONE_AT_A_TIME: f64 = 1.0;
const HELD: f64 = 0.05;

// The loop after a press at `press`, until `until`: the press makes the
// glass stale, so the first frame draws at once, and then a frame at
// each second the harness asks for. The answer is the second of the last
// frame drawn.
fn frames<S: Source, A: Art>(
    browser: &mut Browser<S, A>,
    last: f64,
    press: f64,
    until: f64,
    work: f64,
) -> f64 {
    let mut frame = press.max(last + STEP);
    loop {
        browser.tick(frame);
        // The minute of the wall clock schedules a frame of its own, which
        // would draw the rest and hide a short motion.
        browser.minute = None;
        let due = screen_due(&*browser, None, false, frame)
            .map(|due| due.max(frame + work).max(frame + STEP));
        match due {
            Some(next) if next < until => frame = next,
            _ => return frame,
        }
    }
}

// What one walk leaves on the glass: whether the last frame drew at or
// after the second every motion of the last press ends, which is the
// second each scroll rests at too; the outline that frame drew; the
// outline once everything rests; and the second the walk ended on.
struct Walked {
    rested: bool,
    held: Option<Rectangle>,
    rest: Option<Rectangle>,
    ended: f64,
}

// Press these keys `interval` apart from the second `from`.
fn walked<S: Source, A: Art>(
    browser: &mut Browser<S, A>,
    from: f64,
    keys: &[&str],
    interval: f64,
    work: f64,
) -> Walked {
    let mut last = from - 1.0;
    for (index, key) in keys.iter().enumerate() {
        let press = from + index as f64 * interval;
        browser.tick(press);
        browser.key(key);
        let until = match index + 1 == keys.len() {
            true => press + SETTLE,
            false => press + interval,
        };
        last = frames(browser, last, press, until, work);
    }
    let rested = !browser.breath.moving(last);
    let held = browser.drawn().map(|drawn| drawn.around);
    let ended = last + 10.0;
    browser.tick(ended);
    Walked {
        rested,
        held,
        rest: browser.drawn().map(|drawn| drawn.around),
        ended,
    }
}

// The invented catalog's browser, opened at the second zero.
fn sampled() -> Browser<sample::Catalog, sample::NoArt> {
    let mut browser = Browser::new(sample::Catalog, sample::NoArt);
    browser.tick(0.0);
    browser
}

// The kind of screen on top, so a case checks that its presses reached
// the screen it names.
fn kind(screen: &screens::Screen) -> &'static str {
    match screen {
        screens::Screen::Home(_) => "home",
        screens::Screen::Wall(_) => "wall",
        screens::Screen::Movie(_) => "film",
        screens::Screen::Series(_) => "series",
        screens::Screen::Person(_) => "person",
        screens::Screen::Franchise(_) => "franchise",
    }
}

// Six downs from the top of the invented home page reach the libraries
// strip, whose first slot opens the films and whose second the series.
const LIBRARIES: [&str; 6] = ["down"; 6];

// Each case: the screen, the presses after `LIBRARIES` or from the top
// of the home page that reach it, and the presses on it.
const WALKS: [(&str, bool, &[&str], &[&str]); 10] = [
    // Down the home page's rows, and along a strip to its end.
    ("home", false, &[], &["down"; 9]),
    ("home", false, &["down", "down"], &["right"; 14]),
    // Up onto the strip, along it, and back down.
    ("home", false, &[], &["up", "right", "left", "down"]),
    // A wall: across a row, onto the rail, the rail's jump, and down.
    (
        "wall",
        true,
        &["enter"],
        &[
            "right", "right", "right", "right", "right", "right", "enter", "down", "up", "up",
            "down",
        ],
    ),
    // The search wall: through the hits, and up onto the field.
    (
        "wall",
        false,
        &["a"],
        &["down", "right", "down", "up", "up", "up"],
    ),
    // A film's page: along the playback row, down through every row to
    // the foot, and back up onto the strip.
    (
        "film",
        true,
        &["enter", "enter"],
        &[
            "right", "down", "down", "down", "down", "down", "down", "down", "down", "up", "up",
            "up", "up", "up", "up", "up", "up", "up",
        ],
    ),
    // Along a film's set strip.
    (
        "film",
        true,
        &["enter", "enter", "down", "down"],
        &["right", "right", "left"],
    ),
    // A series' page: across the wall, down through the seasons and the
    // rows under them to the foot, and back up to the wall.
    (
        "series",
        true,
        &["right", "enter", "enter"],
        &[
            "right", "down", "down", "down", "down", "down", "down", "down", "down", "down",
            "down", "down", "down", "up", "up", "up", "up", "up", "up", "up", "up",
        ],
    ),
    // A person's page, opened from a film's first stripe.
    (
        "person",
        true,
        &[
            "enter", "enter", "down", "down", "down", "down", "down", "enter",
        ],
        &["right", "right", "left", "up", "down"],
    ),
    // A franchise's page, opened from the last row of the home page.
    (
        "franchise",
        false,
        &[
            "down", "down", "down", "down", "down", "down", "down", "down", "down", "enter",
        ],
        &["down", "down", "right", "up", "up", "up"],
    ),
];

// The cases at each cost of a frame and each interval between presses.
// One at a time, every press must land; held, the last press must.
#[test]
fn the_last_frame_of_every_motion_shows_its_rest() {
    for (screen, libraries, reach, presses) in WALKS {
        for work in WORK {
            for interval in [ONE_AT_A_TIME, HELD] {
                let mut browser = sampled();
                let before: &[&str] = if libraries { &LIBRARIES } else { &[] };
                for key in before.iter().chain(reach) {
                    browser.key(key);
                }
                assert_eq!(kind(browser.top()), screen, "{reach:?}");
                let walks: Vec<&[&str]> = match interval == HELD {
                    true => vec![presses],
                    false => presses.chunks(1).collect(),
                };
                let mut from = FIRST;
                for walk in walks {
                    let walked = walked(&mut browser, from, walk, interval, work);
                    let case = format!("{reach:?} {walk:?}, {work} s of work, {interval} s apart");
                    assert!(walked.rested, "{case}");
                    assert_eq!(walked.held, walked.rest, "{case}");
                    from = walked.ended;
                }
            }
        }
    }
}

// The person picker is a layer of its own over the page.
#[test]
fn the_last_frame_of_a_move_on_the_picker_shows_its_rest() {
    for work in WORK {
        for interval in [ONE_AT_A_TIME, HELD] {
            let mut browser = on_the_picker();
            let walked = walked(
                &mut browser,
                FIRST,
                &["right", "left", "right"],
                interval,
                work,
            );
            let case = format!("{work} s of work, {interval} s apart");
            assert!(walked.rested, "{case}");
            assert_eq!(walked.held, walked.rest, "{case}");
        }
    }
}
