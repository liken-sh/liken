// `media-operator` reads the power keys from the controller's events topic
// and changes the `Player`'s `spec.power`, so this crate ignores them and
// does not pass them to its client.

use super::*;

#[test]
fn a_power_key_reaches_no_client_and_changes_nothing() {
    let now = Instant::now();
    let cases = [
        (false, Some(crate::panel::ON)),
        (true, Some(crate::panel::OFF)),
    ];
    for name in keys::POWER {
        for (asleep, panel_request) in cases {
            let mut screen = idling(&wiring(), now);
            screen.asleep = asleep;
            screen.panel_request = panel_request;

            let effects = screen.deliver(FIRST_REMOTE_EVENTS, &key(name, 1), false, now);

            assert!(effects.is_empty(), "{name} asleep {asleep}");
            assert_eq!(screen.asleep, asleep, "{name}");
            assert_eq!(screen.panel_request, panel_request, "{name}");
        }
    }
}

// A power key press alone does not show that anyone is using this screen.
#[test]
fn a_power_key_does_not_restart_the_fade_timer() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    let armed = screen.next_deadline();

    screen.deliver(
        FIRST_REMOTE_EVENTS,
        &key("KEY_POWER", 1),
        false,
        now + Duration::from_secs(60),
    );

    assert_eq!(screen.next_deadline(), armed);
}

#[test]
fn a_power_key_during_a_play_reaches_nothing() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    for name in keys::POWER {
        assert!(
            screen
                .deliver(FIRST_REMOTE_EVENTS, &key(name, 1), false, now)
                .is_empty(),
            "{name}"
        );
    }
}

#[test]
fn a_power_key_logs_that_media_operator_handles_it() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_POWER", 1), false, now);
    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_POWER", 2), false, now);
    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_POWER", 0), false, now);

    assert_eq!(
        screen.take_lines(),
        [
            "KEY_POWER from remote default/remote-1 ignored, because media-operator turns the player off and on"
        ]
    );
}

#[test]
fn a_power_command_on_the_commands_topic_changes_nothing() {
    let now = Instant::now();
    for payload in [&br#"{"action":"power"}"#[..], br#"{"action":"power-off"}"#] {
        let mut screen = idling(&wiring(), now);

        assert!(
            screen.deliver(COMMANDS, payload, false, now).is_empty(),
            "the payload {:?} produced effects, want none",
            String::from_utf8_lossy(payload)
        );
        assert!(
            screen.take_lines().is_empty(),
            "the payload {:?} wrote a log line, want none",
            String::from_utf8_lossy(payload)
        );
    }
}
