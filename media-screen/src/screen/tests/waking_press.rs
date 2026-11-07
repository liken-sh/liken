// The press that lifts the shade. On a `Player` whose off window is zero,
// the shade is the only thing between a person and the client after every
// quiet stretch, so the press that lifts it also acts: the client receives
// the wake and then the press. A press on a dark panel only wakes it,
// because the person could not see what the press would act on.

use super::*;

/// A unit whose quiet window ran out and that has no off window, so the
/// shade is down over a panel that stays lit.
fn shaded(now: Instant) -> Screen {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let mut screen = idling(&wiring, now);
    screen.tick(now + Duration::from_secs(600));
    screen.take_lines();
    screen
}

/// A unit whose off window ran out after its quiet window, so the panel is
/// dark behind the shade.
fn off_window_ran_out(now: Instant) -> Screen {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    };
    let mut screen = idling(&wiring, now);
    screen.tick(now + Duration::from_secs(600));
    screen.tick(now + Duration::from_secs(1800));
    screen.take_lines();
    screen
}

/// A unit that `media-operator` asked to sleep, because the `Player` turned
/// off or the TV went to standby, so the panel is dark behind the shade.
fn asked_to_sleep(now: Instant) -> Screen {
    let mut screen = idling(&wiring(), now);
    screen.deliver(POWER, br#"{"action":"sleep"}"#, false, now);
    screen.take_lines();
    screen
}

#[test]
fn the_press_that_lifts_the_shade_reaches_the_client_after_the_wake() {
    let now = Instant::now();
    let pressed = now + Duration::from_secs(700);
    for name in [
        "KEY_SELECT",
        "KEY_ENTER",
        "KEY_OK",
        "KEY_UP",
        "KEY_DOWN",
        "KEY_A",
        "KEY_HOMEPAGE",
        "KEY_PLAYPAUSE",
        "KEY_VOLUMEUP",
        "KEY_UNKNOWN",
    ] {
        let mut screen = shaded(now);

        assert_eq!(
            moments(screen.deliver(SOFA_EVENTS, &key(name, 1), false, pressed)),
            [Moment::Wake, Moment::Press(name.into())],
            "{name}"
        );
        assert!(!screen.asleep, "{name}");
    }
}

// The quiet window leaves the on desire standing, so the lift states nothing
// new on the panel topic.
#[test]
fn the_press_that_lifts_the_shade_over_a_lit_panel_states_no_desire() {
    let now = Instant::now();
    let mut screen = shaded(now);

    let effects = screen.deliver(SOFA_EVENTS, &key("KEY_SELECT", 1), false, now);

    assert!(publishes(effects).is_empty());
}

#[test]
fn the_press_that_lifts_the_shade_restarts_the_quiet_window() {
    let now = Instant::now();
    let mut screen = shaded(now);

    let pressed = now + Duration::from_secs(700);
    screen.deliver(SOFA_EVENTS, &key("KEY_DOWN", 1), false, pressed);

    assert_eq!(
        screen.next_deadline(),
        Some(pressed + Duration::from_secs(600))
    );
}

#[test]
fn a_press_on_a_dark_panel_only_wakes_it_and_lights_the_panel() {
    let now = Instant::now();
    for (mut screen, way) in [
        (off_window_ran_out(now), "the off window"),
        (asked_to_sleep(now), "a sleep ask"),
    ] {
        let effects = screen.deliver(SOFA_EVENTS, &key("KEY_SELECT", 1), false, now);

        assert_eq!(moments(effects.clone()), [Moment::Wake], "{way}");
        assert_eq!(publishes(effects), [desire(crate::panel::ON)], "{way}");
    }
}

// Both clients answer back by asking for the shade at their top level, so a
// back that lifted the shade and reached the client would bring it down again.
#[test]
fn back_on_the_shade_only_lifts_it() {
    let now = Instant::now();
    for name in keys::BACK {
        let mut screen = shaded(now);

        assert_eq!(
            moments(screen.deliver(SOFA_EVENTS, &key(name, 1), false, now)),
            [Moment::Wake],
            "{name}"
        );
        assert!(!screen.asleep, "{name}");
    }
}

#[test]
fn back_on_the_shade_says_why_it_only_lifted_it() {
    let now = Instant::now();
    let mut screen = shaded(now);

    screen.deliver(SOFA_EVENTS, &key("KEY_BACK", 1), false, now);

    assert_eq!(
        screen.take_lines(),
        [
            "KEY_BACK from remote house/sofa woke the screen and did nothing else, because back on the shade only lifts it"
        ]
    );
}

// A repeat that finds the shade down belongs to a control held while the
// shade came down, and that control's press acted before it did.
#[test]
fn a_repeat_that_finds_the_shade_down_only_wakes_it() {
    let now = Instant::now();
    let mut screen = shaded(now);

    assert_eq!(
        moments(screen.deliver(SOFA_EVENTS, &key("KEY_UP", 2), false, now)),
        [Moment::Wake]
    );
}

#[test]
fn a_press_from_a_controller_pointed_elsewhere_leaves_the_shade_down() {
    let now = Instant::now();
    let mut screen = shaded(now);
    screen.deliver(SOFA_FOCUS, b"cinema", false, now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_SELECT", 1), false, now)
            .is_empty()
    );
    assert!(screen.asleep);
}

// The shade belongs to the idle screen, and a film covers the client, so a
// press that finds the shade down during a film reaches no client.
#[test]
fn a_press_that_finds_the_shade_down_during_a_film_only_wakes_it() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);
    screen.asleep = true;

    assert_eq!(
        moments(screen.deliver(SOFA_EVENTS, &key("KEY_SELECT", 1), false, now)),
        [Moment::Wake]
    );
}

#[test]
fn the_press_that_lifts_the_shade_says_it_reached_the_client() {
    let now = Instant::now();
    let mut screen = shaded(now);

    screen.deliver(SOFA_EVENTS, &key("KEY_SELECT", 1), false, now);

    assert_eq!(
        screen.take_lines(),
        ["KEY_SELECT from remote house/sofa woke the screen and passed to the client"]
    );
}

#[test]
fn a_press_on_a_dark_panel_says_why_it_only_woke_the_screen() {
    let now = Instant::now();
    let mut screen = off_window_ran_out(now);

    screen.deliver(SOFA_EVENTS, &key("KEY_UP", 1), false, now);

    assert_eq!(
        screen.take_lines(),
        [format!(
            "KEY_UP from remote house/sofa woke the screen and did nothing else, because the panel was off, published panel desire on to {PANEL}"
        )]
    );
}
