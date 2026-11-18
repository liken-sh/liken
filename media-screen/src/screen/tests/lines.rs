// The screen writes one log line for each operation that a person caused,
// and none for a repeat, a release, a retained message, or a message that
// changes nothing.

use super::*;

/// A `Player` with both a fade timer and an off timer.
fn with_both_timers() -> Wiring {
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
            "KEY_UP from remote default/remote-1 passed to the client".to_string(),
        ),
        (
            wiring(),
            "KEY_VOLUMEUP",
            "KEY_VOLUMEUP from remote default/remote-1 passed to the client".to_string(),
        ),
        (
            wiring(),
            keys::CYCLE,
            format!(
                "KEY_CYCLEWINDOWS from remote default/remote-1: cycle focus, published the cycle request to {FIRST_REMOTE_FOCUS}/cycle"
            ),
        ),
    ];
    for (wiring, name, want) in cases {
        let now = Instant::now();
        let mut screen = idling(&wiring, now);

        screen.deliver(FIRST_REMOTE_EVENTS, &key(name, 1), false, now);

        assert_eq!(screen.take_lines(), [want]);
    }
}

#[test]
fn a_cycle_with_no_focus_topic_says_why() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.remotes[0].focus = String::new();

    screen.deliver(FIRST_REMOTE_EVENTS, &key(keys::CYCLE, 1), false, now);

    assert_eq!(
        screen.take_lines(),
        [
            "KEY_CYCLEWINDOWS from remote default/remote-1 ignored, because the remote has no focus topic"
        ]
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
        screen.deliver(FIRST_REMOTE_EVENTS, &key(name, value), false, now);
    }

    assert!(
        screen.take_lines().is_empty(),
        "a repeat and a release wrote a log line, want none"
    );
}

#[test]
fn a_press_pointed_elsewhere_says_why_once() {
    let cases = [
        (
            "console",
            "KEY_UP from remote default/remote-1 ignored, because focus is on player console",
        ),
        (
            "",
            "KEY_UP from remote default/remote-1 ignored, because the remote's focus topic names no player",
        ),
    ];
    for (player, want) in cases {
        let now = Instant::now();
        let mut screen = idling(&wiring(), now);
        screen.focus[0].player = player.into();

        for value in [1, 2, 2, 0] {
            screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_UP", value), false, now);
        }

        assert_eq!(screen.take_lines(), [want]);
    }
}

#[test]
fn a_press_during_a_film_writes_no_line_here() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_UP", 1), false, now);

    assert!(
        screen.take_lines().is_empty(),
        "a press during a film wrote a log line, want none"
    );
}

#[test]
fn each_timer_writes_a_line() {
    let now = Instant::now();
    let mut screen = idling(&with_both_timers(), now);

    screen.tick(now + Duration::from_secs(600));
    screen.tick(now + Duration::from_secs(1800));

    assert_eq!(
        screen.take_lines(),
        [
            "the fade timer of 600 s expired, so the screen is asleep".to_string(),
            format!("the off timer of 1800 s expired, published panel request off to {PANEL}"),
        ]
    );
}

#[test]
fn a_panel_request_with_no_panel_topic_is_logged() {
    let now = Instant::now();
    let wiring = Wiring {
        panel_topic: String::new(),
        ..with_both_timers()
    };
    let mut screen = idling(&wiring, now);

    screen.tick(now + Duration::from_secs(600));
    screen.take_lines();
    screen.tick(now + Duration::from_secs(1800));

    assert_eq!(
        screen.take_lines(),
        ["the off timer of 1800 s expired, and the player has no panel topic to request off"]
    );
}

#[test]
fn a_sleep_requested_by_the_client_writes_a_line() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    screen.sleep(now);
    screen.sleep(now);

    assert_eq!(
        screen.take_lines(),
        ["the client requested sleep, so the screen is asleep"]
    );
}

#[test]
fn a_command_on_the_commands_topic_writes_a_line() {
    let cases = [
        (
            r#"{"action":"home"}"#,
            format!("{COMMANDS} received home, passed to the client as KEY_HOMEPAGE"),
        ),
        (
            r#"{"action":"play-next","request":{"id":"b"}}"#,
            format!("{COMMANDS} received play-next, passed to the client"),
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
fn a_status_that_changes_nothing_writes_no_line() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    screen.deliver(STATUS, &status("Idle"), true, now);
    screen.deliver(VOLUME, br#"{"level":0.4,"muted":false}"#, true, now);

    assert!(
        screen.take_lines().is_empty(),
        "a status that changes nothing wrote a log line, want none"
    );
}

#[test]
fn remote_name_returns_an_unexpected_topic_unchanged() {
    assert_eq!(remote_name("somewhere/else"), "somewhere/else");
    assert_eq!(
        remote_name("a/remotes/b/remotes/default/pad/focus"),
        "default/pad"
    );
}
