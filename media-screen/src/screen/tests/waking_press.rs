// A press on a sleeping screen. When the panel is still lit, the client
// receives `Moment::Wake` and then the press, so a person does not have to
// press twice each time the screen fades. When the panel is off, the press only
// wakes the screen, because the person cannot see what it would act on.

use super::*;

/// A `Player` whose fade timer expired and that has no off timer, so the
/// screen is asleep and the panel is still on.
fn asleep_with_panel_on(now: Instant) -> Screen {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let mut screen = idling(&wiring, now);
    screen.tick(now + Duration::from_secs(600));
    screen.take_lines();
    screen
}

/// A `Player` whose off timer expired after its fade timer, so the screen
/// is asleep and the panel is off.
fn off_timer_expired(now: Instant) -> Screen {
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

/// A `Player` that `media-operator` told to sleep on the power topic, because
/// the `Player` turned off or the TV went to standby, so the screen is asleep
/// and the panel is off.
fn slept_on_request(now: Instant) -> Screen {
    let mut screen = idling(&wiring(), now);
    screen.deliver(POWER, br#"{"action":"sleep"}"#, false, now);
    screen.take_lines();
    screen
}

#[test]
fn the_press_that_wakes_the_screen_reaches_the_client_after_the_wake() {
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
        let mut screen = asleep_with_panel_on(now);

        assert_eq!(
            moments(screen.deliver(FIRST_REMOTE_EVENTS, &key(name, 1), false, pressed)),
            [Moment::Wake, Moment::Press(name.into())],
            "{name}"
        );
        assert!(!screen.asleep, "{name}");
    }
}

// The fade timer leaves the panel request at `on`, so there is nothing to
// publish.
#[test]
fn the_press_that_wakes_the_screen_over_a_lit_panel_publishes_no_panel_request() {
    let now = Instant::now();
    let mut screen = asleep_with_panel_on(now);

    let effects = screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_SELECT", 1), false, now);

    assert!(
        publishes(effects).is_empty(),
        "the press published a panel request, want none"
    );
}

#[test]
fn the_press_that_wakes_the_screen_restarts_the_fade_timer() {
    let now = Instant::now();
    let mut screen = asleep_with_panel_on(now);

    let pressed = now + Duration::from_secs(700);
    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_DOWN", 1), false, pressed);

    assert_eq!(
        screen.next_deadline(),
        Some(pressed + Duration::from_secs(600))
    );
}

#[test]
fn a_press_on_a_dark_panel_only_wakes_it_and_lights_the_panel() {
    let now = Instant::now();
    for (mut screen, way) in [
        (off_timer_expired(now), "the off timer"),
        (slept_on_request(now), "a sleep request"),
    ] {
        let effects = screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_SELECT", 1), false, now);

        assert_eq!(moments(effects.clone()), [Moment::Wake], "{way}");
        assert_eq!(
            publishes(effects),
            [panel_request(crate::panel::ON)],
            "{way}"
        );
    }
}

#[test]
fn back_on_a_sleeping_screen_only_wakes_it() {
    let now = Instant::now();
    for name in ["KEY_BACK", "KEY_ESC", "KEY_EXIT", "KEY_BACKSPACE"] {
        let mut screen = asleep_with_panel_on(now);

        assert_eq!(
            moments(screen.deliver(FIRST_REMOTE_EVENTS, &key(name, 1), false, now)),
            [Moment::Wake],
            "{name}"
        );
        assert!(!screen.asleep, "{name}");
    }
}

#[test]
fn back_on_a_sleeping_screen_logs_why_it_only_woke_it() {
    let now = Instant::now();
    let mut screen = asleep_with_panel_on(now);

    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_BACK", 1), false, now);

    assert_eq!(
        screen.take_lines(),
        [
            "KEY_BACK from remote default/remote-1 woke the screen and did nothing else, because back on a sleeping screen only wakes it"
        ]
    );
}

/// A `Player` whose client put the screen to sleep on a back press that is
/// still held, as the stock idle screen does.
fn held_back_put_the_screen_to_sleep(now: Instant) -> Screen {
    let mut screen = idling(&wiring(), now);
    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_BACK", 1), false, now);
    screen.sleep(now);
    screen
}

/// A `Player` that `media-operator` told to sleep while a key was held down.
fn slept_on_request_while_a_key_is_held(now: Instant) -> Screen {
    let mut screen = idling(&wiring(), now);
    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_UP", 1), false, now);
    screen.deliver(POWER, br#"{"action":"sleep"}"#, false, now);
    screen
}

// The key was already held when the screen went to sleep, and its press has
// acted. Waking on the repeat would undo a sleep that the client just
// requested, or light a panel that was just turned off.
#[test]
fn a_repeat_on_a_sleeping_screen_does_nothing() {
    let now = Instant::now();
    for (mut screen, name, way) in [
        (asleep_with_panel_on(now), "KEY_UP", "the fade timer"),
        (
            held_back_put_the_screen_to_sleep(now),
            "KEY_BACK",
            "the client's sleep",
        ),
        (
            slept_on_request_while_a_key_is_held(now),
            "KEY_UP",
            "a sleep request",
        ),
        (off_timer_expired(now), "KEY_SELECT", "the off timer"),
    ] {
        let panel_request = screen.panel_request;

        assert!(
            screen
                .deliver(FIRST_REMOTE_EVENTS, &key(name, 2), false, now)
                .is_empty(),
            "{way}"
        );
        assert!(screen.asleep, "{way}");
        assert_eq!(screen.panel_request, panel_request, "{way}");
    }
}

#[test]
fn a_held_key_whose_press_only_woke_the_screen_repeats_nothing() {
    let now = Instant::now();
    for (mut screen, name, first) in [
        (asleep_with_panel_on(now), "KEY_BACK", 1),
        (off_timer_expired(now), "KEY_SELECT", 1),
        (asleep_with_panel_on(now), "KEY_UP", 2),
    ] {
        screen.deliver(FIRST_REMOTE_EVENTS, &key(name, first), false, now);

        for value in [2, 2, 0] {
            assert!(
                screen
                    .deliver(FIRST_REMOTE_EVENTS, &key(name, value), false, now)
                    .is_empty(),
                "{name} at value {value}"
            );
        }
    }
}

#[test]
fn after_the_release_the_next_press_acts() {
    let now = Instant::now();
    let mut screen = asleep_with_panel_on(now);
    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_BACK", 1), false, now);
    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_BACK", 0), false, now);

    assert_eq!(
        moments(screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_BACK", 1), false, now)),
        [Moment::Press("KEY_BACK".into())]
    );
}

#[test]
fn a_held_key_whose_press_woke_and_acted_repeats_to_the_client() {
    let now = Instant::now();
    let mut screen = asleep_with_panel_on(now);
    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_DOWN", 1), false, now);

    assert_eq!(
        moments(screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_DOWN", 2), false, now)),
        [Moment::Press("KEY_DOWN".into())]
    );
}

#[test]
fn ignored_repeats_on_one_controller_leave_another_controllers_repeats_alone() {
    let now = Instant::now();
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..two_remotes()
    };
    let mut screen = idling(&wiring, now);
    screen.tick(now + Duration::from_secs(600));
    screen.deliver(SECOND_REMOTE_EVENTS, &key("KEY_BACK", 1), false, now);

    assert_eq!(
        moments(screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_BACK", 2), false, now)),
        [Moment::Press("KEY_BACK".into())]
    );
}

#[test]
fn a_press_from_a_controller_pointed_elsewhere_leaves_the_screen_asleep() {
    let now = Instant::now();
    let mut screen = asleep_with_panel_on(now);
    screen.deliver(FIRST_REMOTE_FOCUS, b"cinema", false, now);

    assert!(
        screen
            .deliver(FIRST_REMOTE_EVENTS, &key("KEY_SELECT", 1), false, now)
            .is_empty(),
        "a press from a controller pointed elsewhere produced effects, want none"
    );
    assert!(
        screen.asleep,
        "the screen is awake after a press from a controller pointed elsewhere, want asleep"
    );
}

// During a film the idle client is not on screen, so the press is not passed
// to it.
#[test]
fn a_press_on_a_sleeping_screen_during_a_film_only_wakes_it() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);
    screen.asleep = true;

    assert_eq!(
        moments(screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_SELECT", 1), false, now)),
        [Moment::Wake]
    );
}

#[test]
fn the_press_that_wakes_the_screen_says_it_reached_the_client() {
    let now = Instant::now();
    let mut screen = asleep_with_panel_on(now);

    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_SELECT", 1), false, now);

    assert_eq!(
        screen.take_lines(),
        ["KEY_SELECT from remote default/remote-1 woke the screen and passed to the client"]
    );
}

#[test]
fn a_press_on_a_dark_panel_says_why_it_only_woke_the_screen() {
    let now = Instant::now();
    let mut screen = off_timer_expired(now);

    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_UP", 1), false, now);

    assert_eq!(
        screen.take_lines(),
        [format!(
            "KEY_UP from remote default/remote-1 woke the screen and did nothing else, because the panel was off, published panel request on to {PANEL}"
        )]
    );
}
