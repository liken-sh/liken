// The volume row at the browser: the level moments the bus delivers, the
// row they bring up over whatever screen is on the stack, and the frames
// the loop asks for while it runs.

use media_screen::volume::Volume;

use super::*;

// The second the first level lands on, so the row is entered on a clock
// that has already moved.
const PRESS: f64 = 2.0;

// The level a remote pressed to.
fn level() -> Volume {
    Volume {
        level: 40,
        muted: false,
    }
}

// One level moment as the crate delivers it: pressed for a remote's own
// step, and unpressed for the broker's retained catch-up.
fn moment(volume: Volume, pressed: bool) -> Moment {
    Moment::Level { volume, pressed }
}

#[test]
fn a_level_press_brings_the_row_up() {
    let (mut browser, _bus) = on_bus(3, vec![moment(level(), true)]);

    assert!(browser.pump(PRESS));

    let row = browser.level.row(PRESS + 1.0).expect("the row is up");
    assert_eq!(row.volume, level());
    assert_eq!(row.fade, 1.0);
}

#[test]
fn the_retained_catch_up_brings_up_no_row() {
    let (mut browser, _bus) = on_bus(3, vec![moment(level(), false)]);

    assert!(browser.pump(PRESS));

    assert_eq!(browser.level.row(PRESS), None);
    assert_eq!(browser.level.row(PRESS + 1.0), None);
}

#[test]
fn the_row_holds_four_seconds_and_leaves() {
    let (mut browser, _bus) = on_bus(3, vec![moment(level(), true)]);
    browser.pump(PRESS);

    assert!(browser.level.row(PRESS + 4.0).is_some());
    assert_eq!(browser.level.row(PRESS + 4.6), None);
}

#[test]
fn a_second_press_restarts_the_hold() {
    let (mut browser, bus) = on_bus(3, vec![moment(level(), true)]);
    browser.pump(PRESS);

    *bus.inbound.lock().expect("no test panics with the lock") = vec![moment(
        Volume {
            level: 45,
            ..level()
        },
        true,
    )];
    browser.pump(PRESS + 3.0);

    let row = browser.level.row(PRESS + 4.6).expect("the row is up");
    assert_eq!(row.volume.level, 45);
    assert_eq!(browser.level.row(PRESS + 7.6), None);
}

#[test]
fn the_muted_flag_reaches_the_row() {
    let (mut browser, _bus) = on_bus(
        3,
        vec![moment(
            Volume {
                level: 40,
                muted: true,
            },
            true,
        )],
    );
    browser.pump(PRESS);

    assert!(
        browser
            .level
            .row(PRESS + 1.0)
            .expect("the row is up")
            .volume
            .muted
    );
}

#[test]
fn a_descent_under_the_row_leaves_it_where_it_stands() {
    let (mut browser, _bus) = on_bus(3, vec![moment(level(), true)]);
    browser.pump(PRESS);

    browser.key("enter");

    assert!(matches!(browser.top(), screens::Screen::Wall(_)));
    assert!(browser.level.row(PRESS + 1.0).is_some());
}

#[test]
fn the_row_asks_the_loop_for_its_frames() {
    let (mut browser, _bus) = on_bus(3, vec![moment(level(), true)]);
    browser.pump(PRESS);

    // The fade in draws every frame it covers, the hold names the second
    // the row starts to leave, and a row that has left asks for nothing.
    assert_eq!(browser.next_frame(PRESS + 0.1), Some(PRESS + 0.1));
    assert_eq!(browser.next_frame(PRESS + 1.0), Some(PRESS + 4.0));
    assert_eq!(browser.next_frame(PRESS + 4.3), Some(PRESS + 4.3));
    assert_eq!(browser.next_frame(PRESS + 4.7), None);
}

#[test]
fn a_covered_browser_schedules_no_frame_for_the_row() {
    let (mut browser, _bus) = on_bus(3, vec![moment(level(), true), Moment::Sleep]);
    browser.pump(PRESS);

    assert!(browser.asleep());
    assert_eq!(browser.next_frame(PRESS + 0.1), None);
}
