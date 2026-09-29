// The lines the rules write: one per operation a person caused, and none
// for a repeat, a release, a catch-up, or a fold that moves nothing.

use super::*;

/// A screen that plays nothing, with a quiet window and an off window, so a
/// test reads the lines the two windows write.
fn windowed() -> Wiring {
    Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    }
}

#[test]
fn a_press_writes_one_line_that_says_what_it_did() {
    let cases = [
        (
            wiring(),
            "KEY_UP",
            "KEY_UP from remote house/sofa passed to the client".to_string(),
        ),
        (
            wiring(),
            "KEY_VOLUMEUP",
            format!(
                "KEY_VOLUMEUP from remote house/sofa: volume +5, published level 100, not muted to {VOLUME}"
            ),
        ),
        (
            wiring(),
            "KEY_VOLUMEDOWN",
            format!(
                "KEY_VOLUMEDOWN from remote house/sofa: volume -5, published level 95, not muted to {VOLUME}"
            ),
        ),
        (
            wiring(),
            "KEY_MUTE",
            format!(
                "KEY_MUTE from remote house/sofa: mute or unmute, published level 100, muted to {VOLUME}"
            ),
        ),
        (
            Wiring {
                volume_topic: String::new(),
                ..wiring()
            },
            "KEY_MUTE",
            "KEY_MUTE from remote house/sofa ignored, because the player has no sinks".to_string(),
        ),
        (
            wiring(),
            keys::CYCLE,
            format!(
                "KEY_CYCLEWINDOWS from remote house/sofa: cycle focus, published the cycle request to {SOFA_FOCUS}/cycle"
            ),
        ),
        (
            powered(),
            "KEY_POWER",
            format!("KEY_POWER from remote house/sofa: power, published the toggle to {POWER}"),
        ),
    ];
    for (wiring, name, want) in cases {
        let now = Instant::now();
        let mut screen = idling(&wiring, now);

        screen.deliver(SOFA_EVENTS, &key(name, 1), false, now);

        assert_eq!(screen.take_lines(), [want]);
    }
}

#[test]
fn a_cycle_with_no_focus_topic_says_why() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.remotes[0].focus = String::new();

    screen.deliver(SOFA_EVENTS, &key(keys::CYCLE, 1), false, now);

    assert_eq!(
        screen.take_lines(),
        ["KEY_CYCLEWINDOWS from remote house/sofa ignored, because the remote has no focus topic"]
    );
}

#[test]
fn a_repeat_and_a_release_write_no_line() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    for (name, value) in [
        ("KEY_VOLUMEUP", 2),
        ("KEY_VOLUMEUP", 0),
        ("KEY_UP", 2),
        ("KEY_UP", 0),
        ("KEY_POWER", 2),
    ] {
        screen.deliver(SOFA_EVENTS, &key(name, value), false, now);
    }

    assert!(screen.take_lines().is_empty());
}

#[test]
fn a_press_pointed_elsewhere_says_why_once() {
    let cases = [
        (
            "console",
            "KEY_UP from remote house/sofa ignored, because focus is on player console",
        ),
        (
            "",
            "KEY_UP from remote house/sofa ignored, because no focus mark names a player for this remote",
        ),
    ];
    for (mark, want) in cases {
        let now = Instant::now();
        let mut screen = idling(&wiring(), now);
        screen.marks[0].player = mark.into();

        for value in [1, 2, 2, 0] {
            screen.deliver(SOFA_EVENTS, &key("KEY_UP", value), false, now);
        }

        assert_eq!(screen.take_lines(), [want]);
    }
}

#[test]
fn a_press_during_a_film_writes_no_line_here() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    screen.deliver(SOFA_EVENTS, &key("KEY_UP", 1), false, now);

    assert!(screen.take_lines().is_empty());
}

#[test]
fn the_two_windows_each_write_a_line() {
    let now = Instant::now();
    let mut screen = idling(&windowed(), now);

    screen.tick(now + Duration::from_secs(600));
    screen.tick(now + Duration::from_secs(1800));

    assert_eq!(
        screen.take_lines(),
        [
            "the quiet window of 600 s ran out, so the shade is down".to_string(),
            format!("the off window of 1800 s ran out, published panel desire off to {PANEL}"),
        ]
    );
}

#[test]
fn a_desire_with_no_panel_topic_says_so() {
    let now = Instant::now();
    let wiring = Wiring {
        panel_topic: String::new(),
        ..windowed()
    };
    let mut screen = idling(&wiring, now);

    screen.tick(now + Duration::from_secs(600));
    screen.take_lines();
    screen.tick(now + Duration::from_secs(1800));

    assert_eq!(
        screen.take_lines(),
        ["the off window of 1800 s ran out, and the player has no panel topic for the off desire"]
    );
}

#[test]
fn a_press_that_wakes_the_screen_says_so_and_names_the_desire_it_lifts() {
    let now = Instant::now();
    let mut screen = idling(&windowed(), now);
    screen.tick(now + Duration::from_secs(600));
    screen.tick(now + Duration::from_secs(1800));
    screen.take_lines();

    screen.deliver(SOFA_EVENTS, &key("KEY_UP", 1), false, now);

    assert_eq!(
        screen.take_lines(),
        [format!(
            "KEY_UP from remote house/sofa woke the screen and did nothing else, published panel desire on to {PANEL}"
        )]
    );
}

#[test]
fn the_client_asking_for_the_shade_writes_a_line() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    screen.sleep(now);
    screen.sleep(now);

    assert_eq!(
        screen.take_lines(),
        ["the client asked for the shade, so the shade is down"]
    );
}

#[test]
fn an_ask_on_the_commands_topic_writes_a_line() {
    let cases = [
        (
            r#"{"action":"home"}"#,
            format!("{COMMANDS} asked for home, passed to the client as KEY_HOMEPAGE"),
        ),
        (
            r#"{"action":"play-next","request":{"id":"b"}}"#,
            format!("{COMMANDS} asked for play-next, passed to the client"),
        ),
    ];
    for (payload, want) in cases {
        let now = Instant::now();
        let mut screen = idling(&wiring(), now);

        screen.deliver(COMMANDS, payload.as_bytes(), false, now);

        assert_eq!(screen.take_lines(), [want]);
    }
}

#[test]
fn a_status_that_moves_nothing_writes_no_line() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    screen.deliver(STATUS, &status("Idle"), true, now);
    screen.deliver(VOLUME, br#"{"level":40,"muted":false}"#, true, now);

    assert!(screen.take_lines().is_empty());
}

#[test]
fn a_topic_of_another_shape_names_itself() {
    assert_eq!(remote_name("somewhere/else"), "somewhere/else");
    assert_eq!(remote_name("a/remotes/b/remotes/den/pad/focus"), "den/pad");
}
