// The power keys. `media-operator` reads them off the controller's events
// topic and changes the `Player`'s `spec.power`, so a screen client neither
// acts on them nor hands them to the client, in any state: awake, asleep,
// idle, or playing.

use super::*;

#[test]
fn a_power_key_reaches_no_client_and_moves_nothing() {
    let now = Instant::now();
    let cases = [
        (false, Some(crate::panel::ON)),
        (true, Some(crate::panel::OFF)),
    ];
    for name in keys::POWER {
        for (asleep, desire) in cases {
            let mut screen = idling(&wiring(), now);
            screen.asleep = asleep;
            screen.desire = desire;

            let effects = screen.deliver(SOFA_EVENTS, &key(name, 1), false, now);

            assert!(effects.is_empty(), "{name} asleep {asleep}");
            assert_eq!(screen.asleep, asleep, "{name}");
            assert_eq!(screen.desire, desire, "{name}");
        }
    }
}

// A power press restarts no window, because it is not a person at this
// screen: the operator decides what it does to the Player.
#[test]
fn a_power_key_does_not_restart_the_quiet_window() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    let armed = screen.next_deadline();

    screen.deliver(
        SOFA_EVENTS,
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
                .deliver(SOFA_EVENTS, &key(name, 1), false, now)
                .is_empty(),
            "{name}"
        );
    }
}

#[test]
fn a_power_key_says_who_answers_it() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    screen.deliver(SOFA_EVENTS, &key("KEY_POWER", 1), false, now);
    screen.deliver(SOFA_EVENTS, &key("KEY_POWER", 2), false, now);
    screen.deliver(SOFA_EVENTS, &key("KEY_POWER", 0), false, now);

    assert_eq!(
        screen.take_lines(),
        [
            "KEY_POWER from remote house/sofa left to media-operator, which turns the player off and on"
        ]
    );
}

// The commands topic carries no power ask, so a writer that publishes one
// there moves nothing here.
#[test]
fn a_power_ask_on_the_commands_topic_moves_nothing() {
    let now = Instant::now();
    for payload in [&br#"{"action":"power"}"#[..], br#"{"action":"power-off"}"#] {
        let mut screen = idling(&wiring(), now);

        assert!(screen.deliver(COMMANDS, payload, false, now).is_empty());
        assert!(screen.take_lines().is_empty());
    }
}
